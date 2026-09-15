package kill

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"hostops/pfm/internal/compose"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/gather"
	"hostops/pfm/internal/index"
	"hostops/pfm/internal/paths"
	"hostops/pfm/internal/resolve"
	"hostops/pfm/internal/shared"
	"hostops/pfm/internal/store"
)

type fakeProc struct {
	pids    []int
	cmdline map[int][]string
	links   map[int][]gather.FDLink
	stats   map[int]gather.ProcStat
	environ map[int]map[string]string
	pidsErr error
}

func (proc *fakeProc) PIDs() ([]int, error) {
	return append([]int(nil), proc.pids...), proc.pidsErr
}

func (proc *fakeProc) Cmdline(pid int) ([]string, error) {
	if value, found := proc.cmdline[pid]; found {
		return append([]string(nil), value...), nil
	}
	return nil, os.ErrNotExist
}

func (proc *fakeProc) Environ(pid int) (map[string]string, error) {
	if value, found := proc.environ[pid]; found {
		return value, nil
	}
	return nil, os.ErrNotExist
}

func (proc *fakeProc) FDLinks(pid int) ([]gather.FDLink, error) {
	if value, found := proc.links[pid]; found {
		return append([]gather.FDLink(nil), value...), nil
	}
	return nil, os.ErrNotExist
}

func (proc *fakeProc) Stat(pid int) (gather.ProcStat, error) {
	if value, found := proc.stats[pid]; found {
		return value, nil
	}
	return gather.ProcStat{}, os.ErrNotExist
}

type fakeTmux struct {
	mutex         sync.Mutex
	panePID       int
	existsFor     int
	existsCalls   int
	sent          []string
	killedPanes   []string
	killedServers []string
	// viewport wiring: which terminals watch the chat, and which pane each of
	// those terminals is. Both empty means nothing is watching it.
	clientTTYs []string
	panesByTTY map[string]string
}

func (tmux *fakeTmux) ClientTTYs(
	_ context.Context,
	_ string,
) ([]string, error) {
	tmux.mutex.Lock()
	defer tmux.mutex.Unlock()
	return append([]string(nil), tmux.clientTTYs...), nil
}

func (tmux *fakeTmux) PanesByTTY(
	_ context.Context,
	_ string,
) (map[string]string, error) {
	tmux.mutex.Lock()
	defer tmux.mutex.Unlock()
	panes := make(map[string]string, len(tmux.panesByTTY))
	for tty, paneID := range tmux.panesByTTY {
		panes[tty] = paneID
	}
	return panes, nil
}

func (tmux *fakeTmux) PanePID(
	_ context.Context,
	_, _ string,
) (int, error) {
	if tmux.panePID == 0 {
		return 0, errors.New("no pane pid")
	}
	return tmux.panePID, nil
}

func (tmux *fakeTmux) PaneExists(
	_ context.Context,
	_, _ string,
) bool {
	tmux.mutex.Lock()
	defer tmux.mutex.Unlock()
	tmux.existsCalls++
	return tmux.existsCalls <= tmux.existsFor
}

func (tmux *fakeTmux) SendLine(
	_ context.Context,
	_, _ string,
	line string,
) error {
	tmux.mutex.Lock()
	defer tmux.mutex.Unlock()
	tmux.sent = append(tmux.sent, line)
	return nil
}

func (tmux *fakeTmux) KillPane(
	_ context.Context,
	socketPath, paneID string,
) error {
	tmux.mutex.Lock()
	defer tmux.mutex.Unlock()
	tmux.killedPanes = append(
		tmux.killedPanes,
		filepath.Base(socketPath)+"\t"+paneID,
	)
	return nil
}

func (tmux *fakeTmux) KillServer(
	_ context.Context,
	socketPath string,
) error {
	tmux.mutex.Lock()
	defer tmux.mutex.Unlock()
	tmux.killedServers = append(tmux.killedServers, filepath.Base(socketPath))
	return nil
}

type captureSpawner struct {
	args []ExitArgs
}

func (spawner *captureSpawner) Spawn(
	_ context.Context,
	args ExitArgs,
) error {
	spawner.args = append(spawner.args, args)
	return nil
}

type refreshFunc func(context.Context) error

func (function refreshFunc) Refresh(ctx context.Context) error {
	return function(ctx)
}

func TestFinisherDiscoversConfigOwnedClaudeRoots(t *testing.T) {
	jail := newKillJail(t)
	database := jail.open(t)
	defer database.Close()

	configDir := filepath.Join(jail.home, ".cc", "7")
	writeTestFile(
		t,
		filepath.Join(configDir, ".credentials.json"),
		`{"claudeAiOauth":{"accessToken":"fixture"}}`,
	)
	finisher, err := NewFinisher(database, Dependencies{Paths: paths.Values{
		Home:    jail.home,
		SIDDir:  jail.sidDir,
		Roots:   map[pfmengine.ID][]string{pfmengine.Codex: {jail.codexRoot}},
		TmuxDir: jail.tmuxDir,
	}})
	if err != nil {
		t.Fatal(err)
	}
	refresher, ok := finisher.refresher.(indexRefresher)
	if !ok {
		t.Fatalf("refresher = %T, want indexRefresher", finisher.refresher)
	}
	want := []string{filepath.Join(configDir, "projects")}
	if !reflect.DeepEqual(refresher.claudeRoots, want) {
		t.Fatalf("Claude roots = %q, want config-owned discovery %q", refresher.claudeRoots, want)
	}
}

// A Codex tool shell is served by app-server and therefore has no ambient
// TMUX/TMUX_PANE even though its thread still owns a live fleet seat. The CLI
// resolves that seat before it reaches Manager.Kill; the manager must preserve
// the resolved address so `pfm chat kill self --exit` can hand the exact pane
// to the detached finisher.
func TestManagerCanExitResolvedCodexSelfWithoutAmbientTmux(t *testing.T) {
	jail := newKillJail(t)
	database := jail.open(t)
	defer database.Close()
	spawner := &captureSpawner{}
	manager, err := New(database, Dependencies{
		Spawner: spawner,
		Now:     func() time.Time { return time.Unix(600, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}

	const id = "20202020-2020-4020-8020-202020202020"
	target, err := manager.Kill(context.Background(), Request{
		ID:         id,
		Engine:     pfmengine.Codex,
		Exit:       true,
		SocketName: "probe-codex-self",
		PaneID:     "%7",
	})
	if err != nil {
		t.Fatal(err)
	}
	if target.ID != id || len(spawner.args) != 1 {
		t.Fatalf("target=%#v spawn=%#v", target, spawner.args)
	}
	if got, want := spawner.args[0].SocketPath,
		filepath.Join(jail.tmuxDir, "probe-codex-self"); got != want {
		t.Fatalf("socket path=%q, want %q", got, want)
	}
	if spawner.args[0].PaneID != "%7" {
		t.Fatalf("pane=%q, want %%7", spawner.args[0].PaneID)
	}
}

func TestManagerIdentifiesClaudeAndCodexSelf(t *testing.T) {
	jail := newKillJail(t)
	database := jail.open(t)
	defer database.Close()
	ctx := context.Background()

	claudeID := "11111111-1111-4111-8111-111111111111"
	claudePath := filepath.Join(jail.claudeRoot, claudeID+".jsonl")
	if err := database.UpsertTranscript(ctx, store.Transcript{
		UUID:        claudeID,
		Path:        claudePath,
		PromptCount: 7,
	}); err != nil {
		t.Fatal(err)
	}
	codexID := "22222222-2222-4222-8222-222222222222"
	rolloutPath := filepath.Join(
		jail.codexRoot,
		"sessions",
		"2026",
		"07",
		"rollout-2026-07-27T10-00-00-"+codexID+".jsonl",
	)
	if err := database.UpsertRollout(ctx, store.Rollout{
		ID:          codexID,
		Path:        rolloutPath,
		PromptCount: 9,
		UserThread:  true,
	}); err != nil {
		t.Fatal(err)
	}

	writeTestFile(t, rolloutPath, `{"type":"session_meta","payload":{"id":"`+codexID+`","thread_source":"user"}}`+"\n")
	tmux := &fakeTmux{panePID: 10}
	proc := &fakeProc{
		pids:    []int{20},
		cmdline: map[int][]string{20: {"codex"}},
		links: map[int][]gather.FDLink{
			20: {{FD: 12, Target: rolloutPath}},
		},
		stats:   map[int]gather.ProcStat{20: {ParentPID: 10}},
		environ: map[int]map[string]string{},
	}
	spawner := &captureSpawner{}
	now := time.Unix(1_700_000_000, 0)
	manager, err := New(database, Dependencies{
		ProcFS:  proc,
		Tmux:    tmux,
		Spawner: spawner,
		Now:     func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	socketPath := filepath.Join(jail.tmuxDir, "cc-100-1-1")
	target, err := manager.Kill(ctx, Request{
		Self: true,
		Environment: SelfEnvironment{
			TMUX:            socketPath + ",1,0",
			TMUXPane:        "%3",
			ClaudeSessionID: claudeID,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if target.ID != claudeID || target.Engine != pfmengine.Claude {
		t.Fatalf("Claude target = %#v", target)
	}
	assertKilled(t, database, claudeID, pfmengine.Claude, now.Unix())

	cxSocket := filepath.Join(jail.tmuxDir, "cx-200-1-1")
	target, err = manager.Kill(ctx, Request{
		Self: true,
		Exit: true,
		Environment: SelfEnvironment{
			TMUX:     cxSocket + ",2,0",
			TMUXPane: "%8",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if target.ID != codexID || target.Engine != pfmengine.Codex {
		t.Fatalf("Codex target = %#v", target)
	}
	assertKilled(t, database, codexID, pfmengine.Codex, now.Unix())
	// Hiding a LIVE chat also ends it (§ manager.Kill), so the FIRST Kill call
	// above — self, no --exit, but a live TMUX pane — spawns the finisher too;
	// only the SECOND call's --exit is the caller's own explicit vouch for the
	// same choreography.
	if len(spawner.args) != 2 ||
		spawner.args[0].ID != claudeID ||
		spawner.args[0].PaneID != "%3" ||
		spawner.args[0].SocketName != "cc-100-1-1" ||
		spawner.args[1].ID != codexID ||
		spawner.args[1].PaneID != "%8" ||
		spawner.args[1].SocketName != "cx-200-1-1" {
		t.Fatalf("spawned args = %#v", spawner.args)
	}

	if err := manager.Unkill(ctx, claudeID); err != nil {
		t.Fatal(err)
	}
	if _, found, err := database.Killed(ctx, claudeID); err != nil || found {
		t.Fatalf("Killed(Claude) found=%v err=%v, want absent", found, err)
	}
}

// A Codex session that writes no rollout file — the normal shape of a
// paginated thread since Codex 0.146.1 — holds no rollout file descriptor,
// so pid ancestry alone cannot name it. It must still be able to kill ITSELF.
func TestManagerStoreOnlyCodexSelfKills(t *testing.T) {
	jail := newKillJail(t)
	database := jail.open(t)
	defer database.Close()
	ctx := context.Background()

	threadID := "55555555-5555-4555-8555-555555555555"
	writeCodexStateThread(
		t,
		filepath.Join(jail.codexRoot, "state_0.sqlite"),
		threadID,
		"/work/store-only",
		"",
		1_700_000_000,
	)

	tmux := &fakeTmux{panePID: 11}
	proc := &fakeProc{
		pids:    []int{30},
		cmdline: map[int][]string{30: {"codex"}},
		links: map[int][]gather.FDLink{
			30: {{FD: 3, Target: "/dev/null"}},
		},
		stats: map[int]gather.ProcStat{30: {ParentPID: 11}},
		environ: map[int]map[string]string{
			30: {resolve.CodexThreadEnv: threadID},
		},
	}
	now := time.Unix(1_700_000_500, 0)
	manager, err := New(database, Dependencies{
		ProcFS:  proc,
		Tmux:    tmux,
		Spawner: &captureSpawner{},
		Now:     func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	target, err := manager.Kill(ctx, Request{
		Self: true,
		Environment: SelfEnvironment{
			TMUX:     filepath.Join(jail.tmuxDir, "cx-600-1-1") + ",4,0",
			TMUXPane: "%9",
		},
	})
	if err != nil {
		t.Fatalf("store-only Codex self-kill: %v", err)
	}
	if target.ID != threadID || target.Engine != pfmengine.Codex {
		t.Fatalf("store-only Codex target = %#v", target)
	}
	// No rollout row exists for this thread — it lives only in Codex's own
	// state sqlite — so the fleet index cannot name its engine.
	assertKilled(t, database, threadID, "", now.Unix())
}

// A paginated thread that wrote a rollout file earlier but no longer holds it
// open is named by the state store's own rollout_path, not by a file walk.
func TestManagerCodexSelfReadsRolloutPathFromStateStore(t *testing.T) {
	jail := newKillJail(t)
	database := jail.open(t)
	defer database.Close()
	ctx := context.Background()

	threadID := "55555555-5555-4555-8555-555555555555"
	rolloutID := "66666666-6666-4666-8666-666666666666"
	rolloutPath := filepath.Join(
		jail.codexRoot,
		"sessions",
		"2026",
		"07",
		"rollout-2026-07-27T10-00-00-"+rolloutID+".jsonl",
	)
	writeCodexStateThread(
		t,
		filepath.Join(jail.codexRoot, "state_0.sqlite"),
		threadID,
		"/work/paginated",
		rolloutPath,
		1_700_000_000,
	)

	now := time.Unix(1_700_000_500, 0)
	manager, err := New(database, Dependencies{
		ProcFS: &fakeProc{
			pids:    []int{30},
			cmdline: map[int][]string{30: {"codex"}},
			links: map[int][]gather.FDLink{
				30: {{FD: 3, Target: "/dev/null"}},
			},
			stats: map[int]gather.ProcStat{30: {ParentPID: 11}},
			environ: map[int]map[string]string{
				30: {resolve.CodexThreadEnv: threadID},
			},
		},
		Tmux:    &fakeTmux{panePID: 11},
		Spawner: &captureSpawner{},
		Now:     func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	target, err := manager.Kill(ctx, Request{
		Self: true,
		Environment: SelfEnvironment{
			TMUX:     filepath.Join(jail.tmuxDir, "cx-700-1-1") + ",5,0",
			TMUXPane: "%9",
		},
	})
	if err != nil {
		t.Fatalf("paginated Codex self-kill: %v", err)
	}
	if target.ID != rolloutID || target.DataPath != rolloutPath {
		t.Fatalf(
			"paginated Codex target = %#v, want id %q from the state store",
			target,
			rolloutID,
		)
	}
	assertKilled(t, database, rolloutID, "", now.Unix())
}

// AdvanceCodexPane is the reconcile step itself: the binding MOVES to
// whatever the pane's status line shows on every pass, and reports the
// thread it moved off of so the caller can decide whether that is a clear.
func TestAdvanceCodexPaneReturnsThePreviousBindingAndChangedFlag(t *testing.T) {
	jail := newKillJail(t)
	database := jail.open(t)
	defer database.Close()
	ctx := context.Background()

	manager, err := New(database, Dependencies{})
	if err != nil {
		t.Fatal(err)
	}

	// First sight of a pane: nothing to have moved off of, but the write
	// still counts as a change.
	previous, changed, err := manager.AdvanceCodexPane(ctx, "cx-probe", "%7", "thread-a")
	if err != nil {
		t.Fatalf("AdvanceCodexPane(first sight) error = %v", err)
	}
	if previous != "" || !changed {
		t.Fatalf("AdvanceCodexPane(first sight) = (%q, %v), want (\"\", true)", previous, changed)
	}

	// The pane still shows the same thread: no change, and the binding it
	// reports is the one already on file.
	previous, changed, err = manager.AdvanceCodexPane(ctx, "cx-probe", "%7", "thread-a")
	if err != nil {
		t.Fatalf("AdvanceCodexPane(unchanged) error = %v", err)
	}
	if previous != "thread-a" || changed {
		t.Fatalf("AdvanceCodexPane(unchanged) = (%q, %v), want (\"thread-a\", false)", previous, changed)
	}

	// The pane now shows a different thread: previous is the one it moved
	// off of, and changed is true — the caller's own clear signal.
	previous, changed, err = manager.AdvanceCodexPane(ctx, "cx-probe", "%7", "thread-b")
	if err != nil {
		t.Fatalf("AdvanceCodexPane(moved) error = %v", err)
	}
	if previous != "thread-a" || !changed {
		t.Fatalf("AdvanceCodexPane(moved) = (%q, %v), want (\"thread-a\", true)", previous, changed)
	}

	bound, found, err := manager.CodexPaneBinding(ctx, "cx-probe", "%7")
	if err != nil || !found || bound != "thread-b" {
		t.Fatalf("CodexPaneBinding() = (%q, %v, %v), want (\"thread-b\", true, nil)", bound, found, err)
	}
}

func TestManagerClaudeCrumbPrecedenceWritesNullBaseline(t *testing.T) {
	jail := newKillJail(t)
	database := jail.open(t)
	defer database.Close()
	socketName := "cc-300-1-1"
	paneID := "%5"
	socketID := "33333333-3333-4333-8333-333333333333"
	paneIDValue := "44444444-4444-4444-8444-444444444444"
	writeTestFile(
		t,
		filepath.Join(jail.sidDir, socketName),
		"/jail/"+socketID+".jsonl\n",
	)
	writeTestFile(
		t,
		filepath.Join(jail.sidDir, socketName+"."+paneID),
		"/jail/"+paneIDValue+".jsonl\n",
	)
	manager, err := New(database, Dependencies{
		ProcFS:  &fakeProc{},
		Tmux:    &fakeTmux{},
		Spawner: &captureSpawner{},
		Now:     func() time.Time { return time.Unix(25, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	target, err := manager.Kill(context.Background(), Request{
		Self: true,
		Environment: SelfEnvironment{
			TMUX:     filepath.Join(jail.tmuxDir, socketName) + ",1,0",
			TMUXPane: paneID,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if target.ID != paneIDValue {
		t.Fatalf("crumb selected %q, want pane crumb %q", target.ID, paneIDValue)
	}
	killed, found, err := database.Killed(context.Background(), paneIDValue)
	if err != nil || !found {
		t.Fatalf("Killed() found=%v err=%v", found, err)
	}
	if killed.BaselinePrompts != nil {
		t.Fatalf("baseline = %v, want an explicit kill to stay permanent", *killed.BaselinePrompts)
	}
}

// A kill is permanent: new prompts on the transcript never bring the chat
// back, and only an explicit unkill does.
func TestKilledChatStaysKilledAsItGrowsUntilUnkill(t *testing.T) {
	jail := newKillJail(t)
	database := jail.open(t)
	defer database.Close()
	ctx := context.Background()

	id := "77777777-7777-4777-8777-777777777777"
	transcriptPath := filepath.Join(jail.claudeRoot, "project-alpha", id+".jsonl")
	writeTestFile(
		t,
		transcriptPath,
		`{"type":"user","cwd":"/work/alpha","message":{"content":"first"}}`+"\n",
	)
	reindex(t, database)

	manager, err := New(database, Dependencies{
		ProcFS:  &fakeProc{},
		Tmux:    &fakeTmux{},
		Spawner: &captureSpawner{},
		Now:     func() time.Time { return time.Unix(50, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !listedByDefault(t, database, id) {
		t.Fatal("indexed chat is missing from the default listing")
	}
	if _, err := manager.Kill(ctx, Request{ID: id}); err != nil {
		t.Fatal(err)
	}
	assertKilled(t, database, id, pfmengine.Claude, 50)
	if listedByDefault(t, database, id) {
		t.Fatal("killed chat is still listed")
	}

	appendTestFile(
		t,
		transcriptPath,
		`{"type":"user","cwd":"/work/alpha","message":{"content":"second"}}`+"\n"+
			`{"type":"user","cwd":"/work/alpha","message":{"content":"third"}}`+"\n",
	)
	reindex(t, database)
	transcript, found, err := database.Transcript(ctx, id)
	if err != nil || !found || transcript.PromptCount != 3 {
		t.Fatalf("regrown transcript = %#v found=%v err=%v", transcript, found, err)
	}
	if listedByDefault(t, database, id) {
		t.Fatal("a killed chat returned once its prompt count grew")
	}
	assertKilled(t, database, id, pfmengine.Claude, 50)

	if err := manager.Unkill(ctx, id); err != nil {
		t.Fatal(err)
	}
	if !listedByDefault(t, database, id) {
		t.Fatal("unkill did not bring the chat back")
	}
}

func reindex(t *testing.T, database *store.Store) {
	t.Helper()
	indexer, err := index.New(database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := indexer.Run(context.Background(), index.Options{}); err != nil {
		t.Fatal(err)
	}
}

// listedByDefault answers the visibility question twice — through the cached
// SQL candidate query and through compose — and fails if they disagree.
func listedByDefault(t *testing.T, database *store.Store, id string) bool {
	t.Helper()
	ctx := context.Background()
	candidates, _, _, err := database.DefaultCandidates(ctx, 50, 50)
	if err != nil {
		t.Fatal(err)
	}
	cached := false
	for _, candidate := range candidates {
		cached = cached || candidate.UUID == id
	}
	transcripts, err := database.Transcripts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	killed, err := database.KilledChats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	output := compose.Compose(compose.Input{
		Transcripts: transcripts,
		Killed:      killed,
		Options:     compose.Options{View: compose.DefaultView},
	})
	composed := false
	for _, row := range output.Rows {
		composed = composed || row.ID == id
	}
	if cached != composed {
		t.Fatalf(
			"cached candidates (%t) and compose (%t) disagree about %q",
			cached,
			composed,
			id,
		)
	}
	return composed
}

// TestKilledCodexLineageMatchesAnyMemberIDUntilUnkill is the Codex twin of
// TestKilledChatStaysKilledAsItGrowsUntilUnkill, guarding a different
// regression: a live resumed process can expose the CHILD rollout id rather
// than the ROOT every Codex row is keyed on
// (composer.rolloutRow). The kill below is written exactly that way — no
// lineage math, straight into the shared store — and must still kill the row;
// the unkill that follows
// is issued on the ROOT, the only id the picker ever shows, and must still
// lift it.
func TestKilledCodexLineageMatchesAnyMemberIDUntilUnkill(t *testing.T) {
	jail := newKillJail(t)
	database := jail.open(t)
	defer database.Close()
	ctx := context.Background()

	rootID := "88888888-8888-4888-8888-888888888888"
	childID := "99999999-9999-4999-8999-999999999999"
	if err := database.UpsertRollout(ctx, store.Rollout{
		ID:          rootID,
		Path:        filepath.Join(jail.codexRoot, "sessions", "rollout-"+rootID+".jsonl"),
		Size:        100,
		MTimeNS:     100,
		CWD:         "/work/proja",
		UserThread:  true,
		SessionID:   rootID,
		PromptCount: 1,
	}); err != nil {
		t.Fatal(err)
	}
	// The resumed child: same conversation, a LATER rollout file, linked back
	// to the root by session_id — exactly what `codex resume` produces.
	if err := database.UpsertRollout(ctx, store.Rollout{
		ID:          childID,
		Path:        filepath.Join(jail.codexRoot, "sessions", "rollout-"+childID+".jsonl"),
		Size:        200,
		MTimeNS:     200,
		CWD:         "/work/proja",
		UserThread:  true,
		SessionID:   rootID,
		PromptCount: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if !listedCodexByDefault(t, database, rootID) {
		t.Fatal("indexed lineage is missing from the default listing")
	}

	// Write the raw child id straight into the shared store, keyed on whichever
	// rollout file the live process
	// happened to hold — the CHILD, since it is the newer file a resumed
	// session appends to.
	if err := database.Kill(ctx, store.Killed{
		ID:       childID,
		Engine:   pfmengine.Codex,
		KilledAt: 50,
	}); err != nil {
		t.Fatal(err)
	}
	if listedCodexByDefault(t, database, rootID) {
		t.Fatal("a kill written raw on the resumed child left the lineage listed")
	}

	// Unkill is issued on the id the picker actually shows: the root. It must
	// still clear the child-keyed kill, or the row would come right back
	// killed the next time this same lineage is composed.
	manager, err := New(database, Dependencies{
		ProcFS:  &fakeProc{},
		Tmux:    &fakeTmux{},
		Spawner: &captureSpawner{},
		Now:     func() time.Time { return time.Unix(50, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Unkill(ctx, rootID); err != nil {
		t.Fatal(err)
	}
	if !listedCodexByDefault(t, database, rootID) {
		t.Fatal("unkill via the root did not bring the lineage back")
	}
	killed, err := database.KilledChats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(killed) != 0 {
		t.Fatalf("kills survived unkill: %#v", killed)
	}
}

// listedCodexByDefault is listedByDefault's Codex twin: it answers whether
// rootID's resume lineage is in the default listing through the cached SQL
// candidate query and through compose, and fails if they disagree.
func listedCodexByDefault(t *testing.T, database *store.Store, rootID string) bool {
	t.Helper()
	ctx := context.Background()
	_, candidates, _, err := database.DefaultCandidates(ctx, 50, 50)
	if err != nil {
		t.Fatal(err)
	}
	cached := false
	for _, candidate := range candidates {
		cached = cached || candidate.LineageRoot == rootID
	}
	rollouts, err := database.Rollouts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	killed, err := database.KilledChats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	output := compose.Compose(compose.Input{
		Rollouts: rollouts,
		Killed:   killed,
		Options:  compose.Options{View: compose.DefaultView},
	})
	composed := false
	for _, row := range output.Rows {
		composed = composed || row.ID == rootID
	}
	if cached != composed {
		t.Fatalf(
			"cached candidates (%t) and compose (%t) disagree about %q",
			cached,
			composed,
			rootID,
		)
	}
	return composed
}

func TestFinisherChoreographyAndTeammateReaping(t *testing.T) {
	jail := newKillJail(t)
	database := jail.open(t)
	defer database.Close()
	ctx := context.Background()
	id := "55555555-5555-4555-8555-555555555555"
	transcriptPath := filepath.Join(jail.claudeRoot, id+".jsonl")
	if err := database.UpsertTranscript(ctx, store.Transcript{
		UUID:        id,
		Path:        transcriptPath,
		PromptCount: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.Kill(ctx, store.Killed{
		ID:       id,
		Engine:   pfmengine.Claude,
		KilledAt: 100,
	}); err != nil {
		t.Fatal(err)
	}

	socketName := "cc-400-1-1"
	paneID := "%9"
	writeTestFile(t, filepath.Join(jail.sidDir, socketName), transcriptPath)
	writeTestFile(
		t,
		filepath.Join(jail.sidDir, socketName+"."+paneID),
		transcriptPath,
	)
	detached := filepath.Join(
		jail.home,
		".claude",
		".cc-new-children",
		id,
	)
	paneChildren := filepath.Join(
		jail.home,
		".claude",
		".cc-pane-children",
		id,
	)
	writeTestFile(t, detached, "cc-401-1-1\n")
	writeTestFile(t, paneChildren, "cc-402-1-1\t%12\n")

	tmux := &fakeTmux{existsFor: 2}
	refresher := refreshFunc(func(ctx context.Context) error {
		return database.UpsertTranscript(ctx, store.Transcript{
			UUID:        id,
			Path:        transcriptPath,
			PromptCount: 3,
		})
	})
	finisher, err := NewFinisher(database, Dependencies{
		Tmux:         tmux,
		Refresher:    refresher,
		Delay:        time.Millisecond,
		PollEvery:    time.Millisecond,
		PollAttempts: 5,
		Now:          func() time.Time { return time.Unix(200, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := finisher.Run(ctx, ExitArgs{
		Engine:     pfmengine.Claude,
		ID:         id,
		DataPath:   transcriptPath,
		SocketPath: filepath.Join(jail.tmuxDir, socketName),
		SocketName: socketName,
		PaneID:     paneID,
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tmux.sent, []string{"/exit"}) {
		t.Fatalf("sent = %q, want /exit", tmux.sent)
	}
	if !reflect.DeepEqual(tmux.killedServers, []string{"cc-401-1-1"}) {
		t.Fatalf("killed servers = %q", tmux.killedServers)
	}
	if got := tmux.killedPanes; !reflect.DeepEqual(
		got,
		[]string{socketName + "\t" + paneID, "cc-402-1-1\t%12"},
	) {
		t.Fatalf("killed panes = %q", got)
	}
	for _, path := range []string{
		filepath.Join(jail.sidDir, socketName),
		filepath.Join(jail.sidDir, socketName+"."+paneID),
		detached,
		paneChildren,
	} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s remains after cleanup: %v", path, err)
		}
	}
	assertKilled(t, database, id, pfmengine.Claude, 100)
}

// The live fleet registers teammates in the shared store and writes no flat
// file. The reaper must take the table as its source and reach the same two
// kills from it: kill-server for a
// detached teammate on its own socket, kill-pane for one sharing this server.
func TestFinisherReapsTeammatesFromTheSharedChildrenTable(t *testing.T) {
	jail := newKillJail(t)
	database := jail.open(t)
	defer database.Close()
	ctx := context.Background()
	id := "77777777-7777-4777-8777-777777777777"
	transcriptPath := filepath.Join(jail.claudeRoot, id+".jsonl")
	if err := database.UpsertTranscript(ctx, store.Transcript{
		UUID:        id,
		Path:        transcriptPath,
		PromptCount: 2,
	}); err != nil {
		t.Fatal(err)
	}
	state := database.Shared()
	if err := state.AddChild(ctx, shared.KindNew, id, "cc-501-1-1", 1); err != nil {
		t.Fatal(err)
	}
	if err := state.AddChild(ctx, shared.KindPane, id, "cc-502-1-1\t%21", 1); err != nil {
		t.Fatal(err)
	}
	// A teammate of a DIFFERENT chat must survive this reap.
	if err := state.AddChild(
		ctx,
		shared.KindNew,
		"neighbour",
		"cc-503-1-1",
		1,
	); err != nil {
		t.Fatal(err)
	}

	socketName := "cc-500-1-1"
	paneID := "%9"
	tmux := &fakeTmux{existsFor: 1}
	finisher, err := NewFinisher(database, Dependencies{
		Tmux:         tmux,
		Refresher:    refreshFunc(func(context.Context) error { return nil }),
		Delay:        time.Millisecond,
		PollEvery:    time.Millisecond,
		PollAttempts: 2,
		Now:          func() time.Time { return time.Unix(300, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := finisher.Run(ctx, ExitArgs{
		Engine:     pfmengine.Claude,
		ID:         id,
		DataPath:   transcriptPath,
		SocketPath: filepath.Join(jail.tmuxDir, socketName),
		SocketName: socketName,
		PaneID:     paneID,
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tmux.killedServers, []string{"cc-501-1-1"}) {
		t.Fatalf("killed servers = %q, want only the detached teammate", tmux.killedServers)
	}
	if !reflect.DeepEqual(tmux.killedPanes, []string{
		socketName + "\t" + paneID,
		"cc-502-1-1\t%21",
	}) {
		t.Fatalf("killed panes = %q", tmux.killedPanes)
	}
	// The reaped rows are gone and the neighbour's rows are untouched.
	if values, _, err := state.Children(ctx, shared.KindNew, id); err != nil ||
		len(values) != 0 {
		t.Fatalf("children after reap = %v, %v", values, err)
	}
	if values, _, err := state.Children(ctx, shared.KindPane, id); err != nil ||
		len(values) != 0 {
		t.Fatalf("pane children after reap = %v, %v", values, err)
	}
	values, _, err := state.Children(ctx, shared.KindNew, "neighbour")
	if err != nil || !reflect.DeepEqual(values, []string{"cc-503-1-1"}) {
		t.Fatalf("neighbour children = %v, %v", values, err)
	}
}

func TestFinisherCodexUsesQuit(t *testing.T) {
	jail := newKillJail(t)
	database := jail.open(t)
	defer database.Close()
	ctx := context.Background()
	id := "66666666-6666-4666-8666-666666666666"
	path := filepath.Join(jail.codexRoot, "sessions", "rollout-"+id+".jsonl")
	if err := database.UpsertRollout(ctx, store.Rollout{
		ID:          id,
		Path:        path,
		PromptCount: 4,
		UserThread:  true,
	}); err != nil {
		t.Fatal(err)
	}
	tmux := &fakeTmux{}
	finisher, err := NewFinisher(database, Dependencies{
		Tmux:         tmux,
		Refresher:    refreshFunc(func(context.Context) error { return nil }),
		Delay:        time.Millisecond,
		PollEvery:    time.Millisecond,
		PollAttempts: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := finisher.Run(ctx, ExitArgs{
		Engine:     pfmengine.Codex,
		ID:         id,
		DataPath:   path,
		SocketPath: filepath.Join(jail.tmuxDir, "cx-500-1-1"),
		SocketName: "cx-500-1-1",
		PaneID:     "%10",
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tmux.sent, []string{"/quit"}) {
		t.Fatalf("sent = %q, want /quit", tmux.sent)
	}
	assertKilled(t, database, id, pfmengine.Codex, tmuxKilledAt(t, database, id))
}

func TestCommandSpawnerUsesSetsidSelfReexec(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh is not installed")
	}
	root := t.TempDir()
	argvPath := filepath.Join(root, "argv")
	setsidPath := filepath.Join(root, "setsid")
	writeTestFile(
		t,
		setsidPath,
		"#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$PFM_SPAWN_ARGV\"\n",
	)
	if err := os.Chmod(setsidPath, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PFM_SPAWN_ARGV", argvPath)
	spawner := CommandSpawner{
		Executable: "/jail/pfm",
		Setsid:     setsidPath,
		ConfigPath: "/jail/config/pfm.json",
	}
	args := ExitArgs{
		Engine:     pfmengine.Claude,
		ID:         "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
		DataPath:   "/jail/transcript.jsonl",
		SocketPath: "/jail/tmux/cc-1-1-1",
		SocketName: "cc-1-1-1",
		PaneID:     "%3",
	}
	if err := spawner.Spawn(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(argvPath)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"-f",
		"/jail/pfm",
		"--config",
		"/jail/config/pfm.json",
		"internal",
		"kill-exit",
		"--engine",
		string(args.Engine),
		"--id",
		args.ID,
		"--path",
		args.DataPath,
		"--socket",
		args.SocketPath,
		"--socket-name",
		args.SocketName,
		"--pane",
		args.PaneID,
		"",
	}, "\n")
	if string(content) != want {
		t.Fatalf("setsid argv = %q, want %q", content, want)
	}
}

type killJail struct {
	root       string
	home       string
	sidDir     string
	tmuxDir    string
	claudeRoot string
	codexRoot  string
	dbPath     string
}

func newKillJail(t *testing.T) killJail {
	t.Helper()
	root := t.TempDir()
	jail := killJail{
		root:       root,
		home:       filepath.Join(root, "home"),
		sidDir:     filepath.Join(root, "sid"),
		tmuxDir:    filepath.Join(root, "tmux"),
		claudeRoot: filepath.Join(root, "claude"),
		codexRoot:  filepath.Join(root, "codex"),
		dbPath:     filepath.Join(root, "fleet.db"),
	}
	for _, directory := range []string{
		jail.home,
		jail.sidDir,
		jail.tmuxDir,
		jail.claudeRoot,
		jail.codexRoot,
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("TMUX_TMPDIR", filepath.Join(root, "tmp"))
	t.Setenv("PFM_DB", jail.dbPath)
	t.Setenv("PFM_SID_DIR", jail.sidDir)
	t.Setenv("PFM_CLAUDE_ROOTS", jail.claudeRoot)
	t.Setenv("PFM_CODEX_ROOT", jail.codexRoot)
	t.Setenv("PFM_TMUX_DIR", jail.tmuxDir)
	t.Setenv("PFM_HOME", jail.home)
	return jail
}

func (jail killJail) open(t *testing.T) *store.Store {
	t.Helper()
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	return database
}

func appendTestFile(t *testing.T, path, content string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeCodexStateThread builds a scratch Codex state store holding one live
// user thread, using the real schema captured from a live 0.146.1 store. The
// "sqlite" driver is registered by the store package this test already links.
func writeCodexStateThread(
	t *testing.T,
	path, id, cwd, rolloutPath string,
	createdAt int64,
) {
	t.Helper()
	schema, err := os.ReadFile(
		filepath.Join("..", "..", "testdata", "codex-state-schema.sql"),
	)
	if err != nil {
		t.Fatalf("read Codex state schema: %v", err)
	}
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("create scratch Codex state store: %v", err)
	}
	defer database.Close()
	ctx := context.Background()
	if _, err := database.ExecContext(ctx, string(schema)); err != nil {
		t.Fatalf("apply Codex state schema: %v", err)
	}
	if _, err := database.ExecContext(ctx, `
INSERT INTO threads (
  id, rollout_path, created_at, updated_at, source, model_provider, cwd,
  title, sandbox_policy, approval_mode, thread_source, history_mode
) VALUES (?, ?, ?, ?, 'cli', 'openai', ?, '', 'workspace-write',
  'on-request', 'user', 'legacy')`,
		id,
		rolloutPath,
		createdAt,
		createdAt,
		cwd,
	); err != nil {
		t.Fatalf("insert scratch Codex thread %q: %v", id, err)
	}
}

// assertKilled also pins the prompt baseline to NULL: these are explicit
// kills, so none may acquire /clear's auto-unkill behavior.
//
// The engine argument is the DERIVED one. A chat the fleet index has never
// seen — a Codex thread known only to Codex's own state store, say — derives
// no engine at all, and the caller passes "" for it: the kill holds either way,
// because an empty engine means "killed whatever the engine".
func assertKilled(
	t *testing.T,
	database *store.Store,
	id string,
	engine pfmengine.ID,
	killedAt int64,
) {
	t.Helper()
	killed, found, err := database.Killed(context.Background(), id)
	if err != nil || !found {
		t.Fatalf("Killed(%q) found=%v err=%v", id, found, err)
	}
	if killed.Engine != engine ||
		killed.KilledAt != killedAt ||
		killed.BaselinePrompts != nil {
		t.Fatalf("Killed(%q) = %#v", id, killed)
	}
}

func tmuxKilledAt(t *testing.T, database *store.Store, id string) int64 {
	t.Helper()
	killed, found, err := database.Killed(context.Background(), id)
	if err != nil || !found {
		t.Fatalf("Killed(%q) found=%v err=%v", id, found, err)
	}
	return killed.KilledAt
}

// THE AGENT-ROW REGRESSION. ⌃X on a live agent row wrote NOTHING: the agent's
// transcript had not reached the index yet, and the kill refused every id the
// index could not name. The row is composed straight from the running process,
// so "not indexed" is its NORMAL state, not an error — and the kill must land
// on the agent's own session uuid, reach the shared store, and hold while the
// process is still alive.
func TestKillingALiveAgentRowSticksWhileItRuns(t *testing.T) {
	jail := newKillJail(t)
	database := jail.open(t)
	defer database.Close()
	ctx := context.Background()

	// Exactly the reported shape: project "?", no transcript row, no file.
	agentID := "ad195f37-5a8f-4513-94a6-d3616afe566e"
	socket := "cc-1786319281-1986650-21068"
	paneID := "%4"
	if _, found, err := database.Transcript(ctx, agentID); err != nil || found {
		t.Fatalf("agent transcript found = %v, error = %v; want an unindexed id", found, err)
	}

	snapshot := gather.Snapshot{
		Agents: []gather.Agent{{
			PID:       424242,
			Socket:    socket,
			PaneID:    paneID,
			SessionID: agentID,
		}},
		Panes: []gather.Pane{{
			Socket:      socket,
			SessionName: socket,
			PaneID:      paneID,
		}},
	}
	agentRow := func(t *testing.T) (compose.Row, bool) {
		t.Helper()
		killed, err := database.KilledChats(ctx)
		if err != nil {
			t.Fatal(err)
		}
		output := compose.Compose(compose.Input{
			Snapshot: snapshot,
			Killed:   killed,
			Options:  compose.Options{View: compose.DefaultView},
		})
		for _, row := range output.Rows {
			if row.Kind == compose.Agent && row.ID == agentID {
				return row, true
			}
		}
		return compose.Row{}, false
	}

	if row, listed := agentRow(t); !listed || row.Project != "?" {
		t.Fatalf("agent row before the kill = %#v listed=%t", row, listed)
	}

	manager, err := New(database, Dependencies{
		ProcFS:  &fakeProc{},
		Tmux:    &fakeTmux{},
		Spawner: &captureSpawner{},
		Now:     func() time.Time { return time.Unix(4242, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	// The picker's own call: it holds the row, so it names the engine.
	target, err := manager.Kill(ctx, Request{ID: agentID, Engine: pfmengine.Claude})
	if err != nil {
		t.Fatalf("Kill() on a live agent row error = %v", err)
	}
	if target.ID != agentID || target.Engine != pfmengine.Claude {
		t.Fatalf("Kill() target = %#v, want the agent's own session uuid", target)
	}

	// An unindexed id derives no engine, but its shared database row is durable.
	assertKilled(t, database, agentID, "", 4242)

	// STICK: the process is still live and the row is still composed, so the
	// kill has to beat the live-agent short-circuit, not be skipped by it.
	if row, listed := agentRow(t); listed {
		t.Fatalf("killed agent row is still listed while its process lives: %#v", row)
	}
	if err := manager.Unkill(ctx, agentID); err != nil {
		t.Fatal(err)
	}
	if _, listed := agentRow(t); !listed {
		t.Fatal("unkill did not bring the agent row back")
	}
}

// A bare id the index cannot name is still an error, so a mistyped
// `pfm kill` argument cannot quietly record a kill for nothing. Only a
// caller holding the row vouches for it.
func TestKillingAnUnknownIDStillFailsWithoutAnEngine(t *testing.T) {
	jail := newKillJail(t)
	database := jail.open(t)
	defer database.Close()
	ctx := context.Background()

	manager, err := New(database, Dependencies{
		ProcFS:  &fakeProc{},
		Tmux:    &fakeTmux{},
		Spawner: &captureSpawner{},
		Now:     func() time.Time { return time.Unix(4242, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Kill(ctx, Request{ID: "no-such-chat"}); err == nil {
		t.Fatal("Kill() accepted an id no caller vouched for")
	}
	if _, found, err := database.Killed(ctx, "no-such-chat"); err != nil || found {
		t.Fatalf("Killed() found = %v, error = %v; want nothing recorded", found, err)
	}
}
