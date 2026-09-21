package inject

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/resolve"
)

func TestDeliverThenDoesNotRecordExcludedHandoffEdge(t *testing.T) {
	fake := &fakeTmux{capture: "conversation\n❯ ", submitOnEnter: true}
	engine := newTestEngine(t, "cc-1-2-3", fake)
	engine.options.ThenMin = time.Nanosecond
	engine.options.ThenBusyTries = 1
	engine.options.ThenIdlePoll = time.Nanosecond
	engine.options.ThenIdleTries = 1
	engine.options.ThenIdleStable = 1
	engine.options.ThenSettle = time.Nanosecond
	recorded := 0
	engine.recorder = func(context.Context, fleetdb.CommsEvent) error {
		recorded++
		return nil
	}

	result, err := engine.DeliverThen(context.Background(), ThenWait{Target: "chat", Steers: []string{"resume"}})
	if err != nil || !result.Typed || result.Code != 0 {
		t.Fatalf("DeliverThen() = %+v, %v", result, err)
	}
	if recorded != 0 {
		t.Fatalf("reload --then delivery recorded %d excluded edge(s)", recorded)
	}
}

// TestCompactFocusRuleRefusesBeforeAnyKey covers the remaining caller-side
// chain guards: a steerless /compact and an invalid steer die before typing.
// It drives them through engine.inject (via injectChain) rather than the
// public Inject(), because Inject() now refuses a /compact primary outright
// (Task C) before checkSteerChain ever runs — these guards are reached today
// only by a chain hop (DeliverThen, Chain: true) or ScheduleAfterCurrentTurn.
func TestCompactFocusRuleRefusesBeforeAnyKey(t *testing.T) {
	tests := []struct {
		name     string
		request  Request
		wantCode int
		wantText string
	}{
		{
			name:     "steerless compact refused",
			request:  Request{Target: "chat", Message: "/compact hold: read /tmp/x.md"},
			wantCode: 6,
			wantText: "requires exactly one then steer",
		},
		{
			name: "compact steering into compact refused",
			request: Request{
				Target:  "chat",
				Message: "/compact hold: read /tmp/x.md",
				Then:    []string{" /compact again"},
			},
			wantCode: 6,
			wantText: "must not itself start with /compact",
		},
		{
			name: "empty steer refused",
			request: Request{
				Target:  "chat",
				Message: "plain work",
				Then:    []string{"  "},
			},
			wantCode: 6,
			wantText: "must be non-empty",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := &fakeTmux{capture: "conversation\n❯ ", submitOnEnter: true}
			spawner := &fakeSpawner{}
			engine := newTestEngineWith(t, "cc-1-2-3", fake, spawner)
			result, err := engine.injectChain(context.Background(), test.request)
			if err != nil {
				t.Fatal(err)
			}
			if result.Code != test.wantCode || result.Status != "refused" {
				t.Fatalf("inject() = %+v", result)
			}
			if !strings.Contains(result.Message, test.wantText) {
				t.Fatalf("diagnostic %q lacks %q", result.Message, test.wantText)
			}
			if fake.literal != "" || len(fake.keys) != 0 {
				t.Fatalf("refusal touched the pane: literal=%q keys=%q", fake.literal, fake.keys)
			}
			if len(spawner.spawned()) != 0 {
				t.Fatalf("refusal armed a waiter: %+v", spawner.spawned())
			}
		})
	}
}

// TestCompactWithSteerArmsWaiterOnlyAfterConfirmedSubmit covers chat.sh:922-951:
// the chain is armed after the primary submit is CONFIRMED, never before, and
// an unconfirmed submit arms nothing. It drives the delivery through
// injectChain (engine.inject with Chain: true) because the public Inject()
// now refuses a /compact primary outright (Task C) — DeliverThen's waiter is
// the only production caller left that hands engine.inject a /compact.
func TestCompactWithSteerArmsWaiterOnlyAfterConfirmedSubmit(t *testing.T) {
	fake := &fakeTmux{capture: "conversation\n❯ ", submitOnEnter: true}
	spawner := &fakeSpawner{}
	engine := newTestEngineWith(t, "cc-1-2-3", fake, spawner)
	result, err := engine.injectChain(context.Background(), Request{
		Target:  "chat",
		Message: "/compact hold: read /tmp/hold.md — keep the lock scheme",
		Then:    []string{"resume the port", "then run the tests"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 0 || result.Status != "delivered" || result.Steers != 2 {
		t.Fatalf("inject() = %+v", result)
	}
	spawned := spawner.spawned()
	if len(spawned) != 1 {
		t.Fatalf("spawned %d waiters, want exactly one", len(spawned))
	}
	if spawned[0].Target != "%1" ||
		len(spawned[0].Steers) != 2 ||
		spawned[0].Steers[0] != "resume the port" ||
		spawned[0].Steers[1] != "then run the tests" {
		t.Fatalf("waiter = %+v", spawned[0])
	}
	// Task E: the log is scoped by SOCKET as well as pane
	// (chat-then-<base(SocketPath)>-<Pane>.log) — a bare pane-derived name
	// collided across every chat sharing the fleet-standard %0 pane
	// (the 2026-09-03 self-compact that ate an operator's live draft).
	wantLog := engine.steerLogPath(Target{
		SocketPath: filepath.Join(string(filepath.Separator), "tmp", "tmux-jail", "cc-1-2-3"),
		Pane:       "%1",
	})
	if spawned[0].LogPath != wantLog {
		t.Fatalf("steer log path = %q, want %q", spawned[0].LogPath, wantLog)
	}
	if !strings.Contains(result.Message, "2 then steer(s) queued") {
		t.Fatalf("banner does not report the queued steers: %q", result.Message)
	}

	// A /compact travels bare: a footer would corrupt the harness command's
	// argument (chat.sh:659-668).
	if strings.Contains(fake.literal, "to reply:") {
		t.Fatalf("harness command was signed: %q", fake.literal)
	}

	unconfirmed := &fakeTmux{capture: "conversation\n❯ "}
	spawner = &fakeSpawner{}
	engine = newTestEngineWith(t, "cc-1-2-3", unconfirmed, spawner)
	result, err = engine.injectChain(context.Background(), Request{
		Target:  "chat",
		Message: "/compact hold: read /tmp/hold.md",
		Then:    []string{"resume the port"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 6 || result.Steers != 0 || len(spawner.spawned()) != 0 {
		t.Fatalf(
			"unconfirmed submit armed a steer: result=%+v spawned=%+v",
			result,
			spawner.spawned(),
		)
	}
}

// TestLongCompactFocusChunksAndFires is the user-visible size-limit
// regression fixture. A /compact focus this far beyond the TUI's paste edge
// must be split into small literal sends, so the command itself reaches the
// transcript and fires instead of being rejected or collapsed as a paste. It
// drives delivery through injectChain: the public Inject() now refuses a
// /compact primary outright (Task C), so the paced-literal guarantee is
// proven the way a chain hop (DeliverThen, Chain: true) exercises it.
func TestLongCompactFocusChunksAndFires(t *testing.T) {
	const focusRunes = 2147
	focus := strings.Repeat("f", focusRunes)
	wantMessage := "/compact " + focus

	for _, test := range []struct {
		name    string
		capture string
		status  string
	}{
		{
			name:    "idle",
			capture: "conversation\n❯ ",
			status:  "delivered",
		},
		{
			name:    "busy queues without interleaving",
			capture: "Working (10s · 500 tokens)\n❯ ",
			status:  "queued",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := &fakeTmux{
				capture:       test.capture,
				submitOnEnter: true,
				submitCapture: "USER: {{MESSAGE}}\nCOMPACTION FIRED\n❯ ",
			}
			if test.status == "queued" {
				fake.submitCapture = "USER: {{MESSAGE}}\nPress up to edit queued messages\n❯ "
			}
			spawner := &fakeSpawner{}
			engine := newTestEngineWith(t, "cc-long-compact", fake, spawner)
			result, err := engine.injectChain(context.Background(), Request{
				Target:  "chat",
				Message: wantMessage,
				Then:    []string{"resume after compaction"},
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.Code != 0 || result.Status != test.status ||
				result.AutoFilePath != "" || result.Steers != 1 {
				t.Fatalf("long compact result=%+v err=%v", result, err)
			}
			if len(fake.literals) < 2 {
				t.Fatalf("long compact was not internally chunked: %d literal sends", len(fake.literals))
			}
			var typed strings.Builder
			for _, literal := range fake.literals {
				if len([]rune(literal)) > CommandChunkRunes {
					t.Fatalf("literal chunk crossed TUI paste edge: %d runes", len([]rune(literal)))
				}
				typed.WriteString(literal)
			}
			if typed.String() != wantMessage {
				t.Fatalf("chunked compact changed command bytes: got %d want %d", typed.Len(), len(wantMessage))
			}
			if !strings.Contains(fake.capture, wantMessage) {
				t.Fatalf("transcript lost full compact focus: got %d bytes", len(fake.capture))
			}
			if !strings.Contains(fake.capture, "COMPACTION FIRED") && test.status == "delivered" {
				t.Fatalf("compact did not fire in transcript capture: %q", fake.capture)
			}
			if test.status == "queued" &&
				!strings.Contains(result.Proof, "Press up to edit queued messages") {
				t.Fatalf("busy compact proof lacks queue evidence: %q", result.Proof)
			}
			if strings.Contains(typed.String(), "— sid ") ||
				strings.Contains(typed.String(), "to reply: chat_inject") {
				t.Fatalf("slash command acquired a sender signature: %q", typed.String())
			}
		})
	}
}

// TestWaiterCarriesTheSenderTheChatResolved covers the hop identity is lost
// at: the waiter runs detached, so it must be TOLD who armed it. The primary
// here is a /compact — exempt from signing — which is exactly the case that
// hid the defect: the chat never needed its own identity for the message it
// typed, only for the steer a severed process would deliver minutes later.
// It drives delivery through injectChain: the public Inject() now refuses a
// /compact primary outright (Task C), so this is the chain-hop shape
// DeliverThen's waiter actually uses.
func TestWaiterCarriesTheSenderTheChatResolved(t *testing.T) {
	fake := &fakeTmux{
		capture:       "codex conversation\n› ",
		windowName:    "WAVE_ORCHESTRATOR",
		submitOnEnter: true,
	}
	spawner := &fakeSpawner{}
	engine := newSignatureEngineWith(t, "cc-1-2-3", fake, fakeIdentifier{
		identity: resolve.Identity{
			Session:    "cx-1700000000-1-1",
			SocketPath: "/tmp/tmux-jail/cx-1700000000-1-1",
			SocketName: "cx-1700000000-1-1",
			Pane:       "%3",
			Engine:     string(pfmengine.Codex),
			ID:         "019ffd1e-300f",
			Source:     "ancestry",
			Recovered:  true,
		},
	}, spawner)
	result, err := engine.injectChain(context.Background(), Request{
		Target:  "chat",
		Message: "/compact hold: read /tmp/hold.md",
		Then:    []string{"resume the wave"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 0 || result.Steers != 1 {
		t.Fatalf("inject() = %+v", result)
	}
	spawned := spawner.spawned()
	if len(spawned) != 1 {
		t.Fatalf("spawned %d waiters, want exactly one", len(spawned))
	}
	sender := spawned[0].Sender
	if sender.Session != "cx-1700000000-1-1" ||
		sender.Label != "WAVE_ORCHESTRATOR" ||
		sender.UUID != "019ffd1e-300f" {
		t.Fatalf("waiter carries sender %+v", sender)
	}
}

// TestCommandThenSpawnerStatesTheSenderToTheWaiter proves the identity survives
// the process boundary: the detached waiter derives nothing, so the three
// CHAT_SENDER_* names are the whole of what it knows about who armed it.
func TestCommandThenSpawnerStatesTheSenderToTheWaiter(t *testing.T) {
	scratch := t.TempDir()
	dump := filepath.Join(scratch, "environment.txt")
	stub := filepath.Join(scratch, "setsid-stub")
	script := "#!/bin/sh\nenv > " + dump + "\n"
	if err := os.WriteFile(stub, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	// An inherited definition must lose to the one this spawn states, or a
	// chain hop signs as whoever spawned the hop before it.
	t.Setenv(SenderSessionEnv, "cc-STALE-HOP")
	t.Setenv(SenderLabelEnv, "STALE_LABEL")
	spawner := CommandThenSpawner{
		Executable: filepath.Join(scratch, "pfm"),
		Setsid:     stub,
	}
	err := spawner.Spawn(context.Background(), SteerSpawn{
		SocketPath: filepath.Join(scratch, "cx-1-2-3"),
		Target:     "%3",
		Steers:     []string{"resume the wave"},
		LogPath:    filepath.Join(scratch, "steer.log"),
		Sender: Sender{
			Session: "cx-1700000000-1-1",
			UUID:    "019ffd1e-300f",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(dump)
	if err != nil {
		t.Fatal(err)
	}
	environment := string(raw)
	for _, want := range []string{
		SenderSessionEnv + "=cx-1700000000-1-1",
		SenderIDEnv + "=019ffd1e-300f",
	} {
		if !strings.Contains(environment, want) {
			t.Fatalf("waiter environment lacks %q", want)
		}
	}
	// The label was not resolved by this chat, so nothing is stated for it:
	// an inherited one would sign this message with another chat's name.
	for _, unwanted := range []string{
		"cc-STALE-HOP",
		"STALE_LABEL",
		SenderLabelEnv + "=",
	} {
		if strings.Contains(environment, unwanted) {
			t.Fatalf("waiter environment still carries %q", unwanted)
		}
	}
}

func TestCommandThenSpawnerFallsBackToNohup(t *testing.T) {
	scratch := t.TempDir()
	dump := filepath.Join(scratch, "nohup-arguments.txt")
	nohup := filepath.Join(scratch, "nohup-stub")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" > " + dump + "\n"
	if err := os.WriteFile(nohup, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	spawner := CommandThenSpawner{
		Executable: filepath.Join(scratch, "pfm-candidate"),
		Setsid:     filepath.Join(scratch, "setsid-missing"),
		Nohup:      nohup,
	}
	err := spawner.Spawn(context.Background(), SteerSpawn{
		SocketPath: filepath.Join(scratch, "cc-1-2-3"),
		Target:     "%4",
		Steers:     []string{"continue after the primary"},
		LogPath:    filepath.Join(scratch, "then.log"),
	})
	if err != nil {
		t.Fatalf("nohup floor was not used: %v", err)
	}
	// The stub is a SPAWNED process, so this waits on the OS scheduler rather
	// than on anything the engine does. One second is fine for a lone run and
	// too tight under a full parallel suite; a generous ceiling costs nothing
	// when the stub does run, and the assertion still fails if it never does.
	deadline := time.Now().Add(10 * time.Second)
	for {
		raw, readErr := os.ReadFile(dump)
		// The stub's `> dump` redirect CREATES the file before printf writes a
		// byte into it, so an empty read is "not yet", never "the stub ran and
		// passed no arguments" — under a loaded parallel suite that race is the
		// whole difference between this test and a false red.
		if readErr == nil && len(raw) > 0 {
			arguments := string(raw)
			if !strings.Contains(arguments, "pfm-candidate internal then") ||
				strings.Contains(arguments, "-f ") {
				t.Fatalf("nohup arguments = %q", arguments)
			}
			break
		}
		if time.Now().After(deadline) {
			// Name which of the two happened: no dump file at all (the stub
			// never ran) or an empty one (it ran and wrote nothing).
			t.Fatalf("nohup stub wrote no arguments within the deadline: read error %v, bytes %d", readErr, len(raw))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestSteerSpawnFailureIsReportedNotSwallowed keeps a failed arming visible:
// the primary landed, the follow-up did not, and the caller is told which.
// It drives delivery through injectChain: the public Inject() now refuses a
// /compact primary outright (Task C), so this is the chain-hop shape
// DeliverThen's waiter actually uses.
func TestSteerSpawnFailureIsReportedNotSwallowed(t *testing.T) {
	fake := &fakeTmux{capture: "conversation\n❯ ", submitOnEnter: true}
	spawner := &fakeSpawner{err: errors.New("no setsid")}
	engine := newTestEngineWith(t, "cc-1-2-3", fake, spawner)
	result, err := engine.injectChain(context.Background(), Request{
		Target:  "chat",
		Message: "/compact hold: read /tmp/hold.md",
		Then:    []string{"resume"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 6 ||
		result.Steers != 0 ||
		!strings.Contains(result.Message, "could NOT arm") {
		t.Fatalf("inject() = %+v", result)
	}
}

// TestLockNamespaceMatchesChatShell proves a Go inject and a chat.sh inject
// into the same pane contend for the SAME lock directory (chat.sh:144-172).
func TestLockNamespaceMatchesChatShell(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	key := "/tmp/tmux-1000/cc-1-2-3:%5"
	want := "_tmp_tmux-1000_cc-1-2-3_%5.lock"
	if got := lockDirName(key); got != want {
		t.Fatalf("lockDirName(%q) = %q, want %q", key, got, want)
	}
	ctx := context.Background()
	lock, err := acquireTargetLock(ctx, clock.Real, root, key, time.Second, time.Millisecond, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	owner, readErr := os.ReadFile(filepath.Join(root, want, "owner"))
	if readErr != nil {
		t.Fatalf("owner file: %v", readErr)
	}
	fields := strings.Fields(string(owner))
	if len(fields) != 2 || fields[0] != strconv.Itoa(os.Getpid()) {
		t.Fatalf("owner file = %q, want \"<pid> <epoch>\"", owner)
	}
	if _, err := acquireTargetLock(
		ctx,
		clock.Real,
		root,
		key,
		20*time.Millisecond,
		time.Millisecond,
		time.Minute,
	); err == nil {
		t.Fatal("second holder acquired a held lock")
	}
	lock.release()
	second, err := acquireTargetLock(ctx, clock.Real, root, key, time.Second, time.Millisecond, time.Minute)
	if err != nil {
		t.Fatalf("lock not released: %v", err)
	}
	second.release()

	// A lock whose owner process is gone is STOLEN, not waited on.
	stalePath := filepath.Join(root, want)
	if err := os.MkdirAll(stalePath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(stalePath, "owner"),
		[]byte("2147483646 1\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	stolen, err := acquireTargetLock(ctx, clock.Real, root, key, time.Second, time.Millisecond, time.Minute)
	if err != nil {
		t.Fatalf("stale lock was not stolen: %v", err)
	}
	stolen.release()
}

// TestDeliverThenLeavesACodexSteerUndeliveredWithoutATurnBoundary is Wave 8
// item 5's Codex ruling (the two 2026-09-18 sightings: a chat_self_compact
// steer never landed on a Codex chat, its log said "no turn boundary was
// observed"). The Claude busy regex does not know the Codex footer and the
// receipt regex does not know its compaction line, so "the pane never went
// busy" is what a Codex compaction IN PROGRESS looks like — the steady-idle
// fallback typed the steer into it. A steer lost with a named cause beats
// that: on a Codex pane an unobserved boundary is `undelivered`, by name,
// with the steer text kept in the message (= the log). The Claude path is
// unchanged: it still delivers, WITH the WARNING.
func TestDeliverThenLeavesACodexSteerUndeliveredWithoutATurnBoundary(t *testing.T) {
	quick := func(engine *Engine) {
		engine.options.ThenMin = time.Nanosecond
		engine.options.ThenBusyTries = 1
		engine.options.ThenIdlePoll = time.Nanosecond
		engine.options.ThenIdleTries = 1
		engine.options.ThenIdleStable = 1
		engine.options.ThenSettle = time.Nanosecond
	}
	t.Run("codex", func(t *testing.T) {
		fake := &fakeTmux{capture: "conversation\n› ", submitOnEnter: true}
		engine := newTestEngine(t, "cx-1-2-3", fake)
		quick(engine)

		result, err := engine.DeliverThen(context.Background(), ThenWait{
			Target: "chat",
			Steers: []string{"resume the wave"},
			Engine: string(pfmengine.Codex),
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != "undelivered" || result.Code != CodeUndelivered {
			t.Fatalf("Codex waiter with no turn boundary = %+v, want an undelivered result by name", result)
		}
		for _, want := range []string{
			"no turn boundary observed on a Codex pane",
			"E2.09",
			`"resume the wave"`,
		} {
			if !strings.Contains(result.Message, want) {
				t.Fatalf("undelivered message %q lacks %q", result.Message, want)
			}
		}
		if len(fake.literals) != 0 || len(fake.keys) != 0 {
			t.Fatalf("the steer was typed into the Codex pane anyway: literals=%q keys=%q", fake.literals, fake.keys)
		}
	})
	t.Run("claude", func(t *testing.T) {
		fake := &fakeTmux{capture: "conversation\n❯ ", submitOnEnter: true}
		engine := newTestEngine(t, "cc-1-2-3", fake)
		quick(engine)

		result, err := engine.DeliverThen(context.Background(), ThenWait{
			Target: "chat",
			Steers: []string{"resume the wave"},
			Engine: string(pfmengine.Claude),
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.Code != 0 || !result.Typed {
			t.Fatalf("Claude waiter with no turn boundary = %+v, want the delivery it always made", result)
		}
		if !strings.Contains(result.Message, "WARNING: no turn boundary was observed") {
			t.Fatalf("Claude delivery lost its WARNING: %q", result.Message)
		}
	})
}

// TestCommandThenSpawnerStatesTheEngineAndArmsTheRecord: the spawner hands
// the waiter the target's engine (`--engine`), and writes the armed record
// beside the log — unclaimed (pid 0), naming the first steer — BEFORE the
// waiter starts. A chain hop (Append) writes none: it is the same arming.
func TestCommandThenSpawnerStatesTheEngineAndArmsTheRecord(t *testing.T) {
	scratch := t.TempDir()
	dump := filepath.Join(scratch, "arguments.txt")
	stub := filepath.Join(scratch, "setsid-stub")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" > " + dump + "\n"
	if err := os.WriteFile(stub, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	spawner := CommandThenSpawner{Executable: filepath.Join(scratch, "pfm"), Setsid: stub}
	logPath := filepath.Join(scratch, "chat-then-cx_1_2_3._3.log")
	before := time.Now().Unix()
	err := spawner.Spawn(context.Background(), SteerSpawn{
		SocketPath: filepath.Join(scratch, "cx-1-2-3"),
		Target:     "%3",
		Engine:     string(pfmengine.Codex),
		Steers:     []string{"/compact", "resume the wave"},
		LogPath:    logPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(dump)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "--engine "+string(pfmengine.Codex)) {
		t.Fatalf("waiter argv lacks the engine: %q", raw)
	}
	record, exists, err := readArmedRecord(strings.TrimSuffix(logPath, ".log") + ".armed")
	if err != nil || !exists {
		t.Fatalf("armed record beside the log: exists=%t err=%v", exists, err)
	}
	if record.PID != 0 || record.Pane != "%3" || record.Steer != "/compact" || record.Stamp < before {
		t.Fatalf("armed record = %+v, want an unclaimed record naming pane %%3 and the first steer", record)
	}

	hopLog := filepath.Join(scratch, "chat-then-hop.log")
	err = spawner.Spawn(context.Background(), SteerSpawn{
		SocketPath: filepath.Join(scratch, "cx-1-2-3"),
		Target:     "%3",
		Steers:     []string{"resume the wave"},
		LogPath:    hopLog,
		Append:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, exists, err := readArmedRecord(strings.TrimSuffix(hopLog, ".log") + ".armed"); err != nil || exists {
		t.Fatalf("a chain hop wrote its own armed record: exists=%t err=%v", exists, err)
	}
}
