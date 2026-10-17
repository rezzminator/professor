package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

// TestIndicatorContractIsFrozen pins the names the sidebar rows, the plugin
// manifest and the painter share.
func TestIndicatorContractIsFrozen(t *testing.T) {
	want := []string{"pfm_working", "pfm_blocked", "pfm_done", "pfm_unknown"}
	if got := IndicatorTokens(); !slices.Equal(got, want) {
		t.Fatalf("IndicatorTokens() = %q, want %q", got, want)
	}
	for _, glyph := range []string{GlyphWorking, GlyphBlocked, GlyphDone, GlyphUnknown} {
		if strings.TrimSpace(glyph) == "" {
			t.Fatalf("an indicator glyph is blank: %q", glyph)
		}
		// Herdr joins row tokens with " · " (src/ui/sidebar/tokens.rs:180): a
		// glyph holding that dot reads as a second separator, "name · ·".
		if strings.Contains(glyph, "·") {
			t.Fatalf("indicator glyph %q holds Herdr's token separator dot", glyph)
		}
	}
	if GlyphDone == GlyphWorking || GlyphUnknown == GlyphWorking || GlyphDone == GlyphUnknown {
		t.Fatalf("done %q, unknown %q and working %q must differ in shape", GlyphDone, GlyphUnknown, GlyphWorking)
	}
	if IndicatorPluginID != "professor.indicators" || IndicatorSource != "pfm-indicators" ||
		IndicatorVerb != "herdr-indicators" {
		t.Fatalf("plugin id %q, source %q, verb %q", IndicatorPluginID, IndicatorSource, IndicatorVerb)
	}
	if strings.HasPrefix(IndicatorSource, "herdr:") {
		t.Fatalf("source %q takes Herdr's reserved prefix", IndicatorSource)
	}
	if !slices.Contains(IndicatorEvents(), "pane.agent_status_changed") {
		t.Fatalf("IndicatorEvents() = %q lacks pane.agent_status_changed", IndicatorEvents())
	}
}

// fakeTarget is a pane or a workspace of the fake Herdr.
type fakeTarget struct {
	ID        string
	Workspace string
	Status    string
	Tokens    map[string]string
}

// fakeWrite is one report_metadata request the fake Herdr accepted or refused.
type fakeWrite struct {
	Method string
	Target string
	Tokens string
	Line   string
}

// fakeIndicatorHerdr answers the painter's calls from an in-memory Herdr: lists
// built from its targets, token patches applied to them as Herdr applies them.
type fakeIndicatorHerdr struct {
	panes  []*fakeTarget
	spaces []*fakeTarget
	lists  int
	writes []fakeWrite
	// onList runs at the start of the n-th pane.list (1-based); failList makes
	// it fail; refuse makes a report on that target id a Refusal.
	onList   func(listNo int)
	failList func(listNo int) error
	refuse   map[string]bool
	failTo   map[string]error
}

func (herdr *fakeIndicatorHerdr) call(_ context.Context, socket string, lines ...[]byte) ([]json.RawMessage, error) {
	results := make([]json.RawMessage, 0, len(lines))
	for _, line := range lines {
		var request struct {
			Method string `json:"method"`
			Params struct {
				PaneID      string             `json:"pane_id"`
				WorkspaceID string             `json:"workspace_id"`
				Source      string             `json:"source"`
				Tokens      map[string]*string `json:"tokens"`
			} `json:"params"`
		}
		if err := json.Unmarshal(line, &request); err != nil {
			return nil, fmt.Errorf("fake herdr: bad request %q: %w", line, err)
		}
		switch request.Method {
		case "pane.list":
			herdr.lists++
			if herdr.onList != nil {
				herdr.onList(herdr.lists)
			}
			if herdr.failList != nil {
				if err := herdr.failList(herdr.lists); err != nil {
					return nil, fmt.Errorf("herdr socket %s: dial for pane.list: %w", socket, err)
				}
			}
			results = append(results, herdr.listResult("pane_list", "panes", "pane_id", herdr.panes))
		case "workspace.list":
			results = append(results, herdr.listResult("workspace_list", "workspaces", "workspace_id", herdr.spaces))
		case "pane.report_metadata", "workspace.report_metadata":
			id, targets := request.Params.PaneID, herdr.panes
			if request.Method == "workspace.report_metadata" {
				id, targets = request.Params.WorkspaceID, herdr.spaces
			}
			tokens, _ := json.Marshal(request.Params.Tokens)
			herdr.writes = append(herdr.writes, fakeWrite{request.Method, id, string(tokens), string(line)})
			if err := herdr.failTo[id]; err != nil {
				return nil, err
			}
			if herdr.refuse[id] {
				return nil, &Refusal{Request: request.Method, Code: "target_not_found", Message: "gone"}
			}
			if request.Params.Source != IndicatorSource {
				return nil, fmt.Errorf("fake herdr: source %q, want %q", request.Params.Source, IndicatorSource)
			}
			for _, target := range targets {
				if target.ID != id {
					continue
				}
				for key, value := range request.Params.Tokens {
					if value == nil {
						delete(target.Tokens, key)
						continue
					}
					if target.Tokens == nil {
						target.Tokens = map[string]string{}
					}
					target.Tokens[key] = *value
				}
			}
			results = append(results, json.RawMessage(`{"type":"ok"}`))
		default:
			return nil, fmt.Errorf("fake herdr: unexpected method %q", request.Method)
		}
	}
	return results, nil
}

func (herdr *fakeIndicatorHerdr) listResult(kind, key, idKey string, targets []*fakeTarget) json.RawMessage {
	entries := make([]map[string]any, 0, len(targets))
	for _, target := range targets {
		entry := map[string]any{idKey: target.ID, "agent_status": target.Status}
		if target.Workspace != "" {
			entry["workspace_id"] = target.Workspace
		}
		if len(target.Tokens) > 0 {
			entry["tokens"] = target.Tokens
		}
		entries = append(entries, entry)
	}
	result, _ := json.Marshal(map[string]any{"type": kind, key: entries})
	return result
}

// setStatus sets every pane and workspace to status.
func (herdr *fakeIndicatorHerdr) setStatus(status string) {
	for _, target := range append(slices.Clone(herdr.panes), herdr.spaces...) {
		target.Status = status
	}
}

// paneWrites lists the token patches written to the pane id, in order.
func (herdr *fakeIndicatorHerdr) paneWrites(id string) []string {
	var tokens []string
	for _, write := range herdr.writes {
		if write.Method == "pane.report_metadata" && write.Target == id {
			tokens = append(tokens, write.Tokens)
		}
	}
	return tokens
}

// steppingClock is a clock whose Sleep moves its time on by the slept duration
// at once, so a blink run never waits.
type steppingClock struct {
	clock.Clock
	now     time.Time
	sleeps  []time.Duration
	onSleep func(ctx context.Context) error
}

func (stepping *steppingClock) Now() time.Time { return stepping.now }

func (stepping *steppingClock) Sleep(ctx context.Context, d time.Duration) error {
	stepping.sleeps = append(stepping.sleeps, d)
	if stepping.onSleep != nil {
		if err := stepping.onSleep(ctx); err != nil {
			return err
		}
	}
	stepping.now = stepping.now.Add(d)
	return nil
}

// phaseOn is a time 100 ms into an even half-second: the blink's on-phase.
var phaseOn = time.UnixMilli(1_000_000_100)

func newTestPainter(
	t *testing.T,
	herdr *fakeIndicatorHerdr,
	at time.Time,
) (*indicatorPainter, *steppingClock, *bytes.Buffer) {
	t.Helper()
	stepping := &steppingClock{now: at}
	stderr := &bytes.Buffer{}
	return &indicatorPainter{
		socket: "/fake/herdr.sock", lockDir: filepath.Join(t.TempDir(), "locks"), stderr: stderr,
		call: herdr.call, clock: stepping, reported: map[string]bool{},
	}, stepping, stderr
}

func blockedHerdr() *fakeIndicatorHerdr {
	return &fakeIndicatorHerdr{
		panes:  []*fakeTarget{{ID: "w1:p1", Workspace: "w1", Status: "blocked"}},
		spaces: []*fakeTarget{{ID: "w1", Status: "blocked"}},
	}
}

func TestBlinkFlipsEveryHalfSecondAndStopsWhenNothingIsBlocked(t *testing.T) {
	herdr := blockedHerdr()
	herdr.onList = func(listNo int) {
		if listNo >= 4 {
			herdr.setStatus("idle")
		}
	}
	painter, stepping, stderr := newTestPainter(t, herdr, phaseOn)
	if err := painter.run(context.Background()); err != nil {
		t.Fatalf("run = %v, want nil; stderr %q", err, stderr)
	}
	wantPane := []string{`{"pfm_blocked":"●"}`, `{"pfm_blocked":null}`, `{"pfm_blocked":"●"}`, `{"pfm_blocked":null}`}
	if got := herdr.paneWrites("w1:p1"); !slices.Equal(got, wantPane) {
		t.Fatalf("pane writes = %q, want on, off, on, off %q", got, wantPane)
	}
	if len(herdr.writes) != 8 {
		t.Fatalf("writes = %d, want a pane and a workspace write per tick (8): %+v", len(herdr.writes), herdr.writes)
	}
	wantSleeps := []time.Duration{400 * time.Millisecond, 500 * time.Millisecond, 500 * time.Millisecond}
	if !slices.Equal(stepping.sleeps, wantSleeps) {
		t.Fatalf(
			"sleeps = %v, want %v: the first to the next 500 ms boundary, then one half-second each",
			stepping.sleeps,
			wantSleeps,
		)
	}
	if herdr.lists != 5 {
		t.Fatalf("lists = %d, want 5: the first paint, three ticks and the re-list after the unlock", herdr.lists)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestSecondCallerReturnsAtOnceWhileAnotherHoldsTheBlinkerLock(t *testing.T) {
	herdr := blockedHerdr()
	painter, stepping, _ := newTestPainter(t, herdr, phaseOn)
	holder, held, err := painter.takeBlinkerLock()
	if err != nil || !held {
		t.Fatalf("first takeBlinkerLock = held %v, %v, want the lock", held, err)
	}
	defer holder()
	if err := painter.run(context.Background()); err != nil {
		t.Fatalf("run beside a holder = %v, want nil", err)
	}
	if herdr.lists != 1 || len(stepping.sleeps) != 0 {
		t.Fatalf("lists %d, sleeps %v: a loser paints once and returns without blinking", herdr.lists, stepping.sleeps)
	}
	if got := herdr.paneWrites("w1:p1"); !slices.Equal(got, []string{`{"pfm_blocked":"●"}`}) {
		t.Fatalf("the loser's one paint wrote %q, want the on-phase token", got)
	}
}

func TestBlinkerLocksArePerSocket(t *testing.T) {
	herdr := blockedHerdr()
	painter, _, _ := newTestPainter(t, herdr, phaseOn)
	release, held, err := painter.takeBlinkerLock()
	if err != nil || !held {
		t.Fatalf("takeBlinkerLock = held %v, %v", held, err)
	}
	defer release()
	other := *painter
	other.socket = "/fake/other.sock"
	releaseOther, held, err := other.takeBlinkerLock()
	if err != nil || !held {
		t.Fatalf("a second socket's lock = held %v, %v, want its own lock", held, err)
	}
	releaseOther()
	names, err := filepath.Glob(filepath.Join(painter.lockDir, "indicators-????????.lock"))
	if err != nil || len(names) != 2 {
		t.Fatalf("lock files = %v, %v, want one indicators-{8 hex}.lock per socket", names, err)
	}
	info, err := os.Stat(painter.lockDir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("lock dir %v, %v, want mode 0700", info, err)
	}
}

func TestLockIsReleasedAndTakenAgainWhenBlockedAppearsAfterTheUnlock(t *testing.T) {
	herdr := blockedHerdr()
	blockedAt := []bool{true, true, false, true, false, false}
	herdr.onList = func(listNo int) {
		status := "idle"
		if blockedAt[listNo-1] {
			status = "blocked"
		}
		herdr.setStatus(status)
	}
	painter, stepping, _ := newTestPainter(t, herdr, phaseOn)
	if err := painter.run(context.Background()); err != nil {
		t.Fatalf("run = %v", err)
	}
	if herdr.lists != 6 || len(stepping.sleeps) != 3 {
		t.Fatalf(
			"lists %d, sleeps %v, want 6 lists and 3 sleeps: blink, unlock, re-list, blink again, unlock, re-list",
			herdr.lists,
			stepping.sleeps,
		)
	}
}

func TestBlinkerGivesUpAfterThreeConsecutiveSocketFailures(t *testing.T) {
	herdr := blockedHerdr()
	herdr.failList = func(listNo int) error {
		if listNo > 1 {
			return errors.New("connection refused")
		}
		return nil
	}
	painter, stepping, stderr := newTestPainter(t, herdr, phaseOn)
	err := painter.run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("run = %v, want the socket error after three failed repaints", err)
	}
	if herdr.lists != 4 || len(stepping.sleeps) != 3 {
		t.Fatalf(
			"lists %d, sleeps %v, want the first paint and exactly three failed ticks",
			herdr.lists,
			stepping.sleeps,
		)
	}
	if got := strings.Count(stderr.String(), "connection refused"); got != 3 {
		t.Fatalf("stderr names the failure %d times, want once per failure (3): %q", got, stderr)
	}
	if !strings.Contains(stderr.String(), "/fake/herdr.sock") {
		t.Fatalf("stderr %q lacks the socket", stderr)
	}
}

func TestSocketFailuresNeedToBeConsecutiveToEndTheBlinker(t *testing.T) {
	herdr := blockedHerdr()
	failing := map[int]bool{2: true, 3: true, 5: true, 6: true}
	herdr.failList = func(listNo int) error {
		if failing[listNo] {
			return errors.New("connection refused")
		}
		return nil
	}
	herdr.onList = func(listNo int) {
		if listNo >= 8 {
			herdr.setStatus("idle")
		}
	}
	painter, _, _ := newTestPainter(t, herdr, phaseOn)
	if err := painter.run(context.Background()); err != nil {
		t.Fatalf("run = %v, want two failures, a good tick, two failures and a recovery to be survived", err)
	}
	if herdr.lists < 8 {
		t.Fatalf("lists = %d, want the blinker to run on past the failures", herdr.lists)
	}
}

func TestCancelledContextEndsTheBlinkerCleanly(t *testing.T) {
	herdr := blockedHerdr()
	painter, stepping, stderr := newTestPainter(t, herdr, phaseOn)
	ctx, cancel := context.WithCancel(context.Background())
	stepping.onSleep = func(ctx context.Context) error {
		cancel()
		return ctx.Err()
	}
	if err := painter.run(ctx); err != nil {
		t.Fatalf("run after cancel = %v, want nil", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want nothing for a clean cancel", stderr)
	}
	if _, held, err := painter.takeBlinkerLock(); err != nil || !held {
		t.Fatalf("lock after the blinker ended = held %v, %v, want it released", held, err)
	}
}

func TestPaintWithoutASocketNamesTheEnvironmentVariable(t *testing.T) {
	stderr := &bytes.Buffer{}
	err := PaintIndicators(context.Background(), "", t.TempDir(), stderr)
	if err == nil || !strings.Contains(err.Error(), EnvSocket) {
		t.Fatalf("PaintIndicators with no socket = %v, want an error naming %s", err, EnvSocket)
	}
	if !strings.Contains(stderr.String(), EnvSocket) {
		t.Fatalf("stderr = %q, want the error written there too", stderr)
	}
}

func TestPaintWithoutALockDirIsRefusedAtEntry(t *testing.T) {
	stderr := &bytes.Buffer{}
	err := PaintIndicators(context.Background(), "/fake/herdr.sock", "", stderr)
	if err == nil || !strings.Contains(err.Error(), "lock") || !strings.Contains(stderr.String(), "lock") {
		t.Fatalf("PaintIndicators with no lock dir = %v, stderr %q, want a lock-directory error on both", err, stderr)
	}
}

func TestPaintReportsAnUnreachableSocketWithItsContext(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "missing.sock")
	stderr := &bytes.Buffer{}
	err := PaintIndicators(context.Background(), socket, t.TempDir(), stderr)
	if err == nil {
		t.Fatal("PaintIndicators on a missing socket = nil, want the dial error")
	}
	for _, want := range []string{socket, "pane.list"} {
		if !strings.Contains(err.Error(), want) || !strings.Contains(stderr.String(), want) {
			t.Fatalf("error %v / stderr %q lack %q", err, stderr, want)
		}
	}
}

func TestPaintIndicatorsOverTheRealSocket(t *testing.T) {
	var requests []string
	panes := `{"type":"pane_list","panes":[{"pane_id":"w1:p1","workspace_id":"w1","agent_status":"working"},` +
		`{"pane_id":"w1:p2","workspace_id":"w1","agent_status":"idle","tokens":{"pfm_done":"✓","other":"keep"}}]}`
	spaces := `{"type":"workspace_list","workspaces":[{"workspace_id":"w1","label":"x","agent_status":"working"}]}`
	socket := serve(t, func(request string) string {
		requests = append(requests, request)
		switch {
		case strings.Contains(request, `"pane.list"`):
			return `{"id":"x","result":` + panes + `}`
		case strings.Contains(request, `"workspace.list"`):
			return `{"id":"x","result":` + spaces + `}`
		}
		return `{"id":"x","result":{"type":"ok"}}`
	})
	stderr := &bytes.Buffer{}
	if err := PaintIndicators(context.Background(), socket, t.TempDir(), stderr); err != nil {
		t.Fatalf("PaintIndicators = %v; stderr %q", err, stderr)
	}
	want := []string{
		`{"id":"pfm-3","method":"pane.report_metadata","params":{"pane_id":"w1:p1","source":"pfm-indicators","tokens":{"pfm_working":"●"}}}`,
		`{"id":"pfm-4","method":"pane.report_metadata","params":{"pane_id":"w1:p2","source":"pfm-indicators","tokens":{"pfm_done":null}}}`,
		`{"id":"pfm-5","method":"workspace.report_metadata","params":{"workspace_id":"w1","source":"pfm-indicators","tokens":{"pfm_working":"●"}}}`,
	}
	if got := requests[2:5]; !slices.Equal(got, want) {
		t.Fatalf("writes over the socket:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestPaintIndicatorsOnAnEmptyHerdrWritesNothing(t *testing.T) {
	var requests []string
	socket := serve(t, func(request string) string {
		requests = append(requests, request)
		if strings.Contains(request, `"pane.list"`) {
			return `{"id":"x","result":{"type":"pane_list","panes":[]}}`
		}
		return `{"id":"x","result":{"type":"workspace_list","workspaces":[]}}`
	})
	if err := PaintIndicators(context.Background(), socket, t.TempDir(), nil); err != nil {
		t.Fatalf("PaintIndicators on an empty Herdr = %v, want nil", err)
	}
	if len(requests) != 2 {
		t.Fatalf("requests = %q, want only the two lists", requests)
	}
}

func TestARunThatWroteListsAgainSoAWriteThatCrossedAStateChangeIsRepainted(t *testing.T) {
	herdr := &fakeIndicatorHerdr{panes: []*fakeTarget{{ID: "w1:p1", Workspace: "w1", Status: "working"}}}
	herdr.onList = func(listNo int) {
		if listNo == 2 {
			herdr.panes[0].Status = "idle" // the agent went idle while the first paint was writing
		}
	}
	painter, _, stderr := newTestPainter(t, herdr, phaseOn)
	if err := painter.run(context.Background()); err != nil {
		t.Fatalf("run = %v; stderr %q", err, stderr)
	}
	if got, want := herdr.paneWrites("w1:p1"), []string{`{"pfm_working":"●"}`, `{"pfm_working":null}`}; !slices.Equal(
		got,
		want,
	) {
		t.Fatalf("pane writes = %q, want the stale working token cleared by the re-list %q", got, want)
	}
	if herdr.lists != 3 {
		t.Fatalf("lists = %d, want 3: the paint, the re-list that clears, and one that writes nothing", herdr.lists)
	}
}

func TestSettleRoundsAreBoundedWhenEveryPaintWrites(t *testing.T) {
	herdr := &fakeIndicatorHerdr{panes: []*fakeTarget{{ID: "w1:p1", Workspace: "w1", Status: "working"}}}
	herdr.onList = func(int) { herdr.panes[0].Tokens = nil } // a Herdr that loses every write
	painter, _, _ := newTestPainter(t, herdr, phaseOn)
	if err := painter.run(context.Background()); err != nil {
		t.Fatalf("run = %v", err)
	}
	if herdr.lists != 1+maxSettleRounds {
		t.Fatalf("lists = %d, want %d: the paint and maxSettleRounds re-lists", herdr.lists, 1+maxSettleRounds)
	}
}

func TestRunRecordsItsSocketForClearIndicators(t *testing.T) {
	painter, _, _ := newTestPainter(t, &fakeIndicatorHerdr{}, phaseOn)
	for range 2 {
		if err := painter.run(context.Background()); err != nil {
			t.Fatalf("run = %v", err)
		}
	}
	names, err := filepath.Glob(filepath.Join(painter.lockDir, "*"))
	if err != nil || len(names) != 1 || filepath.Base(names[0]) != "indicators-"+socketKey(painter.socket)+".socket" {
		t.Fatalf("lock dir holds %v, %v, want only the socket's record", names, err)
	}
	if content, err := os.ReadFile(names[0]); err != nil || string(content) != painter.socket {
		t.Fatalf("record = %q, %v, want the socket path %q", content, err, painter.socket)
	}
}

func TestBlinkerClearsAndStopsWhenItsRecordIsRemoved(t *testing.T) {
	herdr := blockedHerdr()
	herdr.panes[0].Tokens = map[string]string{"other": "keep"}
	painter, stepping, stderr := newTestPainter(t, herdr, phaseOn)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stepping.onSleep = func(ctx context.Context) error {
		if len(stepping.sleeps) > 4 {
			cancel() // a blinker that never stops ends here
			return ctx.Err()
		}
		if len(stepping.sleeps) == 2 {
			if err := os.Remove(painter.recordPath()); err != nil {
				t.Errorf("remove record: %v", err)
			}
		}
		return nil
	}
	if err := painter.run(ctx); err != nil {
		t.Fatalf("run = %v; stderr %q", err, stderr)
	}
	if len(stepping.sleeps) != 2 || herdr.lists != 3 {
		t.Fatalf("sleeps %v, lists %d, want the blinker to stop at the tick after the removal: 2 sleeps, 3 lists",
			stepping.sleeps, herdr.lists)
	}
	if got := herdr.panes[0].Tokens; len(got) != 1 || got["other"] != "keep" {
		t.Fatalf("pane tokens = %v, want only the other source's token left", got)
	}
	if len(herdr.spaces[0].Tokens) != 0 {
		t.Fatalf("workspace tokens = %v, want none", herdr.spaces[0].Tokens)
	}
}

func TestClearIndicatorsClearsEveryRecordedSocketAndForgetsTheGoneOnes(t *testing.T) {
	lockDir := filepath.Join(t.TempDir(), "locks")
	live := &fakeIndicatorHerdr{panes: []*fakeTarget{{
		ID: "w1:p1", Workspace: "w1", Status: "working",
		Tokens: map[string]string{"other": "keep"},
	}}, spaces: []*fakeTarget{{ID: "w1", Status: "done"}}}
	broken := &fakeIndicatorHerdr{failList: func(int) error { return errors.New("protocol garbage") }}
	sockets := map[string]func(context.Context, string, ...[]byte) ([]json.RawMessage, error){
		"/fake/live.sock":   live.call,
		"/fake/broken.sock": broken.call,
		"/fake/gone.sock": func(context.Context, string, ...[]byte) ([]json.RawMessage, error) {
			return nil, fmt.Errorf("herdr socket /fake/gone.sock: dial for pane.list: %w", syscall.ECONNREFUSED)
		},
	}
	route := func(ctx context.Context, socket string, lines ...[]byte) ([]json.RawMessage, error) {
		return sockets[socket](ctx, socket, lines...)
	}
	for socket := range sockets {
		painter := &indicatorPainter{
			socket: socket, lockDir: lockDir, stderr: &bytes.Buffer{}, call: route,
			clock: &steppingClock{now: phaseOn}, reported: map[string]bool{},
		}
		if err := painter.record(); err != nil {
			t.Fatalf("record %s: %v", socket, err)
		}
		if socket == "/fake/live.sock" {
			if _, _, err := painter.paint(context.Background()); err != nil {
				t.Fatalf("paint %s: %v", socket, err)
			}
		}
	}
	if live.panes[0].Tokens[IndicatorWorking] == "" || live.spaces[0].Tokens[IndicatorDone] == "" {
		t.Fatalf("setup: live tokens %v / %v, want painted indicators", live.panes[0].Tokens, live.spaces[0].Tokens)
	}
	stderr := &bytes.Buffer{}
	err := clearRecordedIndicators(context.Background(), lockDir, stderr, route)
	if err == nil || !strings.Contains(err.Error(), "/fake/broken.sock") ||
		!strings.Contains(stderr.String(), "protocol garbage") {
		t.Fatalf("clear = %v, stderr %q, want the broken socket's failure named and written", err, stderr)
	}
	if got := live.panes[0].Tokens; len(got) != 1 || got["other"] != "keep" || len(live.spaces[0].Tokens) != 0 {
		t.Fatalf("live tokens %v / %v, want only the other source's token left", got, live.spaces[0].Tokens)
	}
	names, _ := filepath.Glob(filepath.Join(lockDir, "indicators-*.socket"))
	if want := filepath.Join(lockDir, "indicators-"+socketKey("/fake/broken.sock")+".socket"); len(names) != 1 ||
		names[0] != want {
		t.Fatalf("records left = %v, want only the failed socket's %s", names, want)
	}
	absent := filepath.Join(t.TempDir(), "absent")
	if err := clearRecordedIndicators(context.Background(), absent, nil, route); err != nil {
		t.Fatalf("clear over a missing lock directory = %v, want nil", err)
	}
}
