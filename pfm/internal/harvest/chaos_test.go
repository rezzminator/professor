package harvest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// chaosSeed replays one chaos workload: `go test -run '^TestHarvesterChaos$'
// ./internal/harvest/ -args -chaos.seed=N`. 0 draws a fresh seed, which every
// failure prints. The seed fixes the workload — the items, the callers, their
// kinds and cancels — never the scheduler's interleaving.
var chaosSeed = flag.Uint64("chaos.seed", 0, "chaos suite workload seed (0 = time-based, printed on failure)")

// chaosMarker matches every item's unique marker in any answer.
var chaosMarker = regexp.MustCompile(`chaos-marker-[a-z]+-\d+`)

// chaosItem is one source of the chaos workload: class names how it behaves,
// marker is the text only its own answer may carry.
type chaosItem struct {
	class, id, source, marker string
	body                      []byte // a download's bytes
}

// chaosClasses: every kind of source a harvester read meets at once. A
// fixed class's walks are counted (one per key for the whole run); slow and
// busy walks may repeat (an abandoned or load-refused walk is walked again),
// one at a time.
var chaosClasses = []string{
	"page", "partial", "moved", "fail", "denied", "slow", "busy", "fresh", "file-md", "file-html", "download",
	"doi", "search", "scoped", "scopedfail",
}

// chaosHeaders is the one caller header set the scoped callers send.
var chaosHeaders = map[string]string{"X-Chaos-Caller": "storm"}

// chaosWords is a page body of n words, opening with marker.
func chaosWords(marker string, n int) string {
	words := make([]string, 0, n+1)
	words = append(words, marker)
	for index := range n {
		words = append(words, fmt.Sprintf("word%dof%s", index, strings.TrimPrefix(marker, "chaos-marker-")))
	}
	return strings.Join(words, " ")
}

func chaosPage(marker string, words int) string {
	return "<html><head><title>" + marker + "</title></head><body><article><p>" +
		chaosWords(marker, words) + "</p></article></body></html>"
}

var chaosTag = regexp.MustCompile(`<[^>]+>`)

// chaosRig is the fixture world: every transport slot, the converter and the
// counters the invariants read.
type chaosRig struct {
	t         *testing.T
	seed      uint64
	items     map[string]*chaosItem // by id
	slowGate  chan struct{}         // closed: slow pages answer
	busy      atomic.Bool           // the converter refuses busy pages for load
	mu        sync.Mutex
	walks     map[string]int // direct-rung requests per item id
	active    map[string]int // direct-rung requests in flight per item id
	overlaps  []string       // an id walked twice at once
	idPattern *regexp.Regexp
}

func (rig *chaosRig) errorf(format string, args ...any) {
	rig.t.Helper()
	rig.t.Errorf("seed %d: "+format, append([]any{rig.seed}, args...)...)
}

// direct is the direct rung's transport: the only slot that serves pages,
// counting each item's walks and the walks of one item in flight together.
func (rig *chaosRig) direct(request *http.Request) (*http.Response, error) {
	if request.URL.Host == "searx.chaos.test" {
		return rig.search(request)
	}
	id := rig.idPattern.FindString(request.URL.Path)
	item := rig.items[id]
	own := item != nil && (request.URL.Host == "ok.chaos.test" || request.URL.Host == "fail.chaos.test")
	first := own && !strings.HasPrefix(request.URL.Path, "/landing/")
	if first { // a walk of the item starts at its own URL
		rig.mu.Lock()
		rig.walks[id]++
		rig.active[id]++
		if rig.active[id] > 1 {
			rig.overlaps = append(rig.overlaps, id)
		}
		rig.mu.Unlock()
		defer func() {
			rig.mu.Lock()
			rig.active[id]--
			rig.mu.Unlock()
		}()
	}
	if !own || request.URL.Host == "fail.chaos.test" { // a reader service, an archive, a failing host
		return nil, errors.New("fixture connection failure")
	}
	switch item.class {
	case "slow":
		select {
		case <-rig.slowGate:
		case <-request.Context().Done():
			return nil, request.Context().Err()
		}
	case "moved":
		if first {
			moved := response(request, http.StatusMovedPermanently, "text/html", "")
			moved.Header.Set("Location", "/landing/"+id)
			return moved, nil
		}
	case "denied":
		return response(request, http.StatusForbidden, "text/html", "<html><body>access denied</body></html>"), nil
	case "download":
		served := response(request, http.StatusOK, "application/zip", string(item.body))
		served.ContentLength = int64(len(item.body))
		return served, nil
	}
	words := 200
	if item.class == "partial" {
		words = 420 // the converter keeps a sliver: the recall gate flags it
	}
	return response(request, http.StatusOK, "text/html", chaosPage(item.marker, words)), nil
}

// search answers a SearXNG query with three results naming the query's item.
func (rig *chaosRig) search(request *http.Request) (*http.Response, error) {
	query := request.URL.Query().Get("q")
	item := rig.items[rig.idPattern.FindString(query)]
	if item == nil {
		return response(request, http.StatusBadGateway, "application/json", `{"error":"unknown query"}`), nil
	}
	var results []map[string]any
	for index := range 3 {
		results = append(results, map[string]any{
			"title": item.marker, "url": fmt.Sprintf("https://result.chaos.test/%s/%d", item.id, index),
			"content": item.marker, "engine": "fixture",
		})
	}
	body, err := json.Marshal(map[string]any{"results": results})
	if err != nil {
		return nil, err
	}
	return response(request, http.StatusOK, "application/json", string(body)), nil
}

// Convert keeps the page's text, a sliver of a partial page, and refuses a
// busy page for load while the rig is busy.
func (rig *chaosRig) Convert(ctx context.Context, _, _ string, body []byte) (string, error) {
	if bytes.Contains(body, []byte("chaos-marker-busy-")) && rig.busy.Load() {
		NoteRefusedForLoad(ctx)
		return "", errors.New("fixture converter refused: every worker is busy")
	}
	text := strings.Join(strings.Fields(chaosTag.ReplaceAllString(string(body), " ")), " ")
	if bytes.Contains(body, []byte("chaos-marker-partial-")) {
		text = strings.Join(strings.Fields(text)[:60], " ")
	}
	return "# Chaos page\n\n" + text, nil
}

// chaosCall is one caller of the storm and the answer it got.
type chaosCall struct {
	item     *chaosItem
	mode     string // fetch, size, public, refresh, download, search
	cancel   bool
	result   Result
	found    []SearchResult
	foundErr error
}

// TestHarvesterChaos fires hundreds of callers of every kind at one
// Harvester at once — page reads (whole, size-only and public), failures,
// a 403, a redirect, a partial page, a slow page whose callers cancel
// mid-walk, a page the converter refuses for load, refreshes, local files,
// downloads, DOI reads in two spellings of one key and web searches, every
// key shared by many callers — over fixture transports, then checks the
// invariants, never the timing: each answer is its own item's; an error never reads
// as absence; no content crosses items; the negative cache holds no
// abandoned or load-refused failure; one walk per key in flight (and one in
// all for a source whose outcome caches); every cache file parses; no
// goroutine outlives the storm. Serial: it counts the process's goroutines.
func TestHarvesterChaos(t *testing.T) {
	seed := *chaosSeed
	if seed == 0 {
		seed = uint64(time.Now().UnixNano())
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf(
				"reproduce the workload: go test -run '^TestHarvesterChaos$' ./internal/harvest/ -args -chaos.seed=%d",
				seed,
			)
		}
	})
	rng := rand.New(rand.NewPCG(seed, 0x6368616f73))
	baseline := runtime.NumGoroutine()
	rig := &chaosRig{
		t: t, seed: seed, items: map[string]*chaosItem{}, slowGate: make(chan struct{}),
		walks: map[string]int{}, active: map[string]int{},
		idPattern: regexp.MustCompile(`[a-z]+-\d+`),
	}
	rig.busy.Store(true)
	files := t.TempDir()
	var items []*chaosItem
	for _, class := range chaosClasses {
		for index := range 2 + rng.IntN(3) {
			id := fmt.Sprintf("%s-%d", strings.ReplaceAll(class, "-", ""), index)
			item := &chaosItem{class: class, id: id, marker: "chaos-marker-" + id}
			switch class {
			case "fail", "scopedfail":
				item.source = "https://fail.chaos.test/" + id
			case "file-md":
				item.source = filepath.Join(files, id+".md")
				writeChaosFile(t, item.source, "# "+id+"\n\n"+chaosWords(item.marker, 200)+"\n")
			case "file-html":
				item.source = filepath.Join(files, id+".html")
				writeChaosFile(t, item.source, chaosPage(item.marker, 200))
			case "download":
				item.source = "https://ok.chaos.test/" + id + "/" + id + ".zip"
				item.body = append([]byte("PK\x03\x04"), []byte(chaosWords(item.marker, 40))...)
			case "doi":
				item.source = "10.5555/" + id
			default:
				item.source = "https://ok.chaos.test/" + class + "/" + id
			}
			rig.items[id] = item
			items = append(items, item)
		}
	}
	direct := &http.Client{Transport: roundTripFunc(rig.direct)}
	elsewhere := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("fixture: %s is unreachable", request.URL.Host)
	})}
	h := mustNew(t, Options{
		ContactEmail: "test@example.org", CacheDir: t.TempDir(),
		Client: direct, Chrome: elsewhere, Jina: elsewhere, OA: elsewhere,
		Converter: rig, BrowserRung: browserOff(),
		// one walk per failing key holds only while its failure is cached: a
		// slow runner never outlives it
		NegativeTTL: time.Hour, NegativeTransientTTL: time.Hour,
	})

	calls := chaosStormCalls(rng, items)
	var wait sync.WaitGroup
	contexts := make([]context.CancelFunc, 0, len(calls))
	every := make([]context.CancelFunc, 0, len(calls))
	defer func() {
		for _, cancel := range every {
			cancel()
		}
	}()
	start := make(chan struct{})
	for _, call := range calls {
		ctx, cancel := context.WithCancel(context.Background())
		every = append(every, cancel)
		if call.cancel {
			contexts = append(contexts, cancel)
		}
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			call.run(ctx, h)
		}()
	}
	close(start)
	for index, cancel := range contexts { // the cancelling callers give up while the slow walks hang
		if index%2 == 0 {
			runtime.Gosched()
		}
		cancel()
	}
	close(rig.slowGate)
	waitChaos(t, seed, &wait, "the storm")

	for _, call := range calls {
		rig.checkAnswer(call)
	}
	rig.checkWalks(calls)
	rig.checkNegativeCache(h, calls)
	rig.busy.Store(false)
	for _, item := range items {
		if item.class == "busy" { // a load refusal was never the source's: the next read walks and reads it
			if after := h.Fetch(context.Background(), item.source); after.Error != "" ||
				!strings.Contains(after.Content, item.marker) {
				rig.errorf("%s read after the load refusal answered %q; want its page", item.id, after.Error)
			}
		}
	}
	rig.checkCacheFiles(h.options.CacheDir)
	checkNoGoroutineLeak(t, seed, baseline)
}

func writeChaosFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// chaosStormCalls draws the storm: every item gets callers, a shared key many.
func chaosStormCalls(rng *rand.Rand, items []*chaosItem) []*chaosCall {
	var calls []*chaosCall
	for _, item := range items {
		for range 3 + rng.IntN(10) {
			call := &chaosCall{item: item, mode: []string{"fetch", "size", "public"}[rng.IntN(3)]}
			switch item.class {
			case "download":
				call.mode = "download"
			case "search":
				call.mode = "search"
			case "fresh":
				call.mode = []string{"fetch", "refresh"}[rng.IntN(2)]
			case "doi":
				if rng.IntN(2) == 0 {
					call.mode, call.item = "doi-url", item // the doi.org spelling of the same key
				}
			case "slow":
				call.cancel = rng.IntN(3) == 0
			case "scoped", "scopedfail":
				call.mode = "scoped" // a read with caller headers, whose caches are its header set's
			}
			calls = append(calls, call)
		}
	}
	rng.Shuffle(len(calls), func(i, j int) { calls[i], calls[j] = calls[j], calls[i] })
	return calls
}

func (call *chaosCall) run(ctx context.Context, h *Harvester) {
	source := call.item.source
	switch call.mode {
	case "fetch":
		call.result = h.Fetch(ctx, source)
	case "size":
		call.result = h.FetchWithOptions(ctx, source, FetchOptions{SizeOnly: true})
	case "public":
		call.result = h.FetchPublic(ctx, source, FetchOptions{})
	case "refresh":
		call.result = h.FetchWithOptions(ctx, source, FetchOptions{Refresh: true})
	case "doi-url":
		call.result = h.Fetch(ctx, "https://doi.org/"+source)
	case "scoped":
		headers, err := ParseCallerHeaders(chaosHeaders)
		if err != nil {
			call.result = Result{Source: source, Error: err.Error()}
			return
		}
		scoped, scopedCtx, err := h.ForCaller(ctx, headers, source)
		if err != nil {
			call.result = Result{Source: source, Error: err.Error()}
			return
		}
		call.result = scoped.FetchPublic(scopedCtx, source, FetchOptions{})
	case "download":
		call.result = h.Download(ctx, source)
	case "search":
		call.found, _, call.foundErr = h.Search(ctx, call.item.marker, SearchOptions{
			SearXNGURL: "https://searx.chaos.test", SearXNG: h.client, Count: 8,
		})
	}
}

// checkAnswer: the caller's answer is of its own item, never an empty
// success and never a success where its item can only fail.
func (rig *chaosRig) checkAnswer(call *chaosCall) {
	rig.t.Helper()
	item, result := call.item, call.result
	if call.mode == "search" {
		if call.foundErr != nil || len(call.found) != 3 {
			rig.errorf("search %s = %d results, %v; want its 3", item.id, len(call.found), call.foundErr)
		}
		for index := range call.found {
			if found := &call.found[index]; found.Title != item.marker ||
				!strings.Contains(found.URL, "/"+item.id+"/") {
				rig.errorf("search %s answered another query's result %+v", item.id, *found)
			}
		}
		return
	}
	wantsFailure := item.class == "fail" || item.class == "denied" || item.class == "doi" || item.class == "busy" ||
		item.class == "scopedfail"
	switch {
	case result.Error != "" && wantsFailure:
		return
	case result.Error != "" && call.cancel:
		return // a caller that gave up answers its own cancel
	case result.Error != "":
		rig.errorf("%s %s caller failed: %q", item.id, call.mode, result.Error)
		return
	case wantsFailure:
		rig.errorf("%s %s caller answered success %+v for a source that can only fail", item.id, call.mode, result)
		return
	}
	content := result.Content
	if call.mode == "download" || call.mode == "public" || call.mode == "size" || call.mode == "scoped" {
		if result.Path == "" {
			rig.errorf("%s %s success names no artifact path: %+v", item.id, call.mode, result)
			return
		}
		raw, err := os.ReadFile(result.Path)
		if err != nil {
			rig.errorf("%s %s artifact %s: %v", item.id, call.mode, result.Path, err)
			return
		}
		if call.mode == "download" {
			if !bytes.Equal(raw, item.body) {
				rig.errorf("download %s stored %d bytes that are not its own", item.id, len(raw))
			}
			return
		}
		if call.mode == "size" && (content != "" || result.Tokens == 0) {
			rig.errorf("size-only %s answered content=%d chars tokens=%d", item.id, len(content), result.Tokens)
		}
		content = string(raw)
	}
	markers := slices.Compact(slices.Sorted(slices.Values(chaosMarker.FindAllString(content, -1))))
	if !slices.Equal(markers, []string{item.marker}) {
		rig.errorf("%s %s answer carries markers %v; want only its own %s", item.id, call.mode, markers, item.marker)
	}
	if item.class == "partial" && call.mode != "public" && result.Partial == "" {
		rig.errorf("partial %s %s answered no partial reason", item.id, call.mode)
	}
}

// checkWalks: one walk of a key at a time, and one in all for a source whose
// outcome — a page or a failure the source owns — the caches keep.
func (rig *chaosRig) checkWalks(calls []*chaosCall) {
	rig.t.Helper()
	refreshed := map[string]bool{} // a refresh walks on purpose, beside any walk in flight
	for _, call := range calls {
		if call.mode == "refresh" {
			refreshed[call.item.id] = true
		}
	}
	for _, id := range rig.overlaps { // a download is never shared: download_file always fetches fresh bytes
		if !refreshed[id] && rig.items[id].class != "download" {
			rig.errorf("%s was walked twice at once", id)
		}
	}
	for id, item := range rig.items {
		switch item.class {
		case "page", "partial", "moved", "fail", "denied", "scoped", "scopedfail":
		case "fresh":
			if refreshed[id] {
				continue
			}
		default:
			continue
		}
		if rig.walks[id] != 1 {
			rig.errorf("%s (%s) was walked %d times; want one walk shared by every caller: %v",
				id, item.class, rig.walks[id], rig.answers(id, calls))
		}
	}
}

// answers lists each caller's mode and its answer's rungs and error, for a
// walk count's failure message.
func (rig *chaosRig) answers(id string, calls []*chaosCall) []string {
	var out []string
	for _, call := range calls {
		if call.item.id == id {
			out = append(out, fmt.Sprintf("%s rungs=%v status=%s error=%.80q",
				call.mode, call.result.Rungs, call.result.CacheStatus, call.result.Error))
		}
	}
	return out
}

// checkNegativeCache: every entry, in h's cache and each header set's, is a
// failure the source owns; a source that only failed for a cancel or a load
// refusal has none.
func (rig *chaosRig) checkNegativeCache(h *Harvester, calls []*chaosCall) {
	rig.t.Helper()
	h.scopeMu.Lock()
	caches := []*negativeCache{h.neg}
	for _, scoped := range h.scopes {
		caches = append(caches, scoped.neg)
	}
	h.scopeMu.Unlock()
	for _, cache := range caches {
		cache.mu.Lock()
		for key := range cache.entries {
			failure := cache.entries[key].result.Error
			id := rig.idPattern.FindString(strings.TrimPrefix(key, "fetch:"))
			item := rig.items[id]
			switch {
			case item == nil:
				rig.errorf("negative cache holds %q, no item of the storm", key)
			case item.class == "slow" || item.class == "busy":
				rig.errorf("negative cache holds %s's %s failure %q", item.class, id, failure)
			case failure == "":
				rig.errorf("negative cache holds a success for %s", id)
			}
		}
		cache.mu.Unlock()
	}
	h.neg.mu.Lock()
	defer h.neg.mu.Unlock()
	for _, call := range calls {
		if c := call.item.class; c == "fail" || c == "denied" {
			if _, ok := h.neg.entries[canonicalNegativeKey("fetch", call.item.source)]; !ok {
				rig.errorf("%s's own failure is not in the negative cache", call.item.id)
			}
		}
	}
}

// checkCacheFiles: no temp file outlived its write, every Markdown artifact
// opens with frontmatter that parses, and every stats line is JSON.
func (rig *chaosRig) checkCacheFiles(root string) {
	rig.t.Helper()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".harvest-") || strings.HasPrefix(name, ".public-") {
			rig.errorf("temp file outlived its write: %s", path)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		switch {
		case name == statsFilename:
			for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
				if !json.Valid([]byte(line)) {
					rig.errorf("stats line is not JSON: %q", line)
				}
			}
		case strings.HasSuffix(name, ".md"):
			if meta, _ := readFrontmatter(string(raw)); len(meta) == 0 {
				rig.errorf("artifact %s has no frontmatter that parses: %.120q", path, raw)
			}
		}
		return nil
	})
	if err != nil {
		rig.errorf("walk the cache: %v", err)
	}
}

// waitChaos waits for the storm's callers; a caller that never answers fails
// the run with every goroutine's stack.
func waitChaos(t *testing.T, seed uint64, wait *sync.WaitGroup, what string) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		wait.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		stacks := make([]byte, 1<<20)
		t.Fatalf("seed %d: %s never answered every caller:\n%s", seed, what, stacks[:runtime.Stack(stacks, true)])
	}
}

// checkNoGoroutineLeak: the process returns to its goroutine count from
// before the storm; a count still above it after 10 s fails with the stacks.
func checkNoGoroutineLeak(t *testing.T, seed uint64, baseline int) {
	t.Helper()
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
