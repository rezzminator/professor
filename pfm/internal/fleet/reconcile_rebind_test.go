package fleet

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/kill"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/reload"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// noopCodexRenamer satisfies spawn.Tmux for a rebind that never clears a
// prior thread — ReconcileCodexPanesWith only reaches the renamer on
// action.ClearKill, which a pane's FIRST binding never sets, so none of these
// are ever called.
type noopCodexRenamer struct{}

func (noopCodexRenamer) NewSession(context.Context, spawn.SessionSpec) error { return nil }
func (noopCodexRenamer) Capture(context.Context, string, string) (string, error) {
	return "", nil
}
func (noopCodexRenamer) SendLiteral(context.Context, string, string, string) error { return nil }
func (noopCodexRenamer) SendKey(context.Context, string, string, string) error     { return nil }

// TestReconcileCodexPanesRecordsARebind: ReconcileCodexPanesWith walks the
// state door (spec § Middleware, `state`) — bound to rebound, comp=state,
// kind=fleet — whenever it advances a pane's binding, never the pane's
// socket or the thread id. The smallest fixture that makes one binding move:
// a single freshly-seen Codex pane whose status line shows a bare id
// matching an indexed rollout, with no prior binding to clear — first sight
// is itself a move (kill.Manager.AdvanceCodexPane), so the rename step
// (which needs a real Codex composer) is never reached.
func TestReconcileCodexPanesRecordsARebind(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	testjail.Fleet(t)
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	tmuxTmpDir := t.TempDir()
	resolved.TmuxDir = filepath.Join(tmuxTmpDir, "tmux-"+strconv.Itoa(os.Getuid()))

	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	const threadID = "12121212-1212-4212-8212-121212121212"
	rolloutPath := filepath.Join(
		resolved.Roots[pfmengine.Codex][0], "sessions", "2030", "01", "02",
		"rollout-2030-01-02T03-04-05-"+threadID+".jsonl",
	)
	if err := os.MkdirAll(filepath.Dir(rolloutPath), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"session_meta","payload":{"id":"` + threadID +
		`","thread_source":"user","cwd":"/work/example"}}` + "\n"
	if err := os.WriteFile(rolloutPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertRollout(context.Background(), store.Rollout{
		ID: threadID, Path: rolloutPath, CWD: "/work/example", UserThread: true, PromptCount: 1,
	}); err != nil {
		t.Fatal(err)
	}

	const socket = "cx-1800000099-1-1"
	startNoopCodexPane(t, tmuxTmpDir, socket, "  "+threadID+` · /work/example · Full Access\n`)

	manager, err := kill.New(database, kill.Dependencies{})
	if err != nil {
		t.Fatal(err)
	}

	recorderCtx, recorder := obs.Test(t)
	var stderr bytes.Buffer
	ReconcileCodexPanesWith(
		recorderCtx,
		database,
		gather.Snapshot{Panes: []gather.ProbePane{{
			Socket: socket, SessionName: socket, WindowID: "@1", PaneID: "%0", CurrentCommand: "codex",
		}}},
		pfmconfig.Runtime{Paths: resolved},
		noopCodexRenamer{},
		PrintWarn(&stderr),
	)

	bound, found, err := manager.CodexPaneBinding(context.Background(), socket, "%0")
	if err != nil || !found || bound != threadID {
		t.Fatalf("binding = (%q, %v, %v), want %q: stderr=%q", bound, found, err, threadID, stderr.String())
	}

	var foundTransition bool
	for _, record := range recorder.Records() {
		if record.Message != "state.transition" {
			continue
		}
		if kind, _ := record.Field("kind"); kind != "fleet" {
			continue
		}
		if next, _ := record.Field("next"); next == "rebound" {
			foundTransition = true
		}
	}
	if !foundTransition {
		t.Fatalf("ReconcileCodexPanesWith() wrote no fleet->rebound transition: %s", recorder.Raw())
	}
}

type recordingCodexRenamer struct {
	stage      int
	captures   int
	literals   []string
	keys       []string
	busy       bool
	captureErr error
}

func (*recordingCodexRenamer) NewSession(context.Context, spawn.SessionSpec) error { return nil }

func (renamer *recordingCodexRenamer) Capture(context.Context, string, string) (string, error) {
	renamer.captures++
	if renamer.captureErr != nil {
		return "", renamer.captureErr
	}
	if renamer.busy {
		return "Working (2s · 9 tokens) · esc to interrupt\n› ", nil
	}
	switch renamer.stage {
	case 1:
		return "rename the current thread", nil
	case 2, 3:
		return "Rename thread", nil
	case 4:
		return "Session renamed to E2_MAIN", nil
	default:
		return "› Ask Codex to do anything\nE2_MAIN · /work/example", nil
	}
}

func (renamer *recordingCodexRenamer) SendLiteral(_ context.Context, _, _, value string) error {
	renamer.literals = append(renamer.literals, value)
	if value == "/rename" {
		renamer.stage = 1
	} else {
		renamer.stage = 3
	}
	return nil
}

func (renamer *recordingCodexRenamer) SendKey(_ context.Context, _, _, key string) error {
	renamer.keys = append(renamer.keys, key)
	if key == "Enter" {
		switch renamer.stage {
		case 1:
			renamer.stage = 2
		case 3:
			renamer.stage = 4
		}
	}
	return nil
}

func TestReconcileCodexPanesDefersClearWhileReloadOrTurnRuns(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	testjail.Fleet(t)
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	tmuxTmpDir := t.TempDir()
	resolved.TmuxDir = filepath.Join(tmuxTmpDir, "tmux-"+strconv.Itoa(os.Getuid()))
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	}()
	ctx := context.Background()
	const oldID = "12121212-1212-4212-8212-121212121212"
	const newID = "34343434-3434-4434-8434-343434343434"
	for _, id := range []string{oldID, newID} {
		rolloutPath := filepath.Join(
			resolved.Roots[pfmengine.Codex][0],
			"sessions",
			"2030",
			"01",
			"02",
			"rollout-2030-01-02T03-04-05-"+id+".jsonl",
		)
		if err := os.MkdirAll(filepath.Dir(rolloutPath), 0o700); err != nil {
			t.Fatal(err)
		}
		body := `{"type":"session_meta","payload":{"id":"` + id + `","thread_source":"user","cwd":"/work/example"}}` + "\n"
		if err := os.WriteFile(rolloutPath, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := database.UpsertRollout(
			ctx,
			store.Rollout{ID: id, Path: rolloutPath, CWD: "/work/example", UserThread: true, PromptCount: 1},
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.UpsertCxName(ctx, store.CxName{ID: oldID, ThreadName: "E2_MAIN"}); err != nil {
		t.Fatal(err)
	}
	const socket = "cx-1800000099-1-2"
	const pane = "%0"
	startNoopCodexPane(t, tmuxTmpDir, socket, "  "+newID+` · /work/example · Full Access\n`)
	manager, err := kill.New(database, kill.Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	live := gather.Snapshot{
		Panes: []gather.ProbePane{
			{Socket: socket, SessionName: socket, WindowID: "@1", PaneID: pane, CurrentCommand: "codex"},
		},
	}
	runtime := pfmconfig.Runtime{Paths: resolved}
	renamer := &recordingCodexRenamer{}
	var stderr bytes.Buffer
	pass := func() bool {
		stderr.Reset()
		return ReconcileCodexPanesWith(ctx, database, live, runtime, renamer, PrintWarn(&stderr))
	}
	lockPath := reload.LockPath(resolved.SIDDir, filepath.Base(socket), pane)
	if err := os.Mkdir(lockPath, 0o700); err != nil {
		t.Fatal(err)
	}
	renamer.captureErr = errors.New("capture unavailable")
	firstChanged := pass()
	if !firstChanged || renamer.captures != 0 || len(renamer.literals) != 0 || stderr.Len() != 0 {
		t.Fatalf(
			"first binding changed=%v captures=%d literals=%v warning=%q",
			firstChanged,
			renamer.captures,
			renamer.literals,
			stderr.String(),
		)
	}
	if err := os.Remove(lockPath); err != nil {
		t.Fatal(err)
	}
	if _, moved, err := manager.AdvanceCodexPane(ctx, socket, pane, oldID); err != nil || !moved {
		t.Fatalf("bind old thread: moved=%v err=%v", moved, err)
	}
	renamer.captureErr = nil
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := lock.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	assertDeferred := func(label, wantWarning string) {
		t.Helper()
		changed := pass()
		if changed {
			t.Fatalf("%s: reconcile reported changed", label)
		}
		bound, found, err := manager.CodexPaneBinding(ctx, socket, pane)
		if err != nil || !found || bound != oldID {
			t.Fatalf("%s: binding=(%q,%v,%v), want %s", label, bound, found, err, oldID)
		}
		if got := stderr.String(); got != wantWarning {
			t.Fatalf("%s: warning=%q, want %q", label, got, wantWarning)
		}
	}
	assertDeferred("reload held", "")
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	renamer.busy = true
	assertDeferred("busy turn", "")
	renamer.busy = false
	renamer.captureErr = errors.New("capture unavailable")
	assertDeferred(
		"capture failure",
		"pfm: tmux probe warning: codex pane "+socket+" "+pane+": capture pane before re-applying the chat name (binding retained for retry): capture unavailable\n",
	)
	renamer.captureErr = nil
	if err := os.Remove(lockPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(lockPath, 0o700); err != nil {
		t.Fatal(err)
	}
	assertDeferred(
		"lock probe failure",
		"pfm: tmux probe warning: codex pane "+socket+" "+pane+": probe reload lock (binding retained for retry): open reload lock: open "+lockPath+": is a directory\n",
	)
	if err := os.Remove(lockPath); err != nil {
		t.Fatal(err)
	}
	if !pass() {
		t.Fatalf("idle retry did not advance binding: warning=%q", stderr.String())
	}
	if got := renamer.literals; len(got) != 2 || got[0] != "/rename" || got[1] != "E2_MAIN" {
		t.Fatalf("idle retry literals=%v, want one /rename and E2_MAIN", got)
	}
	bound, found, err := manager.CodexPaneBinding(ctx, socket, pane)
	if err != nil || !found || bound != newID {
		t.Fatalf("idle retry binding=(%q,%v,%v), want %s", bound, found, err, newID)
	}
	killed, err := database.KilledChats(ctx)
	if err != nil || len(killed) != 1 || killed[0].ID != oldID {
		t.Fatalf("idle retry killed=%v err=%v, want old thread", killed, err)
	}
	const childID = "56565656-5656-4565-8565-565656565656"
	if err := database.UpsertRollout(ctx, store.Rollout{
		ID: childID, ParentThread: newID, UserThread: true, PromptCount: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, moved, err := manager.AdvanceCodexPane(ctx, socket, pane, childID); err != nil || !moved {
		t.Fatalf("bind same-lineage child: moved=%v err=%v", moved, err)
	}
	if err := os.Mkdir(lockPath, 0o700); err != nil {
		t.Fatal(err)
	}
	renamer.captureErr = errors.New("capture unavailable")
	beforeCaptures := renamer.captures
	beforeLiterals := len(renamer.literals)
	if !pass() || renamer.captures != beforeCaptures || len(renamer.literals) != beforeLiterals || stderr.Len() != 0 {
		t.Fatalf(
			"same-lineage rebind probed lock or capture: captures=%d literals=%v warning=%q",
			renamer.captures, renamer.literals, stderr.String(),
		)
	}
	bound, found, err = manager.CodexPaneBinding(ctx, socket, pane)
	if err != nil || !found || bound != newID {
		t.Fatalf("same-lineage binding=(%q,%v,%v), want %s", bound, found, err, newID)
	}
}

// startNoopCodexPane brings up a real tmux server on a scratch socket under
// tmuxTmpDir with one pane that paints statusLine and holds — the fixture
// TestReconcileCodexPanesRecordsARebind needs to give ObserveCodexPanes'
// real capture-pane read something to see. Pattern:
// cmd/pfm/reconcile_codex_panes_jail_test.go's startCodexStatusPane.
func startNoopCodexPane(t *testing.T, tmuxTmpDir, socket, statusLine string) {
	t.Helper()
	if err := os.MkdirAll(tmuxTmpDir, 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(
		"tmux", "-f", "/dev/null", "-L", socket,
		"new-session", "-d", "-s", socket, "-n", "codex",
		"printf '"+statusLine+"'; sleep 120",
	)
	command.Env = append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+tmuxTmpDir)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("start jailed codex pane %q: %v: %s", socket, err, output)
	}
	t.Cleanup(func() {
		killCommand := exec.Command("tmux", "-L", socket, "kill-server")
		killCommand.Env = append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+tmuxTmpDir)
		_ = killCommand.Run()
	})
	want := strings.TrimSpace(strings.TrimSuffix(statusLine, `\n`))
	deadline := time.Now().Add(5 * time.Second)
	for {
		capture := exec.Command("tmux", "-L", socket, "capture-pane", "-p", "-t", socket)
		capture.Env = append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+tmuxTmpDir)
		output, err := capture.Output()
		if err == nil && strings.Contains(string(output), want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("jailed codex pane %q never painted %q (fixture failure): last capture=%q err=%v",
				socket, want, output, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
