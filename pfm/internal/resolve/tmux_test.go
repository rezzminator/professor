package resolve

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
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
	script := "#!/bin/sh\ncase \"$*\" in\n*list-panes*) printf '1234 %%0\\n' ;;\n*) printf 'mysession 1234\\n' ;;\nesac\nexit 0\n"
	if err := testjail.WriteExecutable(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return binary
}

// TestWhoamiTmuxRecordsEveryInvocation proves CommandTmuxNamer.SessionPane and
// CommandPaneOwners.PaneOwners cross the observed tmux door (pfmtmux.Exec, not
// the bare pfmtmux.Command) — beside TestTmuxResolverRecordsEveryInvocation's
// sibling façade above.
func TestWhoamiTmuxRecordsEveryInvocation(t *testing.T) {
	ctx, recorder := obs.Test(t)
	binary := fakeWhoamiTmuxBinary(t)
	namer := CommandTmuxNamer{Binary: binary}
	name, pid, err := namer.SessionPane(ctx, "/sockets/cc-1", "%3")
	if err != nil || name != "mysession" || pid != 1234 {
		t.Fatalf("SessionPane = %q %d, %v", name, pid, err)
	}
	owners := CommandPaneOwners{Binary: binary}
	if _, err := owners.PaneOwners(ctx, "/sockets/cc-1"); err != nil {
		t.Fatal(err)
	}
	requireTmuxRecord(t, recorder, 0, "display-message", "%3")
	requireTmuxRecord(t, recorder, 1, "list-panes", "")
}

// tmuxPrinting plays a tmux whose every answer is output.
func tmuxPrinting(t *testing.T, output string) string {
	t.Helper()
	dir := t.TempDir()
	answer := filepath.Join(dir, "answer")
	if err := os.WriteFile(answer, []byte(output), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "tmux")
	if err := testjail.WriteExecutable(binary, []byte("#!/bin/sh\ncat '"+answer+"'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return binary
}

// One listing line tmux wrote in another shape costs that pane, never the
// server's other panes, and the loss is named.
func TestTmuxListingsKeepEveryParsedPane(t *testing.T) {
	t.Run("list-panes", func(t *testing.T) {
		ctx, recorder := obs.Test(t)
		binary := tmuxPrinting(t, "main\x1f%1\x1fclaude\x1fwin\nbroken-line\n")
		panes, err := TmuxResolver{Binary: binary}.ListPanes(ctx, "/sockets/cc-1")
		want := []ResolvedPane{{
			SocketPath: "/sockets/cc-1", SessionName: "main", PaneID: "%1", CurrentCommand: "claude", WindowName: "win",
		}}
		if err != nil || !reflect.DeepEqual(panes, want) {
			t.Fatalf("ListPanes() = %+v, %v; want %+v", panes, err, want)
		}
		var skipped []string
		for _, record := range recorder.Records() {
			if record.Message == "resolve.pane.skipped" {
				sock, _ := record.Field(obs.FieldSock)
				text, _ := record.Field(obs.FieldErr)
				skipped = append(skipped, record.Level+" "+sock.(string)+": "+text.(string))
			}
		}
		if want := []string{
			`WARN /sockets/cc-1: tmux returned 1 fields in "broken-line"`,
		}; !reflect.DeepEqual(
			skipped,
			want,
		) {
			t.Fatalf("skip records = %q, want %q", skipped, want)
		}
	})
	t.Run("pane owners", func(t *testing.T) {
		binary := tmuxPrinting(t, "1234 %0\nabc %1\n")
		owners, err := CommandPaneOwners{Binary: binary}.PaneOwners(context.Background(), "/sockets/cc-1")
		want := []PaneOwner{{SocketPath: "/sockets/cc-1", PaneID: "%0", PanePID: 1234}}
		if !reflect.DeepEqual(owners, want) {
			t.Fatalf("PaneOwners() = %+v, want %+v", owners, want)
		}
		if wantErr := `tmux returned 1 unparsable pane owner line(s), first "abc %1"`; err == nil ||
			err.Error() != wantErr {
			t.Fatalf("PaneOwners() error = %v, want %q", err, wantErr)
		}
	})
	// A stopped tmux server never answers its client: the lookup costs that
	// server alone, named, within the probe bound.
	t.Run("pane owners on a stopped server", func(t *testing.T) {
		binary := filepath.Join(t.TempDir(), "tmux")
		if err := testjail.WriteExecutable(binary, []byte("#!/bin/sh\nexec sleep 60\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		started := time.Now()
		owners, err := CommandPaneOwners{Binary: binary}.PaneOwners(context.Background(), "/sockets/cc-1")
		elapsed := time.Since(started)
		want := "tmux server did not answer within 2s: /sockets/cc-1"
		if owners != nil || err == nil || err.Error() != want || !errors.Is(err, gather.ErrServerUnresponsive) {
			t.Fatalf("PaneOwners() = %+v, %v; want nil, %q", owners, err, want)
		}
		if elapsed > 10*time.Second {
			t.Fatalf("PaneOwners() took %s on a stopped server, want the %s probe bound", elapsed, gather.ProbeTimeout)
		}
	})
}
