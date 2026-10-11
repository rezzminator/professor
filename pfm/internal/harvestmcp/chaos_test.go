package harvestmcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/harvest"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// chaosSeed replays one service chaos workload: `go test -run
// '^TestServiceChaos$' ./internal/harvestmcp/ -args -chaos.seed=N`. 0 draws a
// fresh seed, which every failure prints. It fixes the workload, never the
// scheduler's interleaving.
var chaosSeed = flag.Uint64("chaos.seed", 0, "chaos suite workload seed (0 = time-based, printed on failure)")

var (
	chaosMarkerRE = regexp.MustCompile(`chaos-marker-[a-z]+-\d+`)
	chaosIDRE     = regexp.MustCompile(`[a-z]+-\d+`)
	chaosHeaderRE = regexp.MustCompile(`(?m)^=== \[(\d+)/(\d+)\] (.*)$`)
	chaosTagRE    = regexp.MustCompile(`<[^>]+>`)
)

// serviceChaosItem is one source the service storm reads: field is the read
// input field it goes in ("" for a download or a search).
type serviceChaosItem struct {
	class, id, source, field, marker string
	body                             []byte
}

// chaosTransport is a fixture RoundTripper.
type chaosTransport func(*http.Request) (*http.Response, error)

func (transport chaosTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

// serviceChaosRig is the fixture world behind one Service.
type serviceChaosRig struct {
	t        *testing.T
	seed     uint64
	items    map[string]*serviceChaosItem
	slowGate chan struct{}
}

func (rig *serviceChaosRig) errorf(format string, args ...any) {
	rig.t.Helper()
	rig.t.Errorf("seed %d: "+format, append([]any{rig.seed}, args...)...)
}

func serviceChaosPage(marker string, words int) string {
	var text strings.Builder
	text.WriteString(marker)
	for index := range words {
		fmt.Fprintf(&text, " word%dof%s", index, strings.TrimPrefix(marker, "chaos-marker-"))
	}
	return "<html><head><title>" + marker + "</title></head><body><article><p>" + text.String() +
		"</p></article></body></html>"
}

// direct serves every item's own URL; every other host (a reader service, an
// archive, a resolver API) is unreachable.
func (rig *serviceChaosRig) direct(request *http.Request) (*http.Response, error) {
	item := rig.items[chaosIDRE.FindString(request.URL.Path)]
	if item == nil || request.URL.Host != "ok.chaos.test" || item.class == "fail" {
		return nil, fmt.Errorf("fixture: %s is unreachable", request.URL.Host)
	}
	reply := func(status int, contentType, body string) *http.Response {
		return &http.Response{
			StatusCode: status, Status: http.StatusText(status), Request: request,
			Header:        http.Header{"Content-Type": {contentType}},
			Body:          io.NopCloser(strings.NewReader(body)),
			ContentLength: int64(len(body)),
		}
	}
	switch item.class {
	case "slow":
		select {
		case <-rig.slowGate:
		case <-request.Context().Done():
			return nil, request.Context().Err()
		}
	case "download":
		return reply(http.StatusOK, "application/zip", string(item.body)), nil
	}
	words := 200
	if item.class == "partial" {
		words = 420
	}
	return reply(http.StatusOK, "text/html", serviceChaosPage(item.marker, words)), nil
}

// Convert keeps a page's text (a sliver of a partial one) and refuses a busy
// page for load, as the converter pool does when full.
func (rig *serviceChaosRig) Convert(ctx context.Context, _, _ string, body []byte) (string, error) {
	if bytes.Contains(body, []byte("chaos-marker-busy-")) {
		harvest.NoteRefusedForLoad(ctx)
		return "", errors.New("fixture converter refused: every worker is busy")
	}
	words := strings.Fields(chaosTagRE.ReplaceAllString(string(body), " "))
	if bytes.Contains(body, []byte("chaos-marker-partial-")) {
		words = words[:60]
	}
	return "# Chaos page\n\n" + strings.Join(words, " "), nil
}

// searxng answers a SearXNG query with two results naming the query's item.
func (rig *serviceChaosRig) searxng(writer http.ResponseWriter, request *http.Request) {
	item := rig.items[chaosIDRE.FindString(strings.TrimPrefix(request.URL.Query().Get("q"), "chaos-marker-"))]
	if item == nil {
		http.Error(writer, "unknown query", http.StatusBadGateway)
		return
	}
	results := []map[string]any{}
	for index := range 2 {
		results = append(results, map[string]any{
			"title": item.marker, "url": fmt.Sprintf("https://result.chaos.test/%s/%d", item.id, index),
			"content": item.marker, "engine": "fixture",
		})
	}
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(map[string]any{"results": results}); err != nil {
		rig.errorf("encode the search answer: %v", err)
	}
}

// serviceChaosCall is one tool call of the storm and the answer it got.
type serviceChaosCall struct {
	tool     string
	args     map[string]any
	items    []*serviceChaosItem // in the order the call names them
	sizeOnly bool
	ask      bool
	cancel   bool
	result   *mcp.CallToolResult
	err      error
}

// TestServiceChaos fires every kind of harvester tool call at one Service at
// once, over several MCP sessions: reads of pages, partial pages, failing
// hosts, a slow page some callers abandon mid-walk, a page the converter
// refuses for load, local files, cached and failing publications — whole,
// size-only and refreshed, alone or batched, with ask over a fake engine —
// downloads, literature searches whose every source fails and web searches
// over a fixture SearXNG, each key shared by many calls. It checks
// invariants, never timing: one block per item, in
// order, carrying only its own item's content or an error line (an error
// never reads as absence); an answer over exactly the items the call read;
// no goroutine outliving the storm. Serial: it counts the process's goroutines.
func TestServiceChaos(t *testing.T) {
	seed := *chaosSeed
	if seed == 0 {
		seed = uint64(time.Now().UnixNano())
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf(
				"reproduce the workload: go test -run '^TestServiceChaos$' ./internal/harvestmcp/ -args -chaos.seed=%d",
				seed,
			)
		}
	})
	rng := rand.New(rand.NewPCG(seed, 0x6d6370))
	rig := &serviceChaosRig{t: t, seed: seed, items: map[string]*serviceChaosItem{}, slowGate: make(chan struct{})}
	home := t.TempDir()
	cacheDir := filepath.Join(home, "cache")
	search := httptest.NewServer(http.HandlerFunc(rig.searxng))
	defer search.Close()
	service := newTestService(t, Runtime{
		Home: home, CacheDir: cacheDir, SearXNGURL: search.URL, Machine: chaosAskMachine(t, home),
	})
	direct := &http.Client{Transport: chaosTransport(rig.direct)}
	h, err := harvest.New(harvest.Options{
		ContactEmail: "test@example.org", CacheDir: cacheDir, LocalRoots: []string{},
		Client: direct, Chrome: direct, Jina: direct, OA: direct, Converter: rig, BrowserRung: new(bool),
		NegativeTTL: time.Hour, NegativeTransientTTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	service.harvester, service.resolver.Client = h, direct
	items := rig.populate(rng, home, cacheDir)
	calls := serviceChaosCalls(rng, items)

	baseline := runtime.NumGoroutine()
	sessions := make([]*mcp.ClientSession, 3)
	closers := make([]func(), len(sessions))
	for index := range sessions {
		sessions[index], closers[index] = chaosConnect(t, service)
	}
	var wait sync.WaitGroup
	var cancels, every []context.CancelFunc
	defer func() {
		for _, cancel := range every {
			cancel()
		}
	}()
	start := make(chan struct{})
	for index, call := range calls {
		ctx, cancel := context.WithCancel(context.Background())
		every = append(every, cancel)
		if call.cancel {
			cancels = append(cancels, cancel)
		}
		session := sessions[index%len(sessions)]
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			call.result, call.err = session.CallTool(ctx, &mcp.CallToolParams{Name: call.tool, Arguments: call.args})
		}()
	}
	close(start)
	for _, cancel := range cancels { // the abandoning callers give up while the slow walks hang
		runtime.Gosched()
		cancel()
	}
	close(rig.slowGate)
	waitServiceChaos(t, seed, &wait)
	for _, call := range calls {
		rig.checkCall(call)
	}
	for _, closeSession := range closers {
		closeSession()
	}
	search.CloseClientConnections()
	deadline := time.Now().Add(10 * time.Second)
	for runtime.NumGoroutine() > baseline && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if now := runtime.NumGoroutine(); now > baseline {
		stacks := make([]byte, 1<<20)
		t.Errorf("seed %d: %d goroutines outlived the storm (%d before it):\n%s",
			seed, now-baseline, baseline, stacks[:runtime.Stack(stacks, true)])
	}
}

// populate draws the items: pages, partial pages, failing hosts, slow and
// busy pages, local files, a cached and a failing publication, downloads and
// search queries.
func (rig *serviceChaosRig) populate(rng *rand.Rand, home, cacheDir string) []*serviceChaosItem {
	var items []*serviceChaosItem
	files := filepath.Join(home, "files")
	if err := os.MkdirAll(files, 0o700); err != nil {
		rig.t.Fatal(err)
	}
	for _, class := range []string{"page", "partial", "fail", "slow", "busy", "file", "cited", "uncited", "download", "search"} {
		for index := range 2 + rng.IntN(2) {
			id := fmt.Sprintf("%s-%d", class, index)
			item := &serviceChaosItem{class: class, id: id, marker: "chaos-marker-" + id, field: fieldURLs}
			switch class {
			case "file":
				item.source, item.field = filepath.Join(files, id+".md"), fieldFiles
				body := "# " + id + "\n\n" + strings.Repeat(item.marker+" words of a local file. ", 40) + "\n"
				if err := os.WriteFile(item.source, []byte(body), 0o600); err != nil {
					rig.t.Fatal(err)
				}
			case "cited": // a publication its cached artifact answers
				item.source, item.field = fmt.Sprintf("10.5555/chaos.cited.%d", index), fieldPublications
				seedArtifact(rig.t, cacheDir, harvest.NormalizeIdentifier(item.source), "",
					"# "+item.marker+"\n\n"+strings.Repeat(item.marker+" cited words. ", 60))
			case "uncited": // a publication no provider can resolve
				item.source, item.field = fmt.Sprintf("10.5555/chaos.uncited.%d", index), fieldPublications
			case "download":
				item.source, item.field = "https://ok.chaos.test/"+id+"/"+id+".zip", ""
				item.body = append([]byte("PK\x03\x04"), bytes.Repeat([]byte(item.marker+" "), 20)...)
			case "search":
				item.source, item.field = item.marker, ""
			default:
				item.source = "https://ok.chaos.test/" + class + "/" + id
			}
			rig.items[id] = item
			items = append(items, item)
		}
	}
	return items
}

// serviceChaosCalls draws the storm's calls over items, each item named by
// several calls.
func serviceChaosCalls(rng *rand.Rand, items []*serviceChaosItem) []*serviceChaosCall {
	var readable, downloads, searches []*serviceChaosItem
	for _, item := range items {
		switch item.class {
		case "download":
			downloads = append(downloads, item)
		case "search":
			searches = append(searches, item)
		default:
			readable = append(readable, item)
		}
	}
	pick := func(from []*serviceChaosItem, n int) []*serviceChaosItem {
		order := rng.Perm(len(from))[:min(n, len(from))]
		out := make([]*serviceChaosItem, 0, len(order))
		for _, index := range order {
			out = append(out, from[index])
		}
		return out
	}
	var calls []*serviceChaosCall
	for range 60 {
		call := &serviceChaosCall{tool: toolRead, items: pick(readable, 1+rng.IntN(4))}
		args := map[string]any{}
		for _, item := range call.items {
			sources, _ := args[item.field].([]string)
			args[item.field] = append(sources, item.source)
		}
		// read answers urls, then files, then publications: reorder the items the same way
		slices.SortStableFunc(call.items, func(a, b *serviceChaosItem) int {
			return slices.Index([]string{fieldURLs, fieldFiles, fieldPublications}, a.field) -
				slices.Index([]string{fieldURLs, fieldFiles, fieldPublications}, b.field)
		})
		switch rng.IntN(6) {
		case 0:
			call.sizeOnly, args["include_content"] = true, false
		case 1:
			args["refresh"] = true
		case 2:
			call.ask, call.sizeOnly, args["ask"] = true, true, "Which markers do the items carry?"
		case 3:
			args["headers"] = map[string]string{"X-Chaos-Caller": "storm"} // one header set, shared by its calls
		}
		for _, item := range call.items {
			call.cancel = call.cancel || item.class == "slow" && rng.IntN(3) == 0
		}
		call.args = args
		calls = append(calls, call)
	}
	for range 12 {
		got := pick(downloads, 1+rng.IntN(2))
		urls := make([]string, 0, len(got))
		for _, item := range got {
			urls = append(urls, item.source)
		}
		calls = append(calls, &serviceChaosCall{tool: toolDownloadFile, items: got, args: map[string]any{"urls": urls}})
	}
	for range 8 {
		item := searches[rng.IntN(len(searches))]
		calls = append(
			calls,
			&serviceChaosCall{
				tool:  toolSearchWeb,
				items: []*serviceChaosItem{item},
				args:  map[string]any{"query": item.source},
			},
			&serviceChaosCall{
				tool:  toolSearchLiterature,
				items: []*serviceChaosItem{item},
				args:  map[string]any{"query": item.id},
			},
		)
	}
	rng.Shuffle(len(calls), func(i, j int) { calls[i], calls[j] = calls[j], calls[i] })
	return calls
}

// checkCall: one answer per call, and the answer its tool owes its items.
func (rig *serviceChaosRig) checkCall(call *serviceChaosCall) {
	rig.t.Helper()
	label := fmt.Sprintf("%s %v", call.tool, call.args)
	if call.err != nil {
		if !call.cancel {
			rig.errorf("%s failed at the protocol: %v", label, call.err)
		}
		return
	}
	text := allText(call.result)
	switch call.tool {
	case toolSearchWeb:
		item := call.items[0]
		if call.result.IsError || strings.Count(text, item.marker) < 2 ||
			!slices.Equal(markersIn(text), []string{item.marker}) {
			rig.errorf("%s answered isError=%v %q; want its own two results", label, call.result.IsError, text)
		}
	case toolSearchLiterature:
		if !call.result.IsError || !strings.Contains(text, "every source failed") {
			rig.errorf(
				"%s answered isError=%v %q; want the named failure of every source",
				label,
				call.result.IsError,
				text,
			)
		}
	case toolDownloadFile:
		rig.checkBlocks(call, text, func(item *serviceChaosItem, block string) bool {
			path := lineAfter(block, "path: ")
			raw, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(raw, item.body) {
				rig.errorf(
					"%s: %s stored at %q (%v) is not its own %d bytes",
					label,
					item.id,
					path,
					err,
					len(item.body),
				)
			}
			return false
		})
	case toolRead:
		read := []string{}
		rig.checkBlocks(call, text, func(item *serviceChaosItem, block string) bool {
			failed := rig.checkReadBlock(call, label, item, block)
			if !failed {
				read = append(read, item.marker)
			}
			return failed
		})
		if call.ask && len(read) > 0 {
			at := strings.Index(text, "\n=== answer")
			if at < 0 {
				rig.errorf("%s read %v and answered no ask:\n%s", label, read, text)
				return
			}
			answer := text[at+1:]
			slices.Sort(read)
			if !slices.Equal(markersIn(answer), slices.Compact(read)) {
				rig.errorf(
					"%s answer read markers %v; want exactly the call's read items %v",
					label,
					markersIn(answer),
					read,
				)
			}
		}
	}
}

// checkBlocks splits a per-item answer into its blocks and checks each is its
// own item's, in order; check returns whether the item failed. isError must
// say whether every item failed.
func (rig *serviceChaosRig) checkBlocks(
	call *serviceChaosCall, text string, check func(*serviceChaosItem, string) bool,
) {
	rig.t.Helper()
	if answer := strings.Index(text, "\n\n=== answer"); answer >= 0 {
		text = text[:answer]
	}
	headers := chaosHeaderRE.FindAllStringSubmatchIndex(text, -1)
	if len(headers) != len(call.items) {
		rig.errorf(
			"%s %v answered %d item blocks for %d items:\n%s",
			call.tool,
			call.args,
			len(headers),
			len(call.items),
			text,
		)
		return
	}
	allFailed := true
	for index, at := range headers {
		end := len(text)
		if index+1 < len(headers) {
			end = headers[index+1][0]
		}
		item, block := call.items[index], text[at[0]:end]
		if got := text[at[6]:at[7]]; !strings.Contains(got, item.id) && !strings.Contains(got, item.source) {
			rig.errorf("%s block %d is headed %q; want item %s", call.tool, index+1, got, item.id)
			continue
		}
		failed := strings.Contains(block, "\nerror: ")
		if failed && strings.TrimSpace(lineAfter(block, "error: ")) == "" {
			rig.errorf("%s block for %s is an empty error line:\n%s", call.tool, item.id, block)
		}
		itemFailed := check(item, block) || failed
		allFailed = allFailed && itemFailed
	}
	if call.tool == toolRead && !call.ask && call.result.IsError != allFailed {
		rig.errorf(
			"%s %v isError=%v, but every item failed=%v:\n%s",
			call.tool,
			call.args,
			call.result.IsError,
			allFailed,
			text,
		)
	}
}

// checkReadBlock checks one read item's block and reports whether it failed:
// a source that can only fail fails; one that can be read carries its own
// content (or its size and artifact) and nothing of another item's.
func (rig *serviceChaosRig) checkReadBlock(
	call *serviceChaosCall,
	label string,
	item *serviceChaosItem,
	block string,
) bool {
	rig.t.Helper()
	failed := strings.Contains(block, "\nerror: ")
	switch {
	case item.class == "fail" || item.class == "uncited" || item.class == "busy":
		if !failed {
			rig.errorf("%s: %s can only fail, answered:\n%s", label, item.id, block)
		}
		return true
	case failed && call.cancel:
		return true
	case failed && item.class == "cited" && (call.args["refresh"] == true || call.args["headers"] != nil):
		return true // a refresh, or a header set's own cache, reads past the cached artifact; no provider resolves the work
	case failed:
		rig.errorf("%s: %s failed:\n%s", label, item.id, block)
		return true
	}
	content := block
	if call.sizeOnly {
		path := ""
		if lines := strings.SplitN(block, "\n", 3); len(lines) > 1 {
			path = lines[1]
		}
		raw, err := os.ReadFile(path)
		if err != nil || !strings.Contains(block, "\nsize: ") {
			rig.errorf("%s: size-only %s names artifact %q (%v):\n%s", label, item.id, path, err, block)
			return false
		}
		content = string(raw)
	}
	if got := markersIn(content); !slices.Equal(got, []string{item.marker}) {
		rig.errorf("%s: %s block carries markers %v; want only its own:\n%.400s", label, item.id, got, block)
	}
	if item.class == "partial" && !strings.Contains(block, "\npartial: ") {
		rig.errorf("%s: partial %s has no partial: line:\n%.400s", label, item.id, block)
	}
	return false
}

func markersIn(text string) []string {
	return slices.Compact(slices.Sorted(slices.Values(chaosMarkerRE.FindAllString(text, -1))))
}

func lineAfter(block, prefix string) string {
	for _, line := range strings.Split(block, "\n") {
		if rest, ok := strings.CutPrefix(line, prefix); ok {
			return rest
		}
	}
	return ""
}

// chaosAskMachine is a pfm config whose Claude is a fake engine: it reads the
// artifacts its prompt lists and answers the markers they carry.
func chaosAskMachine(t *testing.T, home string) *config.Config {
	t.Helper()
	account := filepath.Join(home, "account")
	if err := os.MkdirAll(account, 0o700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(home, "claude-fixture")
	script := "#!/bin/sh\nmarkers=$(sed -n 's/^[0-9][0-9]*\\. \\(\\/[^ ]*\\) .*/\\1/p' | while read -r f; do cat \"$f\"; done |\n" +
		"  grep -o 'chaos-marker-[a-z]*-[0-9]*' | sort -u | tr '\\n' ' ')\n" +
		"printf '{\"result\":\"%s\"}\\n' \"$markers\"\n"
	if err := testjail.WriteExecutable(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return &config.Config{
		Claude:   config.Claude{Binary: binary},
		Accounts: []config.Account{{ID: 1, ConfigDir: account}},
	}
}

// chaosConnect opens one in-memory MCP session on service and returns its
// closer, which the storm calls before it counts goroutines.
func chaosConnect(t *testing.T, service *Service) (*mcp.ClientSession, func()) {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := service.Server().Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "chaos", Version: "test"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	return session, func() {
		if err := session.Close(); err != nil {
			t.Errorf("close the client session: %v", err)
		}
		if err := serverSession.Wait(); err != nil && !errors.Is(err, context.Canceled) {
			t.Logf("server session ended: %v", err)
		}
	}
}

func waitServiceChaos(t *testing.T, seed uint64, wait *sync.WaitGroup) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		wait.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(90 * time.Second):
		stacks := make([]byte, 1<<20)
		t.Fatalf("seed %d: the storm never answered every call:\n%s", seed, stacks[:runtime.Stack(stacks, true)])
	}
}
