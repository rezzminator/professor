package reap

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

// TestTmuxReaperRecordsEveryInvocation: the reap façade terminates through
// the observed tmux command — one record per call, the session as target.
func TestTmuxReaperRecordsEveryInvocation(t *testing.T) {
	ctx, recorder := obs.Test(t)
	tmux := TmuxReaper{Binary: fakeTmuxBinary(t), TmuxDir: t.TempDir()}
	if err := tmux.KillSession(ctx, "cc-1-2-3", "main"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := tmux.ClientIdle(ctx, "cc-1-2-3"); err != nil {
		t.Fatal(err)
	}
	requireTmuxRecord(t, recorder, 0, "kill-session", "=main")
	requireTmuxRecord(t, recorder, 1, "list-clients", "")
	_ = context.Background
}

// TestTmuxReaperSessionsNoServerIsAbsence: tmux itself ran and answered "no
// server on this socket" (an ordinary exit failure) — the common, expected
// state of a bunker socket that was never opened. That reads as absence, not
// a sweep failure.
func TestTmuxReaperSessionsNoServerIsAbsence(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "tmux")
	if err := os.WriteFile(
		binary,
		[]byte("#!/bin/sh\necho 'no server running on vsct' >&2\nexit 1\n"),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	tmux := TmuxReaper{Binary: binary, TmuxDir: t.TempDir()}
	sessions, err := tmux.Sessions(context.Background(), "vsct")
	if err != nil {
		t.Fatalf("Sessions() = %v, want nil error for an unopened bunker", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("sessions = %v, want none", sessions)
	}
}

// TestTmuxReaperSessionsCouldNotRunIsAnError (L1-F7): a probe that could not
// even run tmux — the binary itself absent — must not collapse into "no
// bunker sessions". The runner's abort path (runner.go's "list bunker
// sessions" wrap) depends on this error reaching it.
func TestTmuxReaperSessionsCouldNotRunIsAnError(t *testing.T) {
	tmux := TmuxReaper{
		Binary:  filepath.Join(t.TempDir(), "tmux-does-not-exist"),
		TmuxDir: t.TempDir(),
	}
	sessions, err := tmux.Sessions(context.Background(), "vsct")
	if err == nil {
		t.Fatal("Sessions() succeeded with a tmux binary that cannot run")
	}
	if sessions != nil {
		t.Fatalf("sessions = %v, want nil alongside the error", sessions)
	}
}
