package harvest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
)

// The fixtures (testdata/notion) are a captured public Notion page's block
// tree, scrubbed: the page's shell as the site serves it cut to its title;
// the page-chunk API's answer (loadPageChunk, one chunk, its cursor done), and
// every record the site's client reads for the blocks that chunk does not
// hold (syncRecordValuesSpace, three rounds: toggle answers, toggleable
// headings' children, and their children), merged into one captured answer
// envelope; four blocks of another captured public page (a callout, two
// bulleted items, each a mention of an external object, and a divider)
// appended to the page, so those kinds are read from records too. Every
// title is a placeholder naming
// the block's position in the tree and its type ("b008 toggle"), every link an
// example.com one. oracle.json is the tree as the API states it, computed
// from the answers alone: the blocks reached, their count by type, and every
// titled block's placeholder in tree order.
const (
	notionHost = "example.notion.site"
	notionPath = "/An-Example-FAQ-2d1d59a4168c4524bed0a40b04de892a"
	notionURL  = "https://" + notionHost + notionPath
)

type notionOracle struct {
	Types   map[string]int `json:"types"`
	Reached int            `json:"reached"`
	Titled  []string       `json:"titled"`
}

// notionSite serves the page, its chunk, and the records its client asks
// for; unserved names blocks answered as no reader may see them, refused an
// API path answered with that status.
type notionSite struct {
	mu       sync.Mutex
	page     string
	chunk    string
	records  map[string]json.RawMessage
	unserved map[string]bool
	refused  map[string]int
	requests []string
	headers  []http.Header
}

func newNotionSite(t *testing.T) *notionSite {
	t.Helper()
	var records struct {
		RecordMap struct {
			Block map[string]json.RawMessage `json:"block"`
		} `json:"recordMap"`
	}
	if err := json.Unmarshal([]byte(socialFixture(t, "notion/records.json")), &records); err != nil {
		t.Fatalf("records fixture: %v", err)
	}
	return &notionSite{
		page: socialFixture(t, "notion/page.html"), chunk: socialFixture(t, "notion/chunk-0.json"),
		records: records.RecordMap.Block, unserved: map[string]bool{}, refused: map[string]int{},
	}
}

func (site *notionSite) roundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Host != notionHost {
		return response(request, http.StatusNotFound, "text/html", "<html><body>not found</body></html>"), nil
	}
	if !strings.HasPrefix(request.URL.Path, "/api/v3/") {
		if request.URL.Path != notionPath {
			return response(request, http.StatusNotFound, "text/html", "<html><body>not found</body></html>"), nil
		}
		return response(request, http.StatusOK, "text/html; charset=utf-8", site.page), nil
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	site.mu.Lock()
	site.requests = append(site.requests, request.Method+" "+request.URL.Path)
	site.headers = append(site.headers, request.Header.Clone())
	site.mu.Unlock()
	if status := site.refused[request.URL.Path]; status != 0 {
		return response(request, status, "text/html", "<html><body>refused</body></html>"), nil
	}
	switch request.URL.Path {
	case "/api/v3/loadPageChunk":
		var ask struct {
			PageID      string `json:"pageId"`
			ChunkNumber int    `json:"chunkNumber"`
		}
		if json.Unmarshal(body, &ask) != nil || ask.PageID != "2d1d59a4-168c-4524-bed0-a40b04de892a" ||
			ask.ChunkNumber != 0 {
			return response(request, http.StatusBadRequest, "application/json", `{"name":"ValidationError"}`), nil
		}
		return response(request, http.StatusOK, "application/json; charset=utf-8", site.chunk), nil
	case "/api/v3/syncRecordValuesSpace":
		var ask struct {
			Requests []struct {
				Pointer struct {
					Table   string `json:"table"`
					ID      string `json:"id"`
					SpaceID string `json:"spaceId"`
				} `json:"pointer"`
			} `json:"requests"`
		}
		if json.Unmarshal(body, &ask) != nil || len(ask.Requests) == 0 {
			return response(request, http.StatusBadRequest, "application/json", `{"name":"ValidationError"}`), nil
		}
		answer := map[string]json.RawMessage{}
		for _, one := range ask.Requests {
			record, ok := site.records[one.Pointer.ID]
			switch {
			case one.Pointer.Table != "block" || one.Pointer.SpaceID == "":
				return response(request, http.StatusBadRequest, "application/json", `{"name":"ValidationError"}`), nil
			case site.unserved[one.Pointer.ID]:
				answer[one.Pointer.ID] = json.RawMessage(`{"value":{"role":"none"}}`)
			case ok:
				answer[one.Pointer.ID] = record
			}
		}
		raw, err := json.Marshal(map[string]any{"recordMap": map[string]any{"__version__": 3, "block": answer}})
		if err != nil {
			return nil, err
		}
		return response(request, http.StatusOK, "application/json; charset=utf-8", string(raw)), nil
	}
	return response(request, http.StatusNotFound, "application/json", `{}`), nil
}

func (site *notionSite) harvester(t *testing.T, served bool) *Harvester {
	t.Helper()
	missing := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusNotFound, "application/json", `{}`), nil
	})
	converter := &browserSpyConverter{}
	if served {
		converter.convertFn = func(context.Context, string, string, []byte) (string, error) { return servedPage, nil }
	}
	return mustNew(t, Options{
		CacheDir:    t.TempDir(),
		Client:      &http.Client{Transport: roundTripFunc(site.roundTrip)},
		Chrome:      &http.Client{Transport: roundTripFunc(site.roundTrip)},
		Jina:        &http.Client{Transport: missing},
		OA:          &http.Client{Transport: missing},
		Converter:   converter,
		BrowserRung: browserOff(),
		Clock:       newPacingClock(),
	})
}

func notionFixtureOracle(t *testing.T) notionOracle {
	t.Helper()
	var oracle notionOracle
	if err := json.Unmarshal([]byte(socialFixture(t, "notion/oracle.json")), &oracle); err != nil {
		t.Fatalf("oracle fixture: %v", err)
	}
	return oracle
}

// TestNotionPageLoadsEveryBlock: a public Notion page renders its whole block
// tree from the site's own API on the page's host — the page chunk, then the
// records of every block the chunk does not hold, round after round (a
// toggle's answer, a toggleable heading's children), sending no credential;
// every titled block renders once, in tree order; headings, toggles, lists,
// callouts, bookmarks and dividers keep their form; the block counts
// reconcile with the API's own; the artifact is complete, and a second
// harvest is identical.
func TestNotionPageLoadsEveryBlock(t *testing.T) {
	site := newNotionSite(t)
	oracle := notionFixtureOracle(t)
	h := site.harvester(t, false)
	result := h.FetchWithOptions(context.Background(), notionURL, FetchOptions{Refresh: true})
	if result.Error != "" || result.Partial != "" {
		t.Fatalf("the page is not complete: method=%q partial=%q error=%q\n%.2500s", result.Method, result.Partial,
			result.Error, result.Content)
	}
	at := -1
	for _, label := range oracle.Titled {
		index := strings.Index(result.Content, label)
		if index <= at {
			t.Fatalf("block %q is missing or out of tree order (at %d, previous at %d)\n%.3000s", label, index, at,
				result.Content)
		}
		at = index
	}
	var types []string
	for kind, count := range oracle.Types {
		types = append(types, fmt.Sprintf("%s %d", kind, count))
	}
	sort.Strings(types)
	for _, text := range []string{
		"# An Example FAQ",
		fmt.Sprintf("**Blocks:** %d stated · %d loaded", oracle.Reached, oracle.Reached),
		"**Block types:** " + strings.Join(types, ", "),
		"[b003 bookmark](https://example.com/link-1)",
		"## b116 header",
		"#### b007 sub_sub_header",
		"**b008 toggle**\n\nb009 text",
		"1. b",
		"> 💡 b223 callout",
		"\n\n---",
		"(https://example.com/source-",
	} {
		if !strings.Contains(result.Content, text) {
			t.Fatalf("the artifact lacks %q:\n%.3000s", text, result.Content)
		}
	}
	if len(site.requests) < 2 || site.requests[0] != "POST /api/v3/loadPageChunk" {
		t.Fatalf("API requests %v, want the page chunk and then its records", site.requests)
	}
	for _, header := range site.headers {
		if header.Get("Authorization") != "" || header.Get("Cookie") != "" {
			t.Fatalf("an API request carried a credential: %v", header)
		}
	}
	again := h.FetchWithOptions(context.Background(), notionURL, FetchOptions{Refresh: true})
	if again.Content != result.Content {
		t.Fatal("a second harvest of the same page differs")
	}
}

// TestNotionBlocksNotServedAreNamed: a block the site answers no reader may
// see, or whose records it refuses, is never dropped silently: the page
// renders what was served, and the partial marker names the rest, stated ·
// loaded.
func TestNotionBlocksNotServedAreNamed(t *testing.T) {
	oracle := notionFixtureOracle(t)
	t.Run("unserved", func(t *testing.T) {
		site := newNotionSite(t)
		// b009 text: the first toggle's answer.
		for id, record := range site.records {
			if strings.Contains(string(record), `"b009 text"`) {
				site.unserved[id] = true
			}
		}
		result := site.harvester(t, false).FetchWithOptions(context.Background(), notionURL,
			FetchOptions{Refresh: true})
		if strings.Contains(result.Content, "b009 text") || !strings.Contains(result.Content, "b010 toggle") {
			t.Fatalf("the served blocks did not render without the unserved one:\n%.2000s", result.Content)
		}
		want := fmt.Sprintf("notion page: %d of %d blocks loaded; 1 block the site serves to no anonymous reader",
			oracle.Reached-1, oracle.Reached)
		if !strings.Contains(result.Partial, want) {
			t.Fatalf("the partial marker does not name the unserved block: %q, want %q", result.Partial, want)
		}
	})
	t.Run("records refused", func(t *testing.T) {
		site := newNotionSite(t)
		site.refused["/api/v3/syncRecordValuesSpace"] = http.StatusForbidden
		result := site.harvester(t, false).FetchWithOptions(context.Background(), notionURL,
			FetchOptions{Refresh: true})
		if !strings.Contains(result.Content, "b008 toggle") || strings.Contains(result.Content, "b009 text") {
			t.Fatalf("the chunk's blocks did not render alone:\n%.2000s", result.Content)
		}
		// The chunk names its 59 blocks' children, 70 ids; their own children
		// are not known until those load.
		if !strings.Contains(result.Partial, "notion page: 60 of 130 blocks loaded; 70 blocks not read") ||
			!strings.Contains(result.Partial, "HTTP 403") {
			t.Fatalf("the partial marker does not name the unloaded blocks and why: %q", result.Partial)
		}
	})
}

// TestNotionTreeNotLoadedFallsThrough: a page whose chunk the API refuses is
// not rendered from nothing: it falls through to the generic path, which
// names why the tree did not load.
func TestNotionTreeNotLoadedFallsThrough(t *testing.T) {
	site := newNotionSite(t)
	site.refused["/api/v3/loadPageChunk"] = http.StatusForbidden
	result := site.harvester(t, true).FetchWithOptions(context.Background(), notionURL, FetchOptions{Refresh: true})
	if !strings.Contains(result.Content, "SERVED PAGE") {
		t.Fatalf("the page did not fall through to the generic path: error=%q\n%.1500s", result.Error,
			result.Content)
	}
	if !strings.Contains(result.Partial, "the page's block tree did not load") {
		t.Fatalf("the partial marker does not name the unloaded tree: %q", result.Partial)
	}
}
