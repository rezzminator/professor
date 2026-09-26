package spawn

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// fakeTmuxBinary plays tmux: exits 0 and prints nothing.
func fakeTmuxBinary(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "tmux")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
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
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
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
// through the observed tmux command — SendKey and Capture each one record.
func TestTmuxSpawnerRecordsEveryInvocation(t *testing.T) {
	ctx, recorder := obs.Test(t)
	tmux := TmuxSpawner{Binary: fakeTmuxBinary(t), TmuxDir: t.TempDir()}
	if err := tmux.SendKey(ctx, "cc-1-2-3", "%1", "Enter"); err != nil {
		t.Fatal(err)
	}
	if _, err := tmux.Capture(ctx, "cc-1-2-3", "%1"); err != nil {
		t.Fatal(err)
	}
	requireTmuxRecord(t, recorder, 0, "send-keys", "%1")
	requireTmuxRecord(t, recorder, 1, "capture-pane", "%1")
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
	if strings.Contains(err.Error(), sentinel) {
		t.Fatalf("error leaked spec.Run's prompt body: %v", err)
	}
	if !strings.Contains(err.Error(), "claude argc=4") {
		t.Fatalf("error = %v, want it to name the argv shape (claude argc=4)", err)
	}
}
