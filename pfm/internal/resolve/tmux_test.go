package resolve

import (
	"context"
	"os"
	"path/filepath"
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

// TestTmuxResolverRecordsEveryInvocation: the resolve façade terminates
// through the observed tmux command; a capture writes its shape, never
// the pane text.
func TestTmuxResolverRecordsEveryInvocation(t *testing.T) {
	ctx, recorder := obs.Test(t)
	tmux := TmuxResolver{Binary: fakeTmuxBinary(t)}
	if _, err := tmux.CapturePane(ctx, "/sockets/cc-1", "%2"); err != nil {
		t.Fatal(err)
	}
	if _, err := tmux.ListPanes(ctx, "/sockets/cc-1"); err != nil {
		t.Fatal(err)
	}
	requireTmuxRecord(t, recorder, 0, "capture-pane", "%2")
	requireTmuxRecord(t, recorder, 1, "list-panes", "")
	_ = context.Background
}

// fakeWhoamiTmuxBinary plays tmux for CommandTmuxNamer/CommandPaneOwners:
// a display-message answers a session name, a list-panes answers pane rows.
func fakeWhoamiTmuxBinary(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "tmux")
	script := "#!/bin/sh\ncase \"$*\" in\n*list-panes*) printf '1234 %%0\\n' ;;\n*) printf 'mysession\\n' ;;\nesac\nexit 0\n"
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return binary
}

// TestWhoamiTmuxRecordsEveryInvocation proves CommandTmuxNamer.SessionName and
// CommandPaneOwners.PaneOwners cross the observed tmux door (pfmtmux.Exec, not
// the bare pfmtmux.Command) — beside TestTmuxResolverRecordsEveryInvocation's
// sibling façade above.
func TestWhoamiTmuxRecordsEveryInvocation(t *testing.T) {
	ctx, recorder := obs.Test(t)
	binary := fakeWhoamiTmuxBinary(t)
	namer := CommandTmuxNamer{Binary: binary}
	name, err := namer.SessionName(ctx, "/sockets/cc-1", "%3")
	if err != nil || name != "mysession" {
		t.Fatalf("SessionName = %q, %v", name, err)
	}
	owners := CommandPaneOwners{Binary: binary}
	if _, err := owners.PaneOwners(ctx, "/sockets/cc-1"); err != nil {
		t.Fatal(err)
	}
	requireTmuxRecord(t, recorder, 0, "display-message", "%3")
	requireTmuxRecord(t, recorder, 1, "list-panes", "")
}
