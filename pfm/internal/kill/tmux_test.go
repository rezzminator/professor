package kill

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

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

// TestTmuxKillerRecordsEveryInvocation: the kill façade terminates through
// the observed tmux command — one record per call.
func TestTmuxKillerRecordsEveryInvocation(t *testing.T) {
	ctx, recorder := obs.Test(t)
	tmux := TmuxKiller{Binary: fakeTmuxBinary(t)}
	if err := tmux.KillPane(ctx, "/sockets/cc-1", "%3"); err != nil {
		t.Fatal(err)
	}
	if err := tmux.KillServer(ctx, "/sockets/cc-1"); err != nil {
		t.Fatal(err)
	}
	requireTmuxRecord(t, recorder, 0, "kill-pane", "%3")
	requireTmuxRecord(t, recorder, 1, "kill-server", "")
	if len(recorder.Records()) != 2 {
		t.Fatalf("records = %d, want 2: %s", len(recorder.Records()), recorder.Raw())
	}
	_ = context.Background
}

// TestTmuxKillerCaptureReadsOnlyTheScreen: the finisher parses Capture and
// CaptureStyled as the pane's screen (footer, composer, exit dialog), so a
// warning tmux writes to stderr must never join that text; a failed capture
// still names tmux's own complaint.
func TestTmuxKillerCaptureReadsOnlyTheScreen(t *testing.T) {
	directory := t.TempDir()
	warns := filepath.Join(directory, "tmux-warns")
	script := "#!/bin/sh\nprintf '\\342\\235\\257 \\n'\necho 'tmux: terminal warning' >&2\nexit 0\n"
	if err := testjail.WriteExecutable(warns, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	fails := filepath.Join(directory, "tmux-fails")
	failing := []byte("#!/bin/sh\necho \"can't find pane: %4\" >&2\nexit 1\n")
	if err := testjail.WriteExecutable(fails, failing, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for name, capture := range map[string]func(TmuxKiller) (string, error){
		"plain":  func(tmux TmuxKiller) (string, error) { return tmux.Capture(ctx, "/sockets/cc-1", "%4") },
		"styled": func(tmux TmuxKiller) (string, error) { return tmux.CaptureStyled(ctx, "/sockets/cc-1", "%4") },
	} {
		screen, err := capture(TmuxKiller{Binary: warns})
		if err != nil || screen != "\u276f \n" {
			t.Fatalf("%s capture = %q, %v; want only the screen %q", name, screen, err, "\u276f \n")
		}
		_, err = capture(TmuxKiller{Binary: fails})
		if err == nil || !strings.Contains(err.Error(), "can't find pane: %4") {
			t.Fatalf("%s capture of a failing tmux = %v, want tmux's own complaint", name, err)
		}
	}
}
