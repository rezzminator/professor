package mockengine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
)

func TestClaudeHoldStaysBusyUntilTheGateFileIsGone(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	gate := filepath.Join(fix.root, "gate")
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	fix.write(Scenario{SessionID: fixtureSession, BusyMS: intPtr(0), Steps: []Step{
		{Type: StepHold, UntilGone: gate},
		{Type: StepTurn, Reply: "released"},
	}})
	session := fix.startTUI("claude", claudeArgs(), nil)
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	session.typeLine("wait for me")
	session.waitFrame("busy while the gate exists", pfmcall.IsBusy)
	fix.nudge()
	fix.nudge() // the look the first nudge handed is done: it found the gate
	if frame := session.out.frame(); !pfmcall.IsBusy(frame) || strings.Contains(frame, "released") {
		t.Fatalf("the hold released before the gate went:\n%s", frame)
	}
	if err := os.Remove(gate); err != nil {
		t.Fatal(err)
	}
	fix.nudge()
	session.waitFrame("the reply once the gate is gone", func(frame string) bool {
		return !pfmcall.IsBusy(frame) && strings.Contains(frame, "released")
	})
}

// TestClaudeHoldAbortsOnANonAbsenceStatError covers F3: a stat failure that
// is not the gate's confirmed absence (here ENOTDIR, a component of the path
// is a regular file) must abort the hold with a named failure, never release
// it the way a genuine removal does.
func TestClaudeHoldAbortsOnANonAbsenceStatError(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	notADir := filepath.Join(fix.root, "not-a-dir")
	if err := os.WriteFile(notADir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	gate := filepath.Join(notADir, "gate")
	fix.write(Scenario{SessionID: fixtureSession, BusyMS: intPtr(0), Steps: []Step{
		{Type: StepHold, UntilGone: gate},
		{Type: StepTurn, Reply: "unreachable"},
	}})
	session := fix.startTUI("claude", claudeArgs(), nil)
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	session.typeLine("wait for me")
	if code := session.waitExit(); code != ExitUnpinned {
		t.Fatalf("exit = %d, want %d (a non-absence stat error aborts the hold)", code, ExitUnpinned)
	}
	if !strings.Contains(session.stderr.String(), "hold") {
		t.Fatalf("stderr = %q, want it to name the failed hold", session.stderr.String())
	}
}
