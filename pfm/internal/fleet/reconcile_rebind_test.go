package fleet

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/kill"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
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
