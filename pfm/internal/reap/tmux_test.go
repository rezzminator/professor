package reap

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	pfmtmux "github.com/rezzminator/professor/pfm/internal/tmux"
)

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
	ctx = pfmtmux.WithClientRunner(ctx, testjail.TmuxRunner(deps.RunResult{}))
	tmux := TmuxReaper{Binary: "tmux", TmuxDir: t.TempDir()}
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
	fake := testjail.TmuxRunner(deps.RunResult{Stderr: []byte("no server running on vsct\n"), ExitCode: 1})
	tmux := TmuxReaper{TmuxDir: t.TempDir()}
	sessions, err := tmux.Sessions(pfmtmux.WithClientRunner(context.Background(), fake), "vsct")
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

// scriptedTmux is a context whose tmux answers every call with stdout and
// exit status, "no server running" on stderr.
func scriptedTmux(stdout string, exit int) context.Context {
	return pfmtmux.WithClientRunner(context.Background(), testjail.TmuxRunner(deps.RunResult{
		Stdout: []byte(stdout), Stderr: []byte("no server running\n"), ExitCode: exit,
	}))
}

// One session line tmux answers in an unreadable shape costs only that line: it is
// kept, named, with its idle unknown, and the bunker's other sessions list normally.
func TestTmuxReaperSessionsKeepsAnUnreadableLineAsUnknown(t *testing.T) {
	const sep = "\x1f"
	now := time.Unix(10_000, 0)
	output := "good" + sep + "1" + sep + "9000\n" +
		"short" + sep + "0\n" +
		"stamp" + sep + "0" + sep + "soon\n"
	tmux := TmuxReaper{Binary: "tmux", TmuxDir: t.TempDir(), Now: func() time.Time { return now }}
	got, err := tmux.Sessions(scriptedTmux(output, 0), "vsct")
	if err != nil {
		t.Fatalf("Sessions() = %v, want the readable lines listed", err)
	}
	want := []VSCTSession{
		{Name: "good", Attached: true, Idle: 1000 * time.Second},
		{Name: "short" + sep + "0", Unreadable: `2 session fields in "short\x1f0" on vsct`},
		{
			Name:       "stamp",
			Unreadable: `session activity "soon" on vsct: strconv.ParseInt: parsing "soon": invalid syntax`,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Sessions() = %#v, want %#v", got, want)
	}
	for _, session := range got[1:] {
		decision := planVSCT(Input{Apply: true, VSCTMaxIdle: time.Minute}, session)
		if decision.State != StateUnknown || decision.Action != ActionNone {
			t.Fatalf("an unreadable session %q planned %+v, want unknown and never killed", session.Name, decision)
		}
	}
}

// A list-clients that fails is the probe's own error, never "no client answered".
func TestTmuxReaperClientIdleReturnsAFailedProbe(t *testing.T) {
	tmux := TmuxReaper{Binary: "tmux", TmuxDir: t.TempDir()}
	_, found, err := tmux.ClientIdle(scriptedTmux("", 1), "cc-1-2-3")
	if found || err == nil || err.Error() != "tmux cc-1-2-3 list-clients: exit status 1" {
		t.Fatalf("ClientIdle() = found %t, err %v; want the exit failure returned", found, err)
	}
}
