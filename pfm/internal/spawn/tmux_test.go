package spawn

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// fakeTmuxBinary plays tmux: exits 0 and prints nothing.
func fakeTmuxBinary(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "tmux")
	if err := testjail.WriteExecutable(binary, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return binary
}

// fakeTmuxBinaryConfigureFails plays tmux: `new-session` succeeds, every
// other subcommand (the post-birth configure calls) fails, so NewSession
// reaches its "configure chat server" error branch.
func fakeTmuxBinaryConfigureFails(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "tmux")
	script := `#!/bin/sh
for a in "$@"; do
  if [ "$a" = "new-session" ]; then
    exit 0
  fi
done
echo "server gone" >&2
exit 1
`
	if err := testjail.WriteExecutable(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return binary
}

func fakeTmuxBinaryCreateFails(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "tmux")
	if err := testjail.WriteExecutable(
		binary,
		[]byte("#!/bin/sh\necho create failed >&2\nexit 1\n"),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	return binary
}

func fakeTmuxBinaryBlocks(t *testing.T, block, marker string) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "tmux")
	script := fmt.Sprintf(`#!/bin/sh
for a in "$@"; do
  if [ "$a" = "new-session" ]; then
    if [ %q = "new-session" ]; then
      : > %q
      exec sleep 30
    fi
    exit 0
  fi
done
if [ %q = "configure" ]; then
  : > %q
  exec sleep 30
fi
exit 0
`, block, marker, block, marker)
	if err := testjail.WriteExecutable(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return binary
}

// requireTmuxRecord asserts the one comp=tmux record a façade call writes:
// subcmd, target when given, exit 0 and dur_ms.
func requireTmuxRecord(t *testing.T, recorder *obs.Recorder, index int, subcmd, target string) {
	t.Helper()
	records := recorder.Records()
	if len(records) <= index {
		t.Fatalf("records = %d, want at least %d: %s", len(records), index+1, recorder.Raw())
	}
	record := records[index]
	if record.Message != "tmux.exec" {
		t.Fatalf("record %d = %s, want tmux.exec", index, record.Message)
	}
	if comp, _ := record.Field(obs.FieldComp); comp != "tmux" {
		t.Fatalf("comp = %v, want tmux", comp)
	}
	if got, _ := record.Field("subcmd"); got != subcmd {
		t.Fatalf("subcmd = %v, want %s", got, subcmd)
	}
	if got, _ := record.Field("target"); target != "" && got != target {
		t.Fatalf("target = %v, want %s", got, target)
	}
	if exit, _ := record.Field(obs.FieldExit); exit != float64(0) {
		t.Fatalf("exit = %v, want 0", exit)
	}
	if _, found := record.Field(obs.FieldDur); !found {
		t.Fatalf("no dur_ms: %v", record.Fields)
	}
}

// TestTmuxSpawnerRecordsEveryInvocation: the spawn façade terminates
// through the observed tmux command — SendKey and Capture each one record,
// SendPaste two (its buffer load, then the paste into the pane).
func TestTmuxSpawnerRecordsEveryInvocation(t *testing.T) {
	ctx, recorder := obs.Test(t)
	tmux := TmuxSpawner{Binary: fakeTmuxBinary(t), TmuxDir: t.TempDir()}
	if err := tmux.SendKey(ctx, "cc-1-2-3", "%1", "Enter"); err != nil {
		t.Fatal(err)
	}
	if _, err := tmux.Capture(ctx, "cc-1-2-3", "%1"); err != nil {
		t.Fatal(err)
	}
	if err := tmux.SendPaste(ctx, "cc-1-2-3", "%1", "line one\nline two"); err != nil {
		t.Fatal(err)
	}
	requireTmuxRecord(t, recorder, 0, "send-keys", "%1")
	requireTmuxRecord(t, recorder, 1, "capture-pane", "%1")
	requireTmuxRecord(t, recorder, 2, "load-buffer", "")
	requireTmuxRecord(t, recorder, 3, "paste-buffer", "%1")
	_ = context.Background
}

// TestNewSessionConfigureErrorOmitsTheCommandLine: a "configure chat server"
// failure names the pane command's shape (binary + word count), never
// spec.Run itself — spec.Run can carry a prompt body (action.HeadlessRun
// appends request.Prompt to the launch line), and this error text reaches
// obs under FieldErr.
func TestNewSessionConfigureErrorOmitsTheCommandLine(t *testing.T) {
	sentinel := "SENTINEL-PROMPT-BODY-DO-NOT-LOG"
	dir := t.TempDir()
	tmux := TmuxSpawner{Binary: fakeTmuxBinaryConfigureFails(t), TmuxDir: dir}
	err := tmux.NewSession(context.Background(), SessionSpec{
		Socket:  "configure-fails",
		Session: "configure-fails",
		Window:  "w",
		CWD:     dir,
		Run:     "claude --resume abc " + sentinel,
		Width:   80,
		Height:  24,
	})
	if err == nil {
		t.Fatal("NewSession accepted a server whose configure step failed")
	}
	var partial *SessionCreatedError
	if !errors.As(err, &partial) {
		t.Fatalf("configured server lost creation ownership: %v", err)
	}
	if strings.Contains(err.Error(), sentinel) {
		t.Fatalf("error leaked spec.Run's prompt body: %v", err)
	}
	if !strings.Contains(err.Error(), "claude argc=4") {
		t.Fatalf("error = %v, want it to name the argv shape (claude argc=4)", err)
	}
	if !strings.Contains(err.Error(), "the server died before it could be configured") {
		t.Fatalf("error = %v, want the live-context server death message", err)
	}
}

func TestNewSessionCreateFailurePreservesOutput(t *testing.T) {
	dir := t.TempDir()
	tmux := TmuxSpawner{Binary: fakeTmuxBinaryCreateFails(t), TmuxDir: dir}
	err := tmux.NewSession(context.Background(), SessionSpec{
		Socket:  "create-fails",
		Session: "create-fails",
		Window:  "w",
		CWD:     dir,
		Run:     "echo ready",
	})
	if err == nil || err.Error() != "create chat server: exit status 1: create failed\n" {
		t.Fatalf("NewSession error = %v, want original create error with output", err)
	}
}

func newSessionCancelledWhileBlocked(t *testing.T, block string) error {
	t.Helper()
	dir := t.TempDir()
	marker := filepath.Join(dir, "blocking")
	tmux := TmuxSpawner{Binary: fakeTmuxBinaryBlocks(t, block, marker), TmuxDir: dir}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- tmux.NewSession(ctx, SessionSpec{
			Socket:  "cancelled",
			Session: "cancelled",
			Window:  "w",
			CWD:     dir,
			Run:     "echo ready",
			Width:   80,
			Height:  24,
		})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat blocking marker: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("tmux command did not reach blocking arm")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("NewSession did not return after cancellation")
		return nil
	}
}

func TestNewSessionReturnsCancellationDuringConfigure(t *testing.T) {
	err := newSessionCancelledWhileBlocked(t, "configure")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("NewSession error = %v, want context cancellation", err)
	}
	if !strings.HasPrefix(err.Error(), "configure chat server:") || strings.Contains(err.Error(), "the server died") {
		t.Fatalf("NewSession error = %v, want configure cancellation without server death", err)
	}
}

func TestNewSessionReturnsCancellationDuringCreate(t *testing.T) {
	err := newSessionCancelledWhileBlocked(t, "new-session")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("NewSession error = %v, want context cancellation", err)
	}
	if !strings.HasPrefix(err.Error(), "create chat server:") {
		t.Fatalf("NewSession error = %v, want create cancellation", err)
	}
}

func TestNewSessionFailedLaunchKeepsOnlyUnresolvedClaims(t *testing.T) {
	for _, scenario := range []string{"create cleanup", "create unresolved", "query cleanup", "query unresolved"} {
		t.Run(scenario, func(t *testing.T) {
			account, dir := t.TempDir(), t.TempDir()
			createStatus, killStatus := "0", "0"
			if strings.HasPrefix(scenario, "create") {
				createStatus = "1"
			}
			if strings.HasSuffix(scenario, "unresolved") {
				killStatus = "1"
			}
			binary := filepath.Join(dir, "tmux")
			script := "#!/bin/sh\nfor a in \"$@\"; do\n case \"$a\" in\n new-session) exit " + createStatus + ";;\n display-message) echo invalid-pid; exit 0;;\n kill-server) exit " + killStatus + ";;\n esac\ndone\nexit 0\n"
			if err := testjail.WriteExecutable(binary, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			err := (TmuxSpawner{Binary: binary, TmuxDir: dir}).NewSession(context.Background(), SessionSpec{
				Socket: "failed", Session: "failed", Window: "w", CWD: dir,
				Run: "env CLAUDE_CONFIG_DIR=" + account + " claude --resume example",
			})
			if err == nil {
				t.Fatal("failed launch reported success")
			}
			var partial *SessionCreatedError
			if errors.As(err, &partial) != strings.HasSuffix(scenario, "unresolved") {
				t.Fatalf("creation ownership marker does not match cleanup proof: %v", err)
			}
			guard, err := gather.AcquireAccountGuard(account, false)
			if err != nil {
				t.Fatalf("failed launch held lock: %v", err)
			}
			defer func() {
				if err := guard.Close(); err != nil {
					t.Error(err)
				}
			}()
			live, claimErr := guard.Active(gather.NewProcFS(""))
			if strings.HasSuffix(scenario, "unresolved") {
				if claimErr == nil {
					t.Fatal("unproven child termination lost its claim")
				}
			} else if claimErr != nil || len(live) != 0 {
				t.Fatalf("terminated launch claim remained: %v %v", live, claimErr)
			}
		})
	}
}
