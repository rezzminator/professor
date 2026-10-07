package inject

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// armedFixturePath derives the armed record's path from the steer log's the
// way the contract states it — beside the log, ".armed" for ".log" — so the
// tests pin the NAME a second chat would look for, not the helper that
// happens to build it.
func armedFixturePath(engine *Engine, socket string) string {
	target := Target{SocketPath: filepath.Join(string(filepath.Separator), "tmp", "tmux-jail", socket), Pane: "%1"}
	return strings.TrimSuffix(engine.steerLogPath(target), ".log") + ".armed"
}

func writeArmedFixture(t *testing.T, path string, pid int, steer string, stamp time.Time) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"pid":    pid,
		"socket": "/tmp/tmux-jail/cc-armed",
		"pane":   "%1",
		"steer":  steer,
		"stamp":  stamp.Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

// deadPID returns a pid no live process holds: a child that already ran to
// completion and was reaped. (A made-up large number could collide with a
// real process on a busy host; a reaped child's pid is provably free right
// now.)
func deadPID(t *testing.T) int {
	t.Helper()
	command := exec.Command("true")
	if err := command.Run(); err != nil {
		t.Fatalf("run a short-lived child: %v", err)
	}
	return command.Process.Pid
}

// waitOn is the ThenWait CommandThenSpawner hands the waiter for the jail
// socket newTestEngine resolves: the same (socket, pane) pair the engine
// derived the log — and so the armed record — from.
func waitOn(socket string, steers ...string) ThenWait {
	return ThenWait{
		SocketPath: filepath.Join(string(filepath.Separator), "tmp", "tmux-jail", socket),
		Target:     "%1",
		Steers:     steers,
	}
}

func quickThenOptions(engine *Engine) {
	engine.options.ThenMin = time.Nanosecond
	engine.options.ThenBusyTries = 1
	engine.options.ThenIdlePoll = time.Nanosecond
	engine.options.ThenIdleTries = 1
	engine.options.ThenIdleStable = 1
	engine.options.ThenSettle = time.Nanosecond
}

// TestDeliverThenClaimsTheArmedRecordAndReleasesItOnTheLastDelivery: the
// waiter names ITSELF on the record while it runs and removes it once the
// chain's last steer is delivered — the pane is free to arm again.
func TestDeliverThenClaimsTheArmedRecordAndReleasesItOnTheLastDelivery(t *testing.T) {
	fake := &fakeTmux{capture: captureIdle, submitOnEnter: true}
	engine := newTestEngine(t, "cc-armed-release", fake)
	quickThenOptions(engine)
	path := armedFixturePath(engine, "cc-armed-release")
	writeArmedFixture(t, path, 0, "resume", time.Now())

	result, err := engine.DeliverThen(context.Background(), waitOn("cc-armed-release", "resume"))
	if err != nil || result.Code != 0 {
		t.Fatalf("DeliverThen() = %+v, %v", result, err)
	}
	if _, exists, err := readArmedRecord(path); err != nil || exists {
		t.Fatalf("armed record after the last delivery: exists=%t err=%v — the pane stays armed forever", exists, err)
	}
}

// TestDeliverThenHandsTheArmedRecordToTheNextHop: a hop that delivered steer
// N and armed steer N+1 gives the record back UNCLAIMED (pid 0, fresh stamp)
// for the next hop to claim — the chain stays one arming end to end, and a
// second schedule during the hand-off still reads it as armed.
func TestDeliverThenHandsTheArmedRecordToTheNextHop(t *testing.T) {
	fake := &fakeTmux{capture: captureIdle, submitOnEnter: true}
	spawner := &fakeSpawner{}
	engine := newTestEngineWith(t, "cc-armed-hop2", fake, spawner)
	quickThenOptions(engine)
	path := armedFixturePath(engine, "cc-armed-hop2")
	writeArmedFixture(t, path, 0, "resume", time.Now().Add(-time.Hour))

	result, err := engine.DeliverThen(context.Background(), waitOn("cc-armed-hop2", "resume", "then report"))
	if err != nil || result.Code != 0 || result.Steers != 1 {
		t.Fatalf("DeliverThen() = %+v, %v — want the first steer delivered and one more armed", result, err)
	}
	record, exists, err := readArmedRecord(path)
	if err != nil || !exists {
		t.Fatalf("armed record after a hand-off: exists=%t err=%v", exists, err)
	}
	if record.PID != 0 || record.Steer != "resume" || !record.alive(time.Now()) {
		t.Fatalf("hand-off record = %+v, want unclaimed, still naming the chain's first steer, and alive", record)
	}
	if calls := spawner.spawned(); len(calls) != 1 || !calls[0].Append {
		t.Fatalf("next hop spawn = %+v, want one appending spawn", calls)
	}
}

// TestDeliverThenReleasesTheArmedRecordOnARefusal: a chain that aborts (a
// human kept typing) leaves no arming behind — the operator's next request
// must not be refused in the name of a waiter that already gave up.
func TestDeliverThenReleasesTheArmedRecordOnARefusal(t *testing.T) {
	fake := &fakeTmux{capture: captureIdle, clientAttached: true}
	engine := newTestEngine(t, "cc-armed-typing", fake)
	quickThenOptions(engine)
	engine.options.TypistQuiet = time.Hour
	engine.options.Clock = fixedClock{Clock: engine.options.Clock, now: time.Unix(1_000_000, 0)}
	fake.clientActivity = time.Unix(1_000_000, 0)
	path := armedFixturePath(engine, "cc-armed-typing")

	result, err := engine.DeliverThen(context.Background(), waitOn("cc-armed-typing", "resume"))
	if err != nil || result.Code != CodeBusy {
		t.Fatalf("DeliverThen() = %+v, %v — want the typing refusal", result, err)
	}
	if _, exists, err := readArmedRecord(path); err != nil || exists {
		t.Fatalf("armed record after a refusal: exists=%t err=%v", exists, err)
	}
}

// TestArmRecordLeavesALiveArmingAlone: a chained spawn over an armed pane
// keeps the first waiter's record intact.
func TestArmRecordLeavesALiveArmingAlone(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "chat-then-x.log")
	path := armedPathFor(logPath)
	writeArmedFixture(t, path, os.Getpid(), "/compact the first arming", time.Now())

	request := SteerSpawn{
		SocketPath: "/tmp/tmux-jail/cc-armed",
		Target:     "%1",
		Steers:     []string{"resume"},
		LogPath:    logPath,
	}
	if err := armRecord(request, time.Now()); err != nil {
		t.Fatal(err)
	}
	record, _, err := readArmedRecord(path)
	if err != nil || record.PID != os.Getpid() || record.Steer != "/compact the first arming" {
		t.Fatalf("record after a second arm = %+v (%v), want the live first arming untouched", record, err)
	}

	writeArmedFixture(t, path, deadPID(t), "/compact a dead arming", time.Now().Add(-time.Hour))
	if err := armRecord(request, time.Now()); err != nil {
		t.Fatal(err)
	}
	record, _, err = readArmedRecord(path)
	if err != nil || record.PID != 0 || record.Steer != "resume" {
		t.Fatalf("record after arming over a dead one = %+v (%v), want the new unclaimed arming", record, err)
	}
}

// TestClaimArmedLeavesAnotherLiveWaitersRecord: a waiter that finds the
// record held by another live pid does not take it over.
func TestClaimArmedLeavesAnotherLiveWaitersRecord(t *testing.T) {
	engine := newTestEngine(t, "cc-armed-foreign", &fakeTmux{capture: captureIdle})
	var warnings bytes.Buffer
	engine.warningWriter = &warnings
	path := filepath.Join(t.TempDir(), "chat-then-y.armed")
	// os.Getpid() stands in for "another live waiter": alive, and the code
	// under test compares against its own pid — so a child that is provably
	// alive and not us is needed. `sleep` is that child.
	child := exec.Command("sleep", "30")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	writeArmedFixture(t, path, child.Process.Pid, "/compact theirs", time.Now())

	if err := engine.claimArmed(path, "resume"); err != nil {
		t.Fatalf("claimArmed() over a readable record held by another live waiter = %v, want no error", err)
	}

	record, _, err := readArmedRecord(path)
	if err != nil || record.PID != child.Process.Pid || record.Steer != "/compact theirs" {
		t.Fatalf(
			"record after a foreign claim attempt = %+v (%v), want the other waiter's record untouched",
			record,
			err,
		)
	}
	if !strings.Contains(warnings.String(), "held by pid "+strconv.Itoa(child.Process.Pid)) {
		t.Fatalf("the refused claim was not logged: %q", warnings.String())
	}
}

// TestDeliverThenRefusesToClaimAnUnreadableArmedRecord pins F4: claimArmed
// warned about a record it could not decode and then stamped its own pid onto
// the zero value, erasing the identity of an arming it never read — the
// second waiter the record exists to prevent, and a third schedule would then
// be refused by name against the wrong one. The waiter now stops, names the
// path and the read error on its visible result, and leaves the record byte
// for byte as it found it.
func TestDeliverThenRefusesToClaimAnUnreadableArmedRecord(t *testing.T) {
	fake := &fakeTmux{capture: captureIdle, submitOnEnter: true}
	spawner := &fakeSpawner{}
	engine := newTestEngineWith(t, "cc-armed", fake, spawner)
	path := armedFixturePath(engine, "cc-armed")
	torn := []byte(`{"pid":4242,"socket":"/tmp/tmux-jail/cc-armed","pane":"%1","ste`)
	if err := os.WriteFile(path, torn, 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := engine.DeliverThen(context.Background(), ThenWait{
		SocketPath: filepath.Join(string(filepath.Separator), "tmp", "tmux-jail", "cc-armed"),
		Target:     "%1",
		Steers:     []string{"resume the wave"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != CodeUndelivered || result.Status != statusUndelivered {
		t.Fatalf(
			"DeliverThen() = %+v over an armed record that could not be read; want a Code %d undelivered result — an unreadable record is never absence",
			result,
			CodeUndelivered,
		)
	}
	if !strings.Contains(result.Message, path) ||
		!strings.Contains(result.Message, "could not read the armed steer record") {
		t.Fatalf("undelivered message %q names neither the record path nor the read failure", result.Message)
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(got, torn) {
		t.Fatalf("the unreadable armed record was rewritten: %q, want it untouched (%q)", got, torn)
	}
	if len(fake.keys) != 0 || len(fake.literals) != 0 {
		t.Fatalf("typed into the pane after refusing to claim: keys=%q literals=%q", fake.keys, fake.literals)
	}
}
