package call

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/deps"
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
// It drives them through engine.call (via callChain) rather than the
// public Call(), because Call() now refuses a /compact primary outright
// (Task C) before checkSteerChain ever runs — these guards are reached today
// only by a chain hop (DeliverThen, Chain: true).
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
			result, err := engine.callChain(context.Background(), test.request)
			if err != nil {
				t.Fatal(err)
			}
			if result.Code != test.wantCode || result.Status != "refused" {
				t.Fatalf("call() = %+v", result)
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
// callChain (engine.call with Chain: true) because the public Call()
// now refuses a /compact primary outright (Task C) — DeliverThen's waiter is
// the only production caller left that hands engine.call a /compact.
func TestCompactWithSteerArmsWaiterOnlyAfterConfirmedSubmit(t *testing.T) {
	fake := &fakeTmux{capture: "conversation\n❯ ", submitOnEnter: true}
	spawner := &fakeSpawner{}
	engine := newTestEngineWith(t, "cc-1-2-3", fake, spawner)
	result, err := engine.callChain(context.Background(), Request{
		Target:  "chat",
		Message: "/compact hold: read /tmp/hold.md — keep the lock scheme",
		Then:    []string{"resume the port", "then run the tests"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 0 || result.Status != "delivered" || result.Steers != 2 {
		t.Fatalf("call() = %+v", result)
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
	// (the 2026-09-03 compaction that ate an operator's live draft).
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
	result, err = engine.callChain(context.Background(), Request{
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
// drives delivery through callChain: the public Call() now refuses a
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
			result, err := engine.callChain(context.Background(), Request{
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
				strings.Contains(typed.String(), "to reply: call") {
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
// It drives delivery through callChain: the public Call() now refuses a
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
	result, err := engine.callChain(context.Background(), Request{
		Target:  "chat",
		Message: "/compact hold: read /tmp/hold.md",
		Then:    []string{"resume the wave"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 0 || result.Steers != 1 {
		t.Fatalf("call() = %+v", result)
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
	// An inherited definition must lose to the one this spawn states, or a
	// chain hop signs as whoever spawned the hop before it.
	t.Setenv(SenderSessionEnv, "cc-STALE-HOP")
	t.Setenv(SenderLabelEnv, "STALE_LABEL")
	runner := &deps.FakeRunner{}
	spawner := CommandThenSpawner{Executable: filepath.Join(scratch, "pfm"), Runner: runner}
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
	starts := runner.Starts()
	if len(starts) != 1 {
		t.Fatalf("starts = %+v, want exactly one", starts)
	}
	environment := strings.Join(starts[0].Opts.Env, "\n")
	for _, want := range []string{
		SenderSessionEnv + "=cx-1700000000-1-1",
		SenderIDEnv + "=019ffd1e-300f",
	} {
		if !slices.Contains(starts[0].Opts.Env, want) {
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

// The waiter starts detached through the runner: its own argv with no
// launcher in front, its log on stdout and stderr, and the handle released.
// A start that fails names the waiter and takes back the armed record.
func TestCommandThenSpawnerStartsTheWaiterDetached(t *testing.T) {
	for _, tc := range []struct {
		name          string
		startErr      error
		wantErr       string
		wantArmed     bool
		wantLifecycle []deps.LifecycleCall
	}{
		{name: "started", wantArmed: true, wantLifecycle: []deps.LifecycleCall{{Pid: 4242, Action: "release"}}},
		{name: "start fails", startErr: errors.New("boom"), wantErr: "start detached then waiter: boom"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scratch := t.TempDir()
			runner := &deps.FakeRunner{}
			if tc.startErr != nil {
				runner.ScriptStart([]string{"/jail/pfm"}, 0, nil, tc.startErr)
			}
			logPath := filepath.Join(scratch, "then.log")
			err := CommandThenSpawner{Executable: "/jail/pfm", ConfigPath: "/jail/pfm.json", Runner: runner}.Spawn(
				context.Background(),
				SteerSpawn{
					SocketPath: "/jail/tmux/cc-1-2-3",
					Target:     "%4",
					Steers:     []string{"continue after the primary"},
					LogPath:    logPath,
				},
			)
			if tc.wantErr == "" && err != nil || tc.wantErr != "" && (err == nil || err.Error() != tc.wantErr) {
				t.Fatalf("Spawn() = %v, want error %q", err, tc.wantErr)
			}
			starts := runner.Starts()
			if len(starts) != 1 {
				t.Fatalf("starts = %+v, want exactly one", starts)
			}
			want := []string{
				"/jail/pfm", "--config", "/jail/pfm.json", "internal", "then",
				"--socket", "/jail/tmux/cc-1-2-3", "--target", "%4", "--steer", "continue after the primary",
			}
			if !slices.Equal(starts[0].Argv, want) {
				t.Fatalf("waiter argv = %q, want %q", starts[0].Argv, want)
			}
			opts := starts[0].Opts
			stdout, stdoutOK := opts.Stdout.(*os.File)
			stderr, stderrOK := opts.Stderr.(*os.File)
			if !opts.Detach || !stdoutOK || !stderrOK || stdout.Name() != logPath || stderr.Name() != logPath {
				t.Fatalf("start options = %+v, want Detach with stdout and stderr on %s", opts, logPath)
			}
			if got := runner.LifecycleCalls(); !slices.Equal(got, tc.wantLifecycle) {
				t.Fatalf("lifecycle = %+v, want %+v", got, tc.wantLifecycle)
			}
			if _, armed, err := readArmedRecord(armedPathFor(logPath)); err != nil || armed != tc.wantArmed {
				t.Fatalf("armed record exists = %t (err %v), want %t", armed, err, tc.wantArmed)
			}
		})
	}
}

// TestSteerSpawnFailureIsReportedNotSwallowed keeps a failed arming visible:
// the primary landed, the follow-up did not, and the caller is told which.
// It drives delivery through callChain: the public Call() now refuses a
// /compact primary outright (Task C), so this is the chain-hop shape
// DeliverThen's waiter actually uses.
func TestSteerSpawnFailureIsReportedNotSwallowed(t *testing.T) {
	fake := &fakeTmux{capture: "conversation\n❯ ", submitOnEnter: true}
	spawner := &fakeSpawner{err: errors.New("waiter start failed")}
	engine := newTestEngineWith(t, "cc-1-2-3", fake, spawner)
	result, err := engine.callChain(context.Background(), Request{
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
		t.Fatalf("call() = %+v", result)
	}
}

// TestLockNamespaceMatchesChatShell proves a Go call and a chat.sh call
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
// item 5's Codex ruling (the two 2026-09-18 sightings: a compaction
// steer never landed on a Codex chat, its log said "no turn boundary was
// observed"). The Codex footer is now readable, while its compaction receipt
// spelling remains unconfirmed. A steady-idle pane without an observed busy
// turn is still a guess. On a Codex pane that leaves the steer `undelivered`,
// by name,
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
			"steady-idle guess",
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
	runner := &deps.FakeRunner{}
	spawner := CommandThenSpawner{Executable: filepath.Join(scratch, "pfm"), Runner: runner}
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
	if starts := runner.Starts(); len(starts) != 1 ||
		!strings.Contains(strings.Join(starts[0].Argv, " "), "--engine "+string(pfmengine.Codex)) {
		t.Fatalf("waiter starts = %+v, want one argv carrying the engine", starts)
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

func TestCommandThenSpawnerCarriesNativeBoundaryEvidence(t *testing.T) {
	for _, backend := range []string{"", "tmux", "herdr"} {
		t.Run("backend-"+backend, func(t *testing.T) {
			t.Setenv(herdrTurnEnv, "inherited-other-target")
			root := t.TempDir()
			runner := &deps.FakeRunner{}
			request := SteerSpawn{
				Backend:    backend,
				SocketPath: "/native/socket",
				Target:     "p2",
				Engine:     "cc",
				Steers:     []string{"continue"},
				LogPath:    filepath.Join(root, "then.log"),
			}
			if backend == "herdr" {
				request.herdrTurn = &herdrTurn{
					SocketPath:     request.SocketPath,
					Pane:           request.Target,
					StateChangeSeq: 4,
					SawWorking:     true,
				}
			}
			if err := (CommandThenSpawner{Executable: filepath.Join(root, "pfm"), Runner: runner}).Spawn(
				context.Background(),
				request,
			); err != nil {
				t.Fatal(err)
			}
			starts := runner.Starts()
			if len(starts) != 1 {
				t.Fatalf("starts = %+v, want exactly one", starts)
			}
			turn := ""
			for _, entry := range starts[0].Opts.Env {
				if value, found := strings.CutPrefix(entry, herdrTurnEnv+"="); found {
					turn = value
				}
			}
			lines := []string{strings.Join(starts[0].Argv, " "), turn}
			body := strings.Join(lines, "\n")
			if !strings.Contains(lines[0], "internal then --socket /native/socket --target p2") {
				t.Fatalf("detached argv/env=%q", body)
			}
			if backend == "herdr" {
				if !strings.Contains(lines[0], "--backend herdr") ||
					!strings.Contains(lines[1], `"state_change_seq":4`) ||
					!strings.Contains(lines[1], `"pane":"p2"`) {
					t.Fatalf("native handoff=%q", body)
				}
			} else if strings.Contains(lines[0], "--backend") || lines[1] != "" {
				t.Fatalf("legacy tmux handoff=%q", body)
			}
		})
	}
}

// A waiter that could not write its log would run with no voice: an
// undelivered steer is kept only there. It is never started; the caller is
// told why and delivers the steer itself.
func TestCommandThenSpawnerRefusesAnUnwritableLog(t *testing.T) {
	scratch := t.TempDir()
	blocker := filepath.Join(scratch, "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &deps.FakeRunner{}
	logPath := filepath.Join(blocker, "then.log")
	err := CommandThenSpawner{
		Executable: filepath.Join(scratch, "pfm-candidate"),
		Runner:     runner,
	}.Spawn(context.Background(), SteerSpawn{
		SocketPath: filepath.Join(scratch, "cc-1-2-3"),
		Target:     "%4",
		Steers:     []string{"continue after the primary"},
		LogPath:    logPath,
	})
	want := "open then waiter log: open " + logPath + ": not a directory"
	if err == nil || err.Error() != want {
		t.Fatalf("spawn error = %v, want %q", err, want)
	}
	if starts := runner.Starts(); len(starts) != 0 {
		t.Fatalf("waiter started despite the unwritable log: %+v", starts)
	}
}
