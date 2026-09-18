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

func armedRequest() Request {
	return Request{
		Target:  "chat",
		Message: "/compact hold the wave state",
		Then:    []string{"resume the wave"},
	}
}

// TestScheduleRefusesASecondArmingWhileTheWaiterLives is Wave 8 item 5's
// second-request rule: one post-command steer is armed on this pane and its
// waiter is alive, so a second request is refused BY NAME — pid and steer —
// instead of spawning a second waiter whose O_TRUNC wipes the first one's
// log while both race to type into the same pane.
func TestScheduleRefusesASecondArmingWhileTheWaiterLives(t *testing.T) {
	spawner := &fakeSpawner{}
	engine := newTestEngineWith(t, "cc-armed", &fakeTmux{capture: captureIdle}, spawner)

	first, err := engine.ScheduleAfterCurrentTurn(context.Background(), armedRequest())
	if err != nil {
		t.Fatal(err)
	}
	if first.Code != 0 {
		t.Fatalf("first schedule = %+v, want scheduled", first)
	}
	// The real spawner writes this record; fakeSpawner records the spawn
	// only, so the fixture stands in for the waiter it would have started.
	writeArmedFixture(t, armedFixturePath(engine, "cc-armed"), os.Getpid(), "/compact hold the wave state", time.Now())

	second, err := engine.ScheduleAfterCurrentTurn(context.Background(), armedRequest())
	if err != nil {
		t.Fatal(err)
	}
	if second.Code != CodeBusy || second.Status != "refused" {
		t.Fatalf("second schedule = %+v, want a CodeBusy refusal while the first waiter lives", second)
	}
	for _, want := range []string{
		"already armed",
		"pid " + strconv.Itoa(os.Getpid()),
		`"/compact hold the wave state"`,
	} {
		if !strings.Contains(second.Message, want) {
			t.Fatalf("refusal %q does not name %q", second.Message, want)
		}
	}
	if calls := spawner.spawned(); len(calls) != 1 {
		t.Fatalf("spawned %d waiters, want exactly the first one: %+v", len(calls), calls)
	}
}

// TestScheduleReplacesAStaleArmedRecord: a record whose waiter is dead (a
// crashed or killed pfm internal then) must not block the pane forever — it
// is replaced, and the replacement is announced on the warning stream so
// the stale arming is visible rather than silently forgotten.
func TestScheduleReplacesAStaleArmedRecord(t *testing.T) {
	spawner := &fakeSpawner{}
	engine := newTestEngineWith(t, "cc-armed-stale", &fakeTmux{capture: captureIdle}, spawner)
	var warnings bytes.Buffer
	engine.warningWriter = &warnings
	path := armedFixturePath(engine, "cc-armed-stale")
	writeArmedFixture(t, path, deadPID(t), "/compact an earlier wave", time.Now().Add(-time.Hour))

	result, err := engine.ScheduleAfterCurrentTurn(context.Background(), armedRequest())
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 0 {
		t.Fatalf("schedule over a stale record = %+v, want scheduled", result)
	}
	if len(spawner.spawned()) != 1 {
		t.Fatalf("stale record blocked the spawn: %+v", spawner.spawned())
	}
	if !strings.Contains(warnings.String(), "stale") || !strings.Contains(warnings.String(), path) {
		t.Fatalf(
			"replacing a stale armed record printed %q, want a note naming it stale and its path",
			warnings.String(),
		)
	}
}

// TestScheduleChainHopIsNeverRefusedByTheArmedRecord: a chain's follow-ups
// are the SAME arming. The hop that delivers steer N and arms steer N+1 runs
// while the record it belongs to is live, and must never be refused by it.
func TestScheduleChainHopIsNeverRefusedByTheArmedRecord(t *testing.T) {
	spawner := &fakeSpawner{}
	engine := newTestEngineWith(t, "cc-armed-hop", &fakeTmux{capture: captureIdle}, spawner)
	writeArmedFixture(t, armedFixturePath(engine, "cc-armed-hop"), os.Getpid(), "/compact first", time.Now())

	request := armedRequest()
	request.Chain = true
	result, err := engine.ScheduleAfterCurrentTurn(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 0 {
		t.Fatalf("chain hop = %+v, want scheduled despite the live armed record it belongs to", result)
	}
	if calls := spawner.spawned(); len(calls) != 1 || !calls[0].Append {
		t.Fatalf("chain hop spawn = %+v, want one appending spawn", calls)
	}
}

// TestScheduleRefusesWhenTheArmedRecordCannotBeRead: an unreadable record is
// never "no record". Absence says nothing is armed; a read error says we
// failed to look — arming over it could be the second waiter this rule
// exists to prevent, so the schedule refuses with the error itself.
func TestScheduleRefusesWhenTheArmedRecordCannotBeRead(t *testing.T) {
	spawner := &fakeSpawner{}
	engine := newTestEngineWith(t, "cc-armed-dir", &fakeTmux{capture: captureIdle}, spawner)
	path := armedFixturePath(engine, "cc-armed-dir")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}

	result, err := engine.ScheduleAfterCurrentTurn(context.Background(), armedRequest())
	if err != nil {
		t.Fatal(err)
	}
	if result.Code == 0 {
		t.Fatalf("schedule over an unreadable armed record = %+v, want a refusal", result)
	}
	if !strings.Contains(result.Message, path) || !strings.Contains(result.Message, "is a directory") {
		t.Fatalf("refusal %q does not carry the read error and the record path", result.Message)
	}
	if len(spawner.spawned()) != 0 {
		t.Fatalf("an unreadable record still let a waiter spawn: %+v", spawner.spawned())
	}
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

// TestArmRecordLeavesALiveArmingAlone: inject()'s own `--then` spawn is not
// gated by refuseIfArmed, so a plain inject over an armed compaction must
// not rewrite the compaction's record as its own — the record keeps naming
// the arming the refusal reports.
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

	engine.claimArmed(path, "resume")

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
