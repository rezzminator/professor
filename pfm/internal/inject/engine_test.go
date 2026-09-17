package inject

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"hostops/pfm/internal/resolve"
	"hostops/pfm/internal/shared"
)

type fakeResolver struct {
	socket string
	target string
	code   int
	detail string
}

func (fake fakeResolver) Resolve(
	_ context.Context,
	_ resolve.Kind,
	_ string,
) (resolve.Outcome, error) {
	stdout := ""
	if fake.code == 0 {
		stdout = fake.socket + "\t" + fake.target + "\n"
	}
	return resolve.Outcome{
		Code:   fake.code,
		Stdout: stdout,
		Stderr: fake.detail,
	}, nil
}

type fakeTmux struct {
	mu            sync.Mutex
	capture       string
	styled        string
	literal       string
	literalBuffer string
	literals      []string
	pasted        bool
	windowName    string
	paneCommand   string
	keys          []string
	dead          bool
	busyUntilEsc  bool
	stashClears   bool
	submitOnEnter bool
	// submitOnEnterAfter, when > 0, is the 1-indexed Enter press that first
	// actually confirms the submit (earlier presses are swallowed — the
	// composer still visibly holds the message, exactly like a not-yet-drawn
	// real terminal). 0 (the zero value) keeps the pre-existing behavior:
	// the very first Enter confirms, matching every fixture that predates
	// F6 of the merge-gating review.
	submitOnEnterAfter int
	enterCount         int
	submitCapture      string
	proofCapture       string
	submitted          bool
	postCaptures       int
	inMode             bool
	modeAfterEsc       bool
	cancelModes        int
	killLiteral        bool
	// clientAttached, clientActivity, and clientErr back ClientActivity — the
	// typist guard. Default (clientAttached=false) is "no client": the vast
	// majority of jailed fixtures model an unattended pane, so the guard must
	// stay OUT of the way unless a test deliberately sets these.
	clientAttached bool
	clientActivity time.Time
	clientErr      error
}

// fakeSpawner records the detached --then waiter instead of starting one.
type fakeSpawner struct {
	mu    sync.Mutex
	calls []SteerSpawn
	err   error
}

func (fake *fakeSpawner) Spawn(_ context.Context, request SteerSpawn) error {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.err != nil {
		return fake.err
	}
	fake.calls = append(fake.calls, request)
	return nil
}

func (fake *fakeSpawner) spawned() []SteerSpawn {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return append([]SteerSpawn(nil), fake.calls...)
}

func TestScheduleAfterCurrentTurnDoesNotQueueACompactAsModelInput(t *testing.T) {
	fake := &fakeTmux{capture: "Working (10s)\n› Ask Codex to do anything"}
	spawner := &fakeSpawner{}
	engine := newTestEngineWith(t, "cx-scheduled-compact", fake, spawner)
	result, err := engine.ScheduleAfterCurrentTurn(context.Background(), Request{
		Target:  "chat",
		Message: "/compact preserve the live findings",
		Then:    []string{"resume after real compaction"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 0 || result.Status != "scheduled" || result.Typed || result.Steers != 1 {
		t.Fatalf("scheduled compact result = %+v", result)
	}
	if len(fake.literals) != 0 || len(fake.keys) != 0 {
		t.Fatalf("scheduler typed into the busy model turn: literals=%q keys=%q", fake.literals, fake.keys)
	}
	calls := spawner.spawned()
	if len(calls) != 1 || !reflect.DeepEqual(calls[0].Steers, []string{
		"/compact preserve the live findings",
		"resume after real compaction",
	}) {
		t.Fatalf("detached compact chain = %+v", calls)
	}
}

// TestScheduleAfterCurrentTurnComposedSelfCompactKeepsThenAndNoticeOnce pins
// two invariants Task D's mcpserv focus-composition fix must not disturb:
// isCompactCommand's "^/compact(\s|$)" match still recognises the composed
// "/compact <focus>" form Target:"self" now carries (so the T1/notice logic
// still treats it as a self-compaction), the caller's Then steers ride along
// unchanged after the composed primary, and SelfCompactStopNotice — which
// this engine appends, not the MCP layer — rides exactly once on the
// result. This does not regress against the pre-fix bare "/compact": that
// literal string also matches the same pattern (the "$" branch), so this is
// a boundary pin, not a fix-defect regression.
//
// "self" resolves through engine.whoami (ambient tmux identity via
// resolve.NewWhoami), never through the injected Resolver — see
// engine.go's resolve() — so it is fixed here with fakeSelf, exactly as
// TestSelfCompactScheduleTellsTheCallerToStop (then_edge_test.go) already
// does. Leaving whoami at New()'s real ambient default is a jail leak: it
// happened to resolve on a host already running inside a real tmux/chat
// session, then correctly refused inside the isolated fence container
// (Code 4, "self target has no live tmux seat") — watched directly.
func TestScheduleAfterCurrentTurnComposedSelfCompactKeepsThenAndNoticeOnce(t *testing.T) {
	fake := &fakeTmux{capture: "conversation\n❯ "}
	spawner := &fakeSpawner{}
	engine := newTestEngineWith(t, "cc-self-compact-schedule", fake, spawner)
	engine.whoami = fakeSelf{identity: resolve.Identity{
		Session:    "cc-self-compact-schedule",
		SocketPath: filepath.Join(string(filepath.Separator), "tmp", "tmux-jail", "cc-self-compact-schedule"),
		Pane:       "%1",
		Engine:     "claude",
		Source:     "test",
	}}
	then := []string{"resume the acceptance test", "confirm the ledger is clean"}
	result, err := engine.ScheduleAfterCurrentTurn(context.Background(), Request{
		Target:  "self",
		Message: "/compact wave three closeout",
		Then:    then,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 0 || result.Status != "scheduled" || result.Steers != len(then) {
		t.Fatalf("scheduled self compact result = %+v", result)
	}
	if count := strings.Count(result.Message, SelfCompactStopNotice); count != 1 {
		t.Fatalf("SelfCompactStopNotice appeared %d time(s) in %q, want exactly 1", count, result.Message)
	}
	calls := spawner.spawned()
	wantSteers := append([]string{"/compact wave three closeout"}, then...)
	if len(calls) != 1 || !reflect.DeepEqual(calls[0].Steers, wantSteers) {
		t.Fatalf("detached self-compact chain = %+v, want steers %q", calls, wantSteers)
	}
}

func (fake *fakeTmux) Capture(
	_ context.Context,
	_, _ string,
	styled bool,
	_ int,
) (string, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.dead {
		return "", errors.New("dead")
	}
	if fake.submitted && fake.proofCapture != "" {
		fake.postCaptures++
		if fake.postCaptures > 1 {
			return fake.proofCapture, nil
		}
	}
	if styled && fake.styled != "" {
		return fake.styled, nil
	}
	return fake.capture, nil
}

func (fake *fakeTmux) SendLiteral(
	_ context.Context,
	_, _ string,
	text string,
) error {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.dead {
		return errors.New("dead")
	}
	fake.literal = text
	fake.literalBuffer += text
	fake.literals = append(fake.literals, text)
	if fake.killLiteral {
		return nil
	}
	marker := "❯"
	if strings.Contains(fake.capture, "›") &&
		!strings.Contains(fake.capture, "❯") {
		marker = "›"
	}
	fake.capture += "\n" + marker + " " + text
	return nil
}

func (fake *fakeTmux) SendPaste(
	ctx context.Context,
	socketPath, target, text string,
) error {
	fake.mu.Lock()
	fake.pasted = true
	fake.mu.Unlock()
	return fake.SendLiteral(ctx, socketPath, target, text)
}

func (fake *fakeTmux) SendKey(
	_ context.Context,
	_, _ string,
	key string,
) error {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.dead {
		return errors.New("dead")
	}
	fake.keys = append(fake.keys, key)
	switch key {
	case "Escape":
		if fake.modeAfterEsc {
			fake.inMode = true
			fake.modeAfterEsc = false
		}
		if fake.busyUntilEsc {
			fake.capture = "turn interrupted\n❯ "
			fake.busyUntilEsc = false
		}
	case "C-s":
		if fake.stashClears {
			fake.capture = "draft stashed\n❯ "
		}
	case "Enter":
		if fake.submitOnEnter && fake.literalBuffer != "" {
			fake.enterCount++
			if fake.submitOnEnterAfter > 0 && fake.enterCount < fake.submitOnEnterAfter {
				// Swallowed: the composer still visibly holds the message
				// (fake.capture is left exactly as SendLiteral rendered it),
				// so this press must not confirm yet.
				break
			}
			if fake.submitCapture != "" {
				fake.capture = strings.ReplaceAll(
					fake.submitCapture,
					"{{MESSAGE}}",
					fake.literalBuffer,
				)
			} else {
				fake.capture = "USER: " + fake.literalBuffer + "\n❯ "
			}
			fake.literal = ""
			fake.literalBuffer = ""
			fake.submitted = true
		}
	}
	return nil
}

func (fake *fakeTmux) CancelCopyMode(
	context.Context,
	string,
	string,
) error {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.inMode = false
	fake.cancelModes++
	return nil
}

func (fake *fakeTmux) PaneInMode(
	context.Context,
	string,
	string,
) (bool, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return fake.inMode, nil
}

func (fake *fakeTmux) PaneCommand(
	context.Context,
	string,
	string,
) (string, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return fake.paneCommand, nil
}

func (*fakeTmux) CurrentSession(context.Context, string) (string, error) {
	return "sender-session", nil
}

func (fake *fakeTmux) WindowName(
	context.Context,
	string,
	string,
) (string, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return fake.windowName, nil
}

func (fake *fakeTmux) ClientActivity(
	context.Context,
	string,
	string,
) (time.Time, bool, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.clientErr != nil {
		return time.Time{}, false, fake.clientErr
	}
	return fake.clientActivity, fake.clientAttached, nil
}

func newTestEngine(t *testing.T, socket string, tmux *fakeTmux) *Engine {
	t.Helper()
	return newTestEngineWith(t, socket, tmux, &fakeSpawner{})
}

func newTestEngineWith(
	t *testing.T,
	socket string,
	tmux *fakeTmux,
	spawner ThenSpawner,
) *Engine {
	t.Helper()
	if tmux.paneCommand == "" {
		tmux.paneCommand = "claude"
		if strings.HasPrefix(socket, "cx-") {
			tmux.paneCommand = "codex"
		}
	}
	t.Setenv("PFM_HOME", t.TempDir())
	t.Setenv("TMUX", "")
	t.Setenv("CHAT_INJECT_SOCKET", "")
	clearStatedSender(t)
	engine, err := New(Dependencies{
		Resolver: fakeResolver{
			socket: filepath.Join(string(filepath.Separator), "tmp", "tmux-jail", socket),
			target: "%1",
		},
		Tmux:    tmux,
		Spawner: spawner,
		Options: Options{
			Poll:              time.Nanosecond,
			EnterSettle:       time.Nanosecond,
			ProofSettle:       time.Nanosecond,
			BusyTries:         2,
			InterruptTries:    2,
			StashTries:        2,
			SettleTries:       2,
			EnterTries:        2,
			LockTimeout:       time.Second,
			LockPoll:          time.Nanosecond,
			LockMaxHold:       time.Second,
			LockRoot:          t.TempDir(),
			ThenLogRoot:       t.TempDir(),
			Sender:            &Sender{Session: "sender", Label: "Operator", UUID: "1234567890"},
			ClaudeInlineMax:   ClaudeInlineMax,
			CodexInlineMax:    CodexInlineMax,
			CommandChunkRunes: CommandChunkRunes,
			CommandChunkGap:   time.Nanosecond,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

// injectChain drives the engine's internal chain-eligible delivery path
// directly — the same entry point DeliverThen's detached waiter uses
// (Chain: true) — the only production route left to a /compact primary now
// that the public Inject() (chat_inject) refuses one outright (Task C:
// /compact is never injected, only scheduled via chat_self_compact /
// `pfm chat self-compact`). Tests pinning the paced-literal and
// full-transcript delivery guarantees for a /compact command call this
// instead of Inject().
func (engine *Engine) injectChain(ctx context.Context, request Request) (Result, error) {
	ctx = withSender(ctx, engine.sender(ctx))
	request.Chain = true
	return engine.inject(ctx, request)
}

func TestInjectGuardAndDeliveryMatrix(t *testing.T) {
	tests := []struct {
		name       string
		socket     string
		capture    string
		configure  func(*fakeTmux)
		request    Request
		wantCode   int
		wantStatus string
		wantTyped  bool
		wantKey    string
		wantNoKey  string
	}{
		{
			name:       "claude selector aborts before any key",
			socket:     "cc-1-2-3",
			capture:    "Question\n❯ 1. Allow\n  2. Deny\n",
			request:    Request{Target: "chat", Message: "do work"},
			wantCode:   6,
			wantStatus: "refused",
		},
		{
			name:       "codex selector aborts before any key",
			socket:     "cx-1-2-3",
			capture:    "› 1. Allow\n  2. Deny\nenter to confirm\n",
			request:    Request{Target: "chat", Message: "do work"},
			wantCode:   6,
			wantStatus: "refused",
		},
		{
			name:    "busy claude queues without interrupting",
			socket:  "cc-1-2-3",
			capture: "Working (2s · 9 tokens)\n❯ ",
			configure: func(fake *fakeTmux) {
				fake.submitOnEnter = true
			},
			request:    Request{Target: "chat", Message: "queue this"},
			wantCode:   0,
			wantStatus: "queued",
			wantTyped:  true,
			wantNoKey:  "C-s",
		},
		{
			name:       "busy claude draft refuses without typing",
			socket:     "cc-1-2-3",
			capture:    "Working (2s · 9 tokens)\n❯ existing draft",
			request:    Request{Target: "chat", Message: "do work"},
			wantCode:   6,
			wantStatus: "refused",
		},
		{
			name:    "busy codex queues without interrupting",
			socket:  "cx-1-2-3",
			capture: "Working (2s · 9 tokens) · esc to interrupt\n› ",
			configure: func(fake *fakeTmux) {
				fake.submitOnEnter = true
			},
			request:    Request{Target: "chat", Message: "queue this"},
			wantCode:   0,
			wantStatus: "queued",
			wantTyped:  true,
		},
		{
			name:   "busy codex vitest output is not a composer draft",
			socket: "cx-1-2-3",
			capture: "Working (2s · 9 tokens) · esc to interrupt\n" +
				"└ ❯ src/tests/integration/gate-transaction-crash-safety.db.test.ts (3 tests | 1\n" +
				"› ",
			configure: func(fake *fakeTmux) {
				fake.submitOnEnter = true
			},
			request:    Request{Target: "chat", Message: "queue after vitest output"},
			wantCode:   0,
			wantStatus: "queued",
			wantTyped:  true,
			wantNoKey:  "C-s",
		},
		{
			name:   "busy claude agent overlay row is not a composer draft",
			socket: "cc-1-2-3",
			capture: "Working (2s · 9 tokens)\n" +
				"❯ ● qa-cortex  Verifying 6-bugs.md is untracked",
			configure: func(fake *fakeTmux) {
				fake.submitOnEnter = true
			},
			request:    Request{Target: "chat", Message: "queue after agent overlay"},
			wantCode:   0,
			wantStatus: "queued",
			wantTyped:  true,
			wantNoKey:  "C-s",
		},
		{
			name:    "busy codex dim placeholder is not a draft",
			socket:  "cx-1-2-3",
			capture: "Working (2s · 9 tokens) · esc to interrupt\n› \x1b[2mAsk Codex to do anything\x1b[0m",
			configure: func(fake *fakeTmux) {
				fake.submitOnEnter = true
			},
			request:    Request{Target: "chat", Message: "queue after placeholder"},
			wantCode:   0,
			wantStatus: "queued",
			wantTyped:  true,
		},
		{
			name:    "force now interrupts then delivers",
			socket:  "cc-1-2-3",
			capture: "Working (2s · 9 tokens)",
			configure: func(fake *fakeTmux) {
				fake.busyUntilEsc = true
				fake.submitOnEnter = true
			},
			request: Request{
				Target: "chat", Message: "do work", ForceNow: true,
			},
			wantCode: 0, wantStatus: "delivered", wantTyped: true, wantKey: "Escape",
		},
		{
			name:   "idle composer delivers signed text",
			socket: "cc-1-2-3",
			capture: "conversation\n" +
				"❯ ",
			configure: func(fake *fakeTmux) {
				fake.submitOnEnter = true
			},
			request:  Request{Target: "chat", Message: "do work"},
			wantCode: 0, wantStatus: "delivered", wantTyped: true, wantKey: "C-s",
		},
		{
			name:    "numbered codex draft is not a selector",
			socket:  "cx-1-2-3",
			capture: "conversation\n› 1. do this\n",
			configure: func(fake *fakeTmux) {
				fake.stashClears = true
				fake.submitOnEnter = true
			},
			request:  Request{Target: "chat", Message: "next"},
			wantCode: 0, wantStatus: "delivered", wantTyped: true,
		},
		{
			name:       "unstashable claude draft aborts",
			socket:     "cc-1-2-3",
			capture:    "❯ target draft",
			request:    Request{Target: "chat", Message: "next"},
			wantCode:   6,
			wantStatus: "refused",
			wantKey:    "C-s",
		},
		{
			name:       "dead pane refuses",
			socket:     "cc-1-2-3",
			capture:    "❯ ",
			configure:  func(fake *fakeTmux) { fake.dead = true },
			request:    Request{Target: "chat", Message: "next"},
			wantCode:   3,
			wantStatus: "refused",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := &fakeTmux{capture: test.capture}
			if test.configure != nil {
				test.configure(fake)
			}
			engine := newTestEngine(t, test.socket, fake)
			before := fake.capture
			result, err := engine.Inject(context.Background(), test.request)
			if err != nil {
				t.Fatal(err)
			}
			if result.Code != test.wantCode ||
				result.Status != test.wantStatus ||
				result.Typed != test.wantTyped {
				t.Fatalf("Inject() = %+v", result)
			}
			if !test.wantTyped && fake.literal != "" {
				t.Fatalf("refusal typed literal %q", fake.literal)
			}
			if test.wantKey != "" && !contains(fake.keys, test.wantKey) {
				t.Fatalf("keys = %q, want %q", fake.keys, test.wantKey)
			}
			if test.wantNoKey != "" && contains(fake.keys, test.wantNoKey) {
				t.Fatalf("keys = %q, must not contain %q", fake.keys, test.wantNoKey)
			}
			if test.wantCode == 4 && fake.capture != before {
				t.Fatalf("selector refusal changed capture: %q -> %q", before, fake.capture)
			}
			if test.wantTyped &&
				!strings.Contains(result.Proof, "to reply: chat_inject Operator <message>") {
				t.Fatalf("proof lacks mandatory sender signature: %q", result.Proof)
			}
		})
	}
}

func TestInjectRecordsOnlyDeliveredDirectMessages(t *testing.T) {
	newEngine := func(t *testing.T, recorder func(context.Context, shared.CommsEvent) error) (*Engine, *bytes.Buffer) {
		t.Helper()
		fake := &fakeTmux{capture: "› ", submitOnEnter: true}
		engine := newTestEngine(t, "cc-1-2-3", fake)
		warnings := &bytes.Buffer{}
		engine.recorder = recorder
		engine.warningWriter = warnings
		engine.options.Now = func() time.Time { return time.Unix(0, 123) }
		return engine, warnings
	}

	t.Run("direct delivery", func(t *testing.T) {
		var recorded []shared.CommsEvent
		engine, warnings := newEngine(t, func(_ context.Context, event shared.CommsEvent) error {
			recorded = append(recorded, event)
			return nil
		})
		result, err := engine.Inject(context.Background(), Request{Target: "beta", Message: "hello\nverbatim"})
		if err != nil || result.Code != 0 || !result.Typed {
			t.Fatalf("Inject() = %+v, %v", result, err)
		}
		want := []shared.CommsEvent{{
			AtNS: 123, Kind: shared.KindInject, SenderSession: "sender",
			SenderLabel: "Operator", SenderUUID: "1234567890", Target: "beta",
			ReceiverSocket: filepath.Join(string(filepath.Separator), "tmp", "tmux-jail", "cc-1-2-3"),
			ReceiverPane:   "%1", Message: "hello\nverbatim",
		}}
		if !reflect.DeepEqual(recorded, want) {
			t.Fatalf("recorded = %#v, want %#v", recorded, want)
		}
		if warnings.Len() != 0 {
			t.Fatalf("warnings = %q", warnings.String())
		}
	})

	t.Run("recorder failure warns without changing delivery", func(t *testing.T) {
		engine, warnings := newEngine(t, func(context.Context, shared.CommsEvent) error {
			return errors.New("database unavailable")
		})
		result, err := engine.Inject(context.Background(), Request{Target: "beta", Message: "delivered"})
		if err != nil || result.Code != 0 || !result.Typed {
			t.Fatalf("Inject() = %+v, %v", result, err)
		}
		if got := warnings.String(); !strings.Contains(got, "pfm: comms ledger: database unavailable") {
			t.Fatalf("warnings = %q", got)
		}
	})
}

func TestInjectDoesNotRecordTypedButUndeliveredMessage(t *testing.T) {
	fake := &fakeTmux{capture: "› "}
	engine := newTestEngine(t, "cx-undelivered-ledger", fake)
	recorded := 0
	engine.recorder = func(context.Context, shared.CommsEvent) error {
		recorded++
		return nil
	}

	result, err := engine.Inject(context.Background(), Request{
		Target: "chat", Message: "typed but never submitted",
	})
	if err != nil || result.Code != CodeUndelivered || !result.Typed {
		t.Fatalf("Inject() = %+v, %v; want typed undelivered result", result, err)
	}
	if recorded != 0 {
		t.Fatalf("recorder calls = %d, want 0 for typed undelivered message", recorded)
	}
}

// TestInjectBodyAboveFormerAbsoluteCapUsesPaste pins the fixed Task C
// contract for the size that used to be the worst case for the old
// auto-file spill: a body so large (1 MiB) that under the old code it was
// unconditionally replaced by a file pointer. It must now still reach the
// pane byte-exact through bracketed paste — the auto-file store is a
// fallback for an UNPROVEN delivery, not a proactive size guard. Renamed
// from ...UsesAutoFile, which would otherwise assert a behaviour this body
// no longer exercises.
func TestInjectBodyAboveFormerAbsoluteCapUsesPaste(t *testing.T) {
	fake := &fakeTmux{capture: "› ", submitOnEnter: true}
	engine := newTestEngine(t, "cx-former-absolute-cap", fake)
	body := "former absolute cap payload\n" + strings.Repeat("x", 1<<20)
	result, err := engine.Inject(context.Background(), Request{
		Target:  "chat",
		Message: body,
	})
	if err != nil || result.Code != 0 || result.AutoFilePath != "" ||
		strings.Contains(result.Message, "AUTO-FILE") ||
		!strings.Contains(result.Message, "PASTE") ||
		!fake.pasted || len(fake.literals) != 1 {
		t.Fatalf(
			"former absolute cap result=%+v pasted=%v literals=%d err=%v",
			result,
			fake.pasted,
			len(fake.literals),
			err,
		)
	}
	if !strings.HasPrefix(fake.literals[0], body) {
		t.Fatalf(
			"paste transport did not carry the megabyte body byte-exact: got %d bytes, want prefix of %d bytes",
			len(fake.literals[0]),
			len(body),
		)
	}
}

// TestInjectPasteBoundaryAndKillerBody pins the inline-vs-paste boundary
// (formerly inline-vs-auto-file): one rune under the threshold still goes by
// plain SendLiteral, and everything over it — including a body that used to
// be reduced to a pointer as a "killer" case — now reaches the pane
// byte-exact through bracketed paste instead of being replaced. Renamed
// from ...AutoFileBoundary..., which would otherwise assert a pointer
// swap that no longer happens on the live path.
func TestInjectPasteBoundaryAndKillerBody(t *testing.T) {
	tests := []struct {
		name      string
		socket    string
		threshold int
	}{
		{name: "claude", socket: "cc-auto-file", threshold: 720},
		{name: "codex", socket: "cx-auto-file", threshold: 900},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			underFake := &fakeTmux{capture: "conversation\n❯ ", submitOnEnter: true}
			if strings.HasPrefix(test.socket, "cx-") {
				underFake.capture = "conversation\n› "
			}
			under := newTestEngine(t, test.socket, underFake)
			under.options.DisableSignature = true
			underBody := strings.Repeat("u", test.threshold-1)
			underResult, err := under.Inject(context.Background(), Request{
				Target: "chat", Message: underBody,
			})
			if err != nil || underResult.Code != 0 ||
				len(underFake.literals) == 0 ||
				underFake.literals[0] != underBody ||
				underFake.pasted ||
				strings.Contains(underResult.Message, "AUTO-FILE") ||
				strings.Contains(underResult.Message, "PASTE") {
				t.Fatalf(
					"one-under delivery result=%+v literals=%q pasted=%v err=%v",
					underResult,
					underFake.literals,
					underFake.pasted,
					err,
				)
			}

			overFake := &fakeTmux{capture: underFake.capture, submitOnEnter: true}
			over := newTestEngine(t, test.socket, overFake)
			over.options.DisableSignature = true
			overBody := strings.Repeat("o", test.threshold+1)
			overResult, err := over.Inject(context.Background(), Request{
				Target: "chat", Message: overBody,
			})
			if err != nil || overResult.Code != 0 ||
				overResult.AutoFilePath != "" ||
				strings.Contains(overResult.Message, "AUTO-FILE") ||
				!strings.Contains(overResult.Message, "PASTE") ||
				!overFake.pasted ||
				len(overFake.literals) == 0 ||
				overFake.literals[0] != overBody {
				t.Fatalf("one-over delivery result=%+v literals=%q err=%v", overResult, overFake.literals, err)
			}
			files, globErr := filepath.Glob(filepath.Join(
				os.Getenv("PFM_HOME"), ".local", "state", "pfm", "inject-bodies", "*.md",
			))
			if globErr != nil || len(files) != 0 {
				t.Fatalf("paste delivery that was proven must not also spill a file: files=%q err=%v", files, globErr)
			}

			killerFake := &fakeTmux{capture: overFake.capture, submitOnEnter: true}
			killer := newTestEngine(t, test.socket+"-killer", killerFake)
			killer.options.DisableSignature = true
			killerBody := "killer caption\n" + strings.Repeat("k", 8<<10)
			killerResult, err := killer.Inject(context.Background(), Request{
				Target: "chat", Message: killerBody,
			})
			if err != nil || killerResult.Code != 0 ||
				killerResult.AutoFilePath != "" ||
				len(killerFake.literals) == 0 ||
				killerFake.literals[0] != killerBody ||
				!killerFake.pasted ||
				strings.Contains(killerResult.Message, "AUTO-FILE") {
				t.Fatalf(
					"killer body was not delivered byte-exact via paste: result=%+v literal len=%d err=%v",
					killerResult,
					len(killerFake.literals[0]),
					err,
				)
			}
		})
	}
}

// TestMultiLineLongMessageSurvivesPasteByteExactWithoutMidBodyEnter pins
// Task C item 6: a multi-line body over the inline threshold crosses as ONE
// SendPaste call carrying every embedded newline byte-exact, and the
// engine's own transport layer never decomposes it into a sequence of
// per-line send-keys — the only SendKey calls it issues are the "Enter"s
// from the explicit submit loop AFTER the single paste, never interleaved
// mid-body. Whether a REAL pane treats an embedded newline as literal text
// rather than an early Enter is a bracketed-paste property of the target
// composer (Claude/Codex requesting the terminal mode via \e[?2004h); this
// fake models tmux's argument boundary, not real terminal negotiation — see
// the report's REAL-SESSION gap.
func TestMultiLineLongMessageSurvivesPasteByteExactWithoutMidBodyEnter(t *testing.T) {
	fake := &fakeTmux{capture: "conversation\n❯ ", submitOnEnter: true}
	engine := newTestEngine(t, "cc-multiline-paste", fake)
	engine.options.DisableSignature = true
	body := "line one\nline two\nline three\n" + strings.Repeat("padding line\n", 60)
	result, err := engine.Inject(context.Background(), Request{
		Target: "chat", Message: body,
	})
	if err != nil || result.Code != 0 {
		t.Fatalf("multi-line paste result=%+v err=%v", result, err)
	}
	if !fake.pasted || len(fake.literals) != 1 || fake.literals[0] != body {
		t.Fatalf(
			"multi-line body did not cross as one byte-exact paste call: pasted=%v chunks=%d",
			fake.pasted, len(fake.literals),
		)
	}
	// "C-s" is the routine idle mash-guard sent before ANY delivery, typed
	// and paste alike; it is not paste-specific. What matters here is that
	// no OTHER key appears — in particular no extra "Enter" beyond the
	// submit loop's own single Enter per attempt, which would signal the
	// engine split the paste into a per-line typed sequence instead of the
	// one SendPaste call already asserted above.
	for _, key := range fake.keys {
		if key != "Enter" && key != "C-s" {
			t.Fatalf("unexpected key sent around a multi-line paste: %q (keys=%q)", key, fake.keys)
		}
	}
}

// TestSlashCommandOfAnyLengthUsesPacedLiteralNotPaste pins Task C item 9:
// isHarnessCommand is checked BEFORE the size comparison in the transport
// ladder (commandTransport short-circuits pasteTransport), so a "/"-prefixed
// command far over the inline threshold still takes the paced-literal chunk
// transport TESTPLAN.md:446 already pins — never paste, and never the
// auto-file rescue.
func TestSlashCommandOfAnyLengthUsesPacedLiteralNotPaste(t *testing.T) {
	fake := &fakeTmux{capture: "conversation\n❯ ", submitOnEnter: true}
	engine := newTestEngine(t, "cc-command-any-length", fake)
	engine.options.DisableSignature = true
	command := "/status " + strings.Repeat("focus text ", 300) // far over ClaudeInlineMax (720)
	result, err := engine.Inject(context.Background(), Request{
		Target: "chat", Message: command,
	})
	if err != nil || result.Code != 0 {
		t.Fatalf("command inject result=%+v err=%v", result, err)
	}
	if fake.pasted {
		t.Fatalf("a slash command was routed into the paste transport: literals=%q", fake.literals)
	}
	if len(fake.literals) < 2 {
		t.Fatalf("a command far over the chunk size was not paced into multiple literal chunks: %d", len(fake.literals))
	}
	if joined := strings.Join(fake.literals, ""); joined != command {
		t.Fatalf("paced literal chunks lost bytes: got %d chars, want %d", len(joined), len(command))
	}
	if result.AutoFilePath != "" {
		t.Fatalf("a command routed through the auto-file rescue: %+v", result)
	}
}

// TestPasteRescuePersistsUnprovenDeliveryAndTellsTheCaller pins Task C item
// 8: when a paste delivery cannot be proven (no tail match, no placeholder),
// pasteRescue fires — the body is preserved to the SAME auto-file store the
// resume path uses, AutoFilePath is populated, and the path is folded into
// Result.Message, so the caller is told rather than silently handed a
// pointer nobody mentioned.
func TestPasteRescuePersistsUnprovenDeliveryAndTellsTheCaller(t *testing.T) {
	fake := &fakeTmux{
		capture:       "conversation\n❯ ",
		submitOnEnter: true,
		// The post-submit proof capture shows a cleared composer that
		// carries neither the message tail nor a paste placeholder — an
		// unprovable delivery.
		proofCapture: "conversation continues elsewhere\n❯ ",
	}
	engine := newTestEngine(t, "cc-paste-rescue", fake)
	body := strings.Repeat("unprovable paste body ", 60) // over ClaudeInlineMax (720)
	result, err := engine.Inject(context.Background(), Request{
		Target: "chat", Message: body,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !fake.pasted {
		t.Fatalf("expected the paste transport for an over-threshold body")
	}
	if result.Status != "delivered_unproven" || result.AutoFilePath == "" ||
		!strings.Contains(result.Message, result.AutoFilePath) ||
		!strings.Contains(result.Message, "AUTO-FILE RESCUE") {
		t.Fatalf("unproven paste delivery did not rescue to a reported file: result=%+v", result)
	}
	stored, readErr := os.ReadFile(result.AutoFilePath)
	if readErr != nil || string(stored) != body {
		t.Fatalf("rescued body=%d bytes err=%v, want the exact original request message", len(stored), readErr)
	}
}

// TestPasteRescueFailureIsReportedNotSwallowed covers pasteRescue's own
// failure branch: when even the rescue write cannot land, the caller is told
// "AUTO-FILE RESCUE FAILED" with the underlying error, never silently
// dropped. The fence runs as root, so a chmod-based permission failure is a
// no-op for every uid; ENOTDIR from a regular file occupying the rescue
// root is fail-closed regardless of uid.
func TestPasteRescueFailureIsReportedNotSwallowed(t *testing.T) {
	fake := &fakeTmux{
		capture:       "conversation\n❯ ",
		submitOnEnter: true,
		proofCapture:  "conversation continues elsewhere\n❯ ",
	}
	engine := newTestEngine(t, "cc-paste-rescue-fail", fake)
	blocked := filepath.Join(t.TempDir(), "rescue-root-is-a-file")
	if err := os.WriteFile(blocked, []byte("occupying the rescue root"), 0o600); err != nil {
		t.Fatal(err)
	}
	engine.options.BodyRoot = blocked
	body := strings.Repeat("unprovable paste body ", 60)
	result, err := engine.Inject(context.Background(), Request{
		Target: "chat", Message: body,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "delivered_unproven" || result.AutoFilePath != "" ||
		!strings.Contains(result.Message, "AUTO-FILE RESCUE FAILED") {
		t.Fatalf("rescue failure was not reported: result=%+v", result)
	}
}

// TestLongProseAutoFilePreservesBodySignatureAndProof covers the other side
// of the unlimited-inject contract: plain prose over the inline threshold
// now travels whole through bracketed paste — byte-exact body, signature,
// and proof all survive — instead of being replaced by a short signed
// pointer. The name is kept (rather than renamed to ...Paste...) because the
// coverage it pins is unchanged: "long bodies deliver intact with signature
// and proof"; only the transport carrying that guarantee changed.
func TestLongProseAutoFilePreservesBodySignatureAndProof(t *testing.T) {
	body := "long prose payload\n" + strings.Repeat("byte-exact body ", 400)
	fake := &fakeTmux{
		capture:       "conversation\n❯ ",
		submitOnEnter: true,
		submitCapture: "USER: {{MESSAGE}}\n❯ ",
	}
	engine := newTestEngine(t, "cc-long-prose", fake)
	result, err := engine.Inject(context.Background(), Request{
		Target:  "chat",
		Message: body,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 0 || result.Status != "delivered" ||
		result.AutoFilePath != "" || result.Unsigned {
		t.Fatalf("long prose result=%+v err=%v", result, err)
	}
	if !fake.pasted {
		t.Fatalf("long prose body did not use the bracketed-paste transport: literals=%q", fake.literals)
	}
	if len(fake.literals) != 1 {
		t.Fatalf("paste delivery used unexpected transport chunks: %d", len(fake.literals))
	}
	expectedSigned, expectedUnsigned := engine.signedMessage(context.Background(), body, false)
	if expectedUnsigned {
		t.Fatalf("test fixture produced an unsigned message; adjust the sender fixture")
	}
	delivered := fake.literals[0]
	if delivered != expectedSigned ||
		!strings.HasPrefix(delivered, body) ||
		!strings.Contains(delivered, "to reply: chat_inject Operator <message>") {
		t.Fatalf(
			"paste transport changed body/signature semantics: got %d bytes, want %d bytes matching engine.signedMessage; delivered=%q",
			len(delivered),
			len(expectedSigned),
			delivered,
		)
	}
	if strings.Contains(result.Message, "AUTO-FILE") ||
		!strings.Contains(result.Message, "PASTE") ||
		!strings.Contains(result.Proof, "USER: long prose payload") {
		t.Fatalf("paste receipt/proof lost delivery evidence: result=%+v", result)
	}
}

func TestPersistBodyPrunesExpiredMarkdownByAge(t *testing.T) {
	engine := newTestEngine(t, "cc-body-prune", &fakeTmux{capture: "❯ "})
	now := time.Date(2031, 2, 3, 4, 5, 6, 7, time.UTC)
	engine.options.Now = func() time.Time { return now }
	engine.options.BodyMaxAge = 24 * time.Hour
	if err := os.MkdirAll(engine.options.BodyRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(engine.options.BodyRoot, "stale.md")
	fresh := filepath.Join(engine.options.BodyRoot, "fresh.md")
	nonBody := filepath.Join(engine.options.BodyRoot, "stale.txt")
	for _, path := range []string{stale, fresh, nonBody} {
		if err := os.WriteFile(path, []byte(path), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := now.Add(-48 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(nonBody, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(fresh, now, now); err != nil {
		t.Fatal(err)
	}
	path, warnings, err := engine.persistBody("exact body", "cc-1:%2")
	if err != nil || len(warnings) != 0 {
		t.Fatalf("persist body path=%q warnings=%q err=%v", path, warnings, err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("expired markdown survived: %v", err)
	}
	for _, kept := range []string{fresh, nonBody} {
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("non-expired/non-body file %q was pruned: %v", kept, err)
		}
	}
	stored, err := os.ReadFile(path)
	if err != nil || string(stored) != "exact body" ||
		!strings.Contains(filepath.Base(path), "cc-1--2") {
		t.Fatalf("canonical file path=%q body=%q err=%v", path, stored, err)
	}
}

func TestPrepareForResumeUsesSignedAutoFilePointer(t *testing.T) {
	engine := newTestEngine(t, "cc-resume-auto-file", &fakeTmux{capture: "❯ "})
	body := "resume caption\n" + strings.Repeat("r", 8<<10)
	prepared, err := engine.PrepareForResume(
		context.Background(),
		"11111111-2222-4333-8444-555555555555",
		"cc",
		body,
	)
	if err != nil || prepared.AutoFilePath == "" ||
		!strings.Contains(prepared.Message, "resume caption — read ") ||
		!strings.Contains(prepared.Message, " fully\n\n— sid 12345678") {
		t.Fatalf("resume preparation=%+v err=%v", prepared, err)
	}
	stored, err := os.ReadFile(prepared.AutoFilePath)
	if err != nil || string(stored) != body {
		t.Fatalf("resume canonical body=%d bytes err=%v", len(stored), err)
	}
}

func TestInjectShortExplicitFileUsesPasteTransport(t *testing.T) {
	fake := &fakeTmux{capture: "› ", submitOnEnter: true}
	engine := newTestEngine(t, "cx-1-2-3", fake)
	message := strings.Repeat("file body ", 20)
	result, err := engine.Inject(context.Background(), Request{
		Target:     "chat",
		Message:    message,
		FileBacked: true,
	})
	if err != nil || result.Code != 0 || result.Status != "delivered" {
		t.Fatalf("file-backed result=%+v err=%v", result, err)
	}
	if !fake.pasted {
		t.Fatal("file-backed delivery used inline send-keys instead of paste transport")
	}
}

// The render-settle probe is advisory. Terminal echo can disappear while the
// TUI still accepted the literal bytes; leaving those bytes in the composer
// without an Enter is the worst possible failure because every retry stacks a
// second body on top of the first.
func TestRenderSettleMissStillFallsThroughToEnter(t *testing.T) {
	fake := &fakeTmux{
		capture:       "conversation\n❯ ",
		killLiteral:   true,
		submitOnEnter: true,
	}
	engine := newTestEngine(t, "cc-1-2-3", fake)
	result, err := engine.Inject(context.Background(), Request{
		Target:  "chat",
		Message: "terminal echo is deliberately killed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 0 || !contains(fake.keys, "Enter") {
		t.Fatalf("render miss stranded typed text: result=%+v keys=%q", result, fake.keys)
	}
}

// tmux reports the foreground descendant, not the pane launcher. Codex and
// Claude legitimately show node/python/sleep while a tool runs, but their
// busy composers remain safe queue surfaces.
func TestBusyQueueIgnoresForegroundDescendant(t *testing.T) {
	fake := &fakeTmux{
		capture:       "Working (600s · 99 tokens)\n❯ ",
		paneCommand:   "python3",
		submitOnEnter: true,
	}
	engine := newTestEngine(t, "cc-1-2-3", fake)
	result, err := engine.Inject(context.Background(), Request{
		Target:  "chat",
		Message: "queue while the descendant owns the foreground",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 0 || result.Status != "queued" || !result.Typed {
		t.Fatalf("busy descendant was refused: %+v", result)
	}
	if contains(fake.keys, "Escape") {
		t.Fatalf("ordinary queue interrupted the pane: keys=%q", fake.keys)
	}
}

func TestDeliveryProofClassificationMatrix(t *testing.T) {
	tests := []struct {
		name          string
		socket        string
		paneCommand   string
		initial       string
		afterSubmit   string
		fileBacked    bool
		wantStatus    string
		wantProofText string
	}{
		{
			name:          "claude idle message is past the composer",
			socket:        "cc-proof-idle",
			paneCommand:   "claude",
			initial:       "conversation\n❯ ",
			afterSubmit:   "conversation\nUSER: {{MESSAGE}}\n❯ ",
			wantStatus:    "delivered",
			wantProofText: "USER: proof body",
		},
		{
			name:        "claude busy queue indicator",
			socket:      "cc-proof-queue",
			paneCommand: "claude",
			initial:     "Working (2s · 9 tokens)\n❯ ",
			afterSubmit: "Working (2s · 9 tokens)\nQUEUED: {{MESSAGE}}\n" +
				"Press up to edit queued messages\n❯ ",
			wantStatus:    "queued",
			wantProofText: "Press up to edit queued messages",
		},
		{
			name:          "codex idle message is past the composer",
			socket:        "cx-proof-idle",
			paneCommand:   "codex",
			initial:       "conversation\n› ",
			afterSubmit:   "conversation\nUSER: {{MESSAGE}}\n› ",
			wantStatus:    "delivered",
			wantProofText: "USER: proof body",
		},
		{
			name:        "codex busy queue follows process not socket spelling",
			socket:      "cc-proof-codex-queue",
			paneCommand: "codex",
			initial:     "Working (2s · 9 tokens) · esc to interrupt\n› ",
			afterSubmit: "Working (2s · 9 tokens)\nPending message\n" +
				"{{MESSAGE}}\n› ",
			wantStatus:    "queued",
			wantProofText: "Pending message",
		},
		{
			name:          "file-backed body is past the composer",
			socket:        "cx-proof-file",
			paneCommand:   "codex",
			initial:       "conversation\n› ",
			afterSubmit:   "conversation\nUSER: {{MESSAGE}}\n› ",
			fileBacked:    true,
			wantStatus:    "delivered",
			wantProofText: "USER: proof body",
		},
		{
			name:          "cleared composer without pane evidence is honest",
			socket:        "cc-proof-unproven",
			paneCommand:   "claude",
			initial:       "conversation\n❯ ",
			afterSubmit:   "conversation unchanged\n❯ ",
			wantStatus:    "delivered_unproven",
			wantProofText: "conversation unchanged",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := &fakeTmux{
				capture:       test.initial,
				paneCommand:   test.paneCommand,
				submitOnEnter: true,
				submitCapture: test.afterSubmit,
			}
			engine := newTestEngine(t, test.socket, fake)
			result, err := engine.Inject(context.Background(), Request{
				Target:     "chat",
				Message:    "proof body",
				FileBacked: test.fileBacked,
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.Code != 0 || result.Status != test.wantStatus {
				t.Fatalf("Inject() = %+v", result)
			}
			if !strings.Contains(result.Proof, test.wantProofText) {
				t.Fatalf("proof %q lacks %q", result.Proof, test.wantProofText)
			}
			if test.wantStatus == "delivered_unproven" &&
				!strings.Contains(result.Message, "delivered-unproven") {
				t.Fatalf("unproven receipt = %q", result.Message)
			}
		})
	}
}

func TestBlindDeliveryProofStatesMissingComposerLine(t *testing.T) {
	fake := &fakeTmux{
		capture:       "conversation\n❯ ",
		submitOnEnter: true,
		submitCapture: "response started\n❯ ",
		proofCapture:  "terminal redraw without a visible prompt",
	}
	engine := newTestEngine(t, "cc-proof-blind", fake)
	result, err := engine.Inject(context.Background(), Request{
		Target:  "chat",
		Message: "blind proof body",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 0 || result.Status != "delivered_unproven" ||
		!strings.Contains(result.Message, "no composer line") {
		t.Fatalf("blind proof receipt = %+v", result)
	}
}

func TestSuccessReceiptNamesForceAndStashedDraft(t *testing.T) {
	tests := []struct {
		name    string
		fake    *fakeTmux
		request Request
		want    string
	}{
		{
			name: "force now",
			fake: &fakeTmux{
				capture:       "Working (3s · 4 tokens)",
				busyUntilEsc:  true,
				submitOnEnter: true,
			},
			request: Request{Target: "chat", Message: "urgent", ForceNow: true},
			want:    "FORCE-NOW: interrupted",
		},
		{
			name: "stashed draft",
			fake: &fakeTmux{
				capture:       "conversation\n❯ existing draft",
				stashClears:   true,
				submitOnEnter: true,
			},
			request: Request{Target: "chat", Message: "next"},
			want:    "stashed and restored",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine := newTestEngine(t, "cc-receipt", test.fake)
			result, err := engine.Inject(context.Background(), test.request)
			if err != nil {
				t.Fatal(err)
			}
			if result.Code != 0 || !strings.Contains(result.Message, test.want) {
				t.Fatalf("receipt = %+v, want %q", result, test.want)
			}
		})
	}
}

func TestInjectCarriesSuccessfulResolverNote(t *testing.T) {
	fake := &fakeTmux{capture: "conversation\n❯ ", submitOnEnter: true}
	engine := newTestEngine(t, "cc-resolve-note", fake)
	engine.resolver = fakeResolver{
		socket: "/tmp/tmux-jail/cc-resolve-note",
		target: "%1",
		detail: "resolved 🔖 label 'QA' → pane '%1'",
	}
	result, err := engine.Inject(context.Background(), Request{
		Target:  "QA",
		Message: "next",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 0 || result.ResolutionNote != "resolved 🔖 label 'QA' → pane '%1'" {
		t.Fatalf("resolution note lost: %+v", result)
	}
}

func TestResolvePreservesAmbiguousVerdict(t *testing.T) {
	engine := newTestEngine(t, "cx-ambiguous", &fakeTmux{})
	engine.resolver = fakeResolver{code: 2, detail: "two live panes match"}
	_, code, detail, err := engine.Resolve(context.Background(), "duplicate")
	if err != nil || code != CodeAmbiguous || detail != "two live panes match" {
		t.Fatalf("Resolve() code=%d detail=%q err=%v", code, detail, err)
	}
}

// TestSingleEnterConfirmsSubmitAcrossCodexAndBusyQueues pins Task B: the
// engine sends exactly ONE Enter per attempt of the submit loop. The former
// unconditional second Enter (fired whenever !base.DraftStashed) is what let
// the operator's own next keystroke land as a submitted message once the
// first Enter had already cleared the composer — see
// the 2026-09-03 eaten-draft incident. One Enter must still confirm
// submission across every composer shape the old two-Enter defense claimed
// to cover.
func TestSingleEnterConfirmsSubmitAcrossCodexAndBusyQueues(t *testing.T) {
	tests := []struct {
		name    string
		socket  string
		capture string
	}{
		{name: "idle codex", socket: "cx-enter", capture: "conversation\n› "},
		{name: "busy claude queue", socket: "cc-enter", capture: "Working (4s · 2 tokens)\n❯ "},
		{name: "busy codex queue", socket: "cx-enter", capture: "Working (4s · 2 tokens)\n› "},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := &fakeTmux{capture: test.capture, submitOnEnter: true}
			engine := newTestEngine(t, test.socket, fake)
			result, err := engine.Inject(context.Background(), Request{
				Target:  "chat",
				Message: "submit defensively",
			})
			if err != nil || result.Code != 0 {
				t.Fatalf("delivery result=%+v err=%v", result, err)
			}
			enters := 0
			for _, key := range fake.keys {
				if key == "Enter" {
					enters++
				}
			}
			if enters != 1 {
				t.Fatalf("keys=%q, want exactly one Enter per submit attempt", fake.keys)
			}
		})
	}
}

// TestEnterConfirmsWhenPostSubmitComposerRowIsNotVisible pins the ROOT CAUSE
// of F3 of the merge-gating review: the real jailed
// TestMCPHandshakeAndAllToolsOverJailedStdio suite flaked on a genuinely
// submitted oversize (1MB) bracketed-paste body, reporting
// "typed_unconfirmed ... after 12 Enter attempts" even though the message
// had actually landed. The old confirm loop read `input == ""` (no visible
// composer row at all — exactly what a real terminal shows once a giant
// echoed body has scrolled the composer's own "❯"/"›" marker off the
// viewport) as "not yet confirmed, retry," then made it WORSE with a
// scrollback fallback that could only ever find the ORIGINAL pre-submit
// composer line (still holding the placeholder) sitting further back in
// history — so a genuinely successful submission could NEVER confirm no
// matter how many Enters were sent. A blank/unreadable composer row is not
// evidence the message is still sitting there: only a capture that
// POSITIVELY shows the message (its prefix or an unexpanded paste
// placeholder) still in the composer may hold the loop back for another
// Enter. Revert the confirm check in engine.go's Enter loop to the old
// `input == "" || HasPastePlaceholder(input) { continue }` shape and this
// fails: the loop exhausts EnterTries with `submitted` never set, even
// though fake.submitted is already true.
func TestEnterConfirmsWhenPostSubmitComposerRowIsNotVisible(t *testing.T) {
	fake := &fakeTmux{
		capture:       "conversation\n❯ ",
		submitOnEnter: true,
		// No "❯"/"›" anywhere in this template: models a real terminal
		// where the post-submit echo (or the tail of the still-flushing
		// oversize body) has pushed the composer's own marker row off the
		// visible viewport — genuinely submitted, but with no composer line
		// for lastComposerLine to find.
		submitCapture: "(scrolled output with no composer row visible)\nUSER:{{MESSAGE}}",
	}
	engine := newTestEngine(t, "cc-enter-offscreen", fake)
	result, err := engine.Inject(context.Background(), Request{
		Target:  "chat",
		Message: "a message long enough to scroll the marker off-screen",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 0 || result.Status == "typed_unconfirmed" {
		t.Fatalf(
			"result = %+v, want a confirmed delivery even though the post-submit capture carries no visible composer row",
			result,
		)
	}
	enters := 0
	for _, key := range fake.keys {
		if key == "Enter" {
			enters++
		}
	}
	if enters != 1 {
		t.Fatalf(
			"keys=%q, want exactly one Enter — confirmation must not loop when there is no positive evidence the composer still holds the message",
			fake.keys,
		)
	}
}

// TestEnterConfirmsOnSecondAttemptWhenFirstIsSwallowed is the flip side of
// the fix above (and closes F6): a re-Enter is still legitimate, but ONLY
// when the capture proves the composer STILL holds the message. fakeTmux's
// submitOnEnterAfter models a first Enter the target never actually
// registers (the composer capture keeps showing the original message
// prefix, positive evidence it is still sitting there) and a second Enter
// that does. TestSingleEnterConfirmsSubmitAcrossCodexAndBusyQueues alone
// could never catch a regression here — its fixture confirms on the very
// first Enter unconditionally, so it stays green even if a future change
// made the confirm loop unable to retry at all.
func TestEnterConfirmsOnSecondAttemptWhenFirstIsSwallowed(t *testing.T) {
	fake := &fakeTmux{
		capture:            "conversation\n❯ ",
		submitOnEnter:      true,
		submitOnEnterAfter: 2,
	}
	engine := newTestEngine(t, "cc-enter-swallowed", fake)
	result, err := engine.Inject(context.Background(), Request{
		Target:  "chat",
		Message: "resend until the target actually registers it",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 0 || !result.Typed || result.Status == "typed_unconfirmed" {
		t.Fatalf("result = %+v, want a confirmed delivery once the second Enter registers", result)
	}
	enters := 0
	for _, key := range fake.keys {
		if key == "Enter" {
			enters++
		}
	}
	if enters != 2 {
		t.Fatalf("keys=%q, want exactly two Enters: one swallowed, one that confirmed", fake.keys)
	}
}

func TestBusyPaneStashesExistingDraftBeforeQueueing(t *testing.T) {
	fake := &fakeTmux{
		capture:       "Working (7s · 8 tokens)\n❯ existing draft",
		stashClears:   true,
		submitOnEnter: true,
	}
	engine := newTestEngine(t, "cc-busy-draft", fake)
	result, err := engine.Inject(context.Background(), Request{
		Target:  "chat",
		Message: "queue after preserving the draft",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 0 || result.Status != "queued" || !result.DraftStashed ||
		!contains(fake.keys, "C-s") {
		t.Fatalf("busy draft was not preserved before queueing: result=%+v keys=%q", result, fake.keys)
	}
}

func TestCopyModeIsCancelledAgainAfterOverlayDismissal(t *testing.T) {
	fake := &fakeTmux{
		capture:       "Restore the code\n❯ ",
		inMode:        true,
		modeAfterEsc:  true,
		submitOnEnter: true,
	}
	engine := newTestEngine(t, "cc-copy-mode", fake)
	result, err := engine.Inject(context.Background(), Request{
		Target:  "chat",
		Message: "deliver after overlays",
	})
	if err != nil || result.Code != 0 {
		t.Fatalf("delivery result=%+v err=%v", result, err)
	}
	if fake.cancelModes != 2 || fake.inMode {
		t.Fatalf("copy mode cancellation count=%d inMode=%t", fake.cancelModes, fake.inMode)
	}
}

func TestInjectAdversarialGuardStatesNeverMistypeOnRefusal(t *testing.T) {
	states := []struct {
		socket      string
		capture     string
		paneCommand string
		dead        bool
		code        int
	}{
		{"cc-1-2-3", "❯ 1. Allow\n2. Deny", "claude", false, 6},
		{"cx-1-2-3", "› 1. Allow\n2. Deny", "codex", false, 6},
		{"cc-1-2-3", "❯ draft", "claude", false, 6},
		{"cc-1-2-3", "❯ ", "claude", true, 3},
	}
	for iteration := 0; iteration < 100; iteration++ {
		state := states[iteration%len(states)]
		fake := &fakeTmux{
			capture:     state.capture,
			paneCommand: state.paneCommand,
			dead:        state.dead,
		}
		engine := newTestEngine(t, state.socket, fake)
		before := fake.capture
		result, err := engine.Inject(context.Background(), Request{
			Target:  "chat",
			Message: "must never mistype",
		})
		if err != nil {
			t.Fatalf("iteration %d: %v", iteration, err)
		}
		if result.Code != state.code || result.Typed || fake.literal != "" {
			t.Fatalf("iteration %d: result=%+v literal=%q", iteration, result, fake.literal)
		}
		if result.Code == 4 && fake.capture != before {
			t.Fatalf("iteration %d selector capture changed", iteration)
		}
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// TestResolveAcceptsAQuotedTargetFromTheReplyHint pins both readings of the
// footer. A spaced label is advertised as chat_inject "Delivery Trust"
// <message> so the CLI form sees one argument; a recipient going through the
// MCP tool passes the target as a JSON string and would carry those quotes
// straight into the target. Both must reach the same chat, and neither may
// strip quotes out of a label that genuinely contains them mid-string.
func TestResolveAcceptsAQuotedTargetFromTheReplyHint(t *testing.T) {
	for _, test := range []struct{ in, want string }{
		{`"Delivery Trust"`, "Delivery Trust"},
		{`Delivery Trust`, "Delivery Trust"},
		{`  "P:DO"  `, "P:DO"},
		{`P:DO`, "P:DO"},
		{`cc-1787705979-3980493-30867`, "cc-1787705979-3980493-30867"},
		{`say "hi" now`, `say "hi" now`},
		{`"`, `"`},
		{``, ``},
	} {
		if got := unquoteTarget(test.in); got != test.want {
			t.Fatalf("unquoteTarget(%q) = %q, want %q", test.in, got, test.want)
		}
	}
}

// TestInjectRefusesATypingHumanUnlessForced pins the typist guard (Task A):
// a human at the keyboard is not a safe queue surface, busy or idle. Revert
// the guard block in engine.go's inject() (the ClientActivity call right
// after the busy/queue decision) and the "recent activity refuses" and
// "tmux error aborts" cases fail — the message never types over a human, and
// a tmux read failure never renders as "nobody typing".
func TestInjectRefusesATypingHumanUnlessForced(t *testing.T) {
	tests := []struct {
		name        string
		attached    bool
		activityAgo time.Duration
		forceNow    bool
		draft       string
		clientErr   error
		wantCode    int
		wantStatus  string
		wantDeliver bool
		wantSubstr  []string
		refuseNever string
	}{
		{
			// tmux's client_activity moves on focus events, mouse reports and
			// the terminal's own query replies, not only keystrokes — an
			// attached VS Code tab looks "typing" forever. Only a draft in the
			// composer proves a human mid-sentence.
			name:        "recent activity with an empty composer delivers",
			attached:    true,
			activityAgo: time.Second,
			wantDeliver: true,
		},
		{
			name:        "recent activity with a draft refuses",
			attached:    true,
			activityAgo: time.Second,
			draft:       "❯ half a sentence",
			wantCode:    CodeBusy,
			wantStatus:  "typing",
			wantSubstr:  []string{"%1", "force_now"},
		},
		{
			name:        "stale activity delivers",
			attached:    true,
			activityAgo: 10 * time.Second,
			wantDeliver: true,
		},
		{
			name:        "force now bypasses even recent activity",
			attached:    true,
			activityAgo: time.Second,
			forceNow:    true,
			wantDeliver: true,
		},
		{
			name:        "tmux error aborts, never renders as quiet or typing",
			clientErr:   errors.New("boom: no server running on socket"),
			wantCode:    CodeUndelivered,
			wantStatus:  "undelivered",
			wantSubstr:  []string{"boom: no server running on socket"},
			refuseNever: "typing",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := &fakeTmux{capture: "conversation\n❯ ", submitOnEnter: true}
			if test.draft != "" {
				fake.capture = "conversation\n" + test.draft
			}
			fake.clientAttached = test.attached
			fake.clientErr = test.clientErr
			engine := newTestEngine(t, "cc-typist-guard", fake)
			now := time.Unix(1_700_000_000, 0)
			fake.clientActivity = now.Add(-test.activityAgo)
			engine.options.Now = func() time.Time { return now }
			result, err := engine.Inject(context.Background(), Request{
				Target:   "chat",
				Message:  "ordinary message, not a command",
				ForceNow: test.forceNow,
			})
			if err != nil {
				t.Fatal(err)
			}
			if test.wantDeliver {
				if result.Code != 0 || !result.Typed {
					t.Fatalf("Inject() = %+v, want delivered", result)
				}
				return
			}
			if result.Code != test.wantCode || result.Status != test.wantStatus {
				t.Fatalf("Inject() = %+v, want code=%d status=%q", result, test.wantCode, test.wantStatus)
			}
			if len(fake.keys) != 0 || len(fake.literals) != 0 || fake.literal != "" {
				t.Fatalf(
					"guard typed despite refusing: keys=%q literals=%q literal=%q",
					fake.keys,
					fake.literals,
					fake.literal,
				)
			}
			for _, substr := range test.wantSubstr {
				if !strings.Contains(result.Message, substr) {
					t.Fatalf("refusal %q lacks %q", result.Message, substr)
				}
			}
			if test.refuseNever != "" && strings.Contains(result.Message, test.refuseNever) {
				t.Fatalf(
					"a tmux error rendered as %q, an answer rather than a failure to look: %q",
					test.refuseNever,
					result.Message,
				)
			}
		})
	}
}

// TestInjectRefusesCompactPrimaryPointingToSelfCompact pins Task C's public
// entry point: Inject() refuses ANY /compact primary, with or without a
// then steer, before checkSteerChain or resolve ever run, and its remedy
// names both the MCP tool and the CLI twin. injectChain — the internal path
// DeliverThen's waiter drives — still accepts a /compact primary; that half
// of the guarantee is pinned elsewhere (then_test.go). Revert the ban block
// at the top of Inject() and this fails: the refusal disappears and the
// engine types the primary instead.
func TestInjectRefusesCompactPrimaryPointingToSelfCompact(t *testing.T) {
	fake := &fakeTmux{capture: "conversation\n❯ ", submitOnEnter: true}
	spawner := &fakeSpawner{}
	engine := newTestEngineWith(t, "cc-compact-ban", fake, spawner)
	for _, test := range []struct {
		name string
		then []string
	}{
		{name: "no then", then: nil},
		{name: "with then", then: []string{"resume the wave"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := engine.Inject(context.Background(), Request{
				Target:  "chat",
				Message: "/compact hold: read /tmp/hold.md",
				Then:    test.then,
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.Code != CodeUndelivered || result.Typed {
				t.Fatalf("Inject() = %+v", result)
			}
			for _, want := range []string{"chat_self_compact", "pfm chat self-compact"} {
				if !strings.Contains(result.Message, want) {
					t.Fatalf("refusal %q lacks %q", result.Message, want)
				}
			}
		})
	}
	if len(fake.keys) != 0 || len(fake.literals) != 0 || fake.literal != "" {
		t.Fatalf(
			"a banned /compact primary was typed: keys=%q literals=%q literal=%q",
			fake.keys,
			fake.literals,
			fake.literal,
		)
	}
	// The internal chain path is the one production route left to a /compact
	// primary (DeliverThen -> engine.inject with Chain: true) — it must
	// still accept exactly what Inject() just refused.
	chained, err := engine.injectChain(context.Background(), Request{
		Target:  "chat",
		Message: "/compact hold: read /tmp/hold.md",
		Then:    []string{"resume the wave"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if chained.Code != 0 || !chained.Typed {
		t.Fatalf("injectChain() rejected a /compact primary it must still accept: %+v", chained)
	}
}

// TestSteerLogPathScopedBySocketAsWellAsPane pins Task E: every chat's live
// pane is %0 on its OWN socket, so scoping the log by pane alone collided
// every chat on the machine onto "chat-then-_0.log", each fresh chain
// truncating the log the last chat's forensics depended on. Revert
// steerLogPath to take a bare pane string (dropping the socket component)
// and this fails: two different sockets sharing pane %0 produce the SAME
// path.
func TestSteerLogPathScopedBySocketAsWellAsPane(t *testing.T) {
	engine := newTestEngine(t, "cc-log-path-a", &fakeTmux{})
	first := engine.steerLogPath(Target{SocketPath: "/tmp/tmux-jail/cc-1111", Pane: "%0"})
	second := engine.steerLogPath(Target{SocketPath: "/tmp/tmux-jail/cc-2222", Pane: "%0"})
	if first == second {
		t.Fatalf("two different sockets sharing pane %%0 collided on one log path: %q", first)
	}
	if !strings.Contains(first, "cc_1111") {
		t.Fatalf("log path does not carry its socket basename: %q", first)
	}
	if !strings.Contains(second, "cc_2222") {
		t.Fatalf("log path does not carry its socket basename: %q", second)
	}
	if !strings.HasSuffix(first, "_0.log") || !strings.HasSuffix(second, "_0.log") {
		t.Fatalf("log paths lost the pane component: first=%q second=%q", first, second)
	}

	// F2 of the merge-gating review: joining the raw socket-base and pane
	// with a literal "-" BEFORE sanitizing let a hyphen inside either
	// component alias with the join delimiter, so two distinct (socket,
	// pane) pairs whose hyphen boundary fell in a different place could
	// still collide on one sanitized path. This repo's own socket names are
	// hyphen-joined numeric triples (cmd/pfm/commands.go's freshEngineSocket,
	// "%s%d-%d-%d"), so this is not a contrived pair.
	hyphenA := engine.steerLogPath(Target{SocketPath: "/tmp/tmux-jail/cc-1787705979-3980493", Pane: "30867"})
	hyphenB := engine.steerLogPath(Target{SocketPath: "/tmp/tmux-jail/cc-1787705979", Pane: "3980493-30867"})
	if hyphenA == hyphenB {
		t.Fatalf(
			"two distinct (socket, pane) pairs whose hyphen boundary fell differently collided on one log path: %q (pairs: (%q,%q) and (%q,%q))",
			hyphenA,
			"/tmp/tmux-jail/cc-1787705979-3980493",
			"30867",
			"/tmp/tmux-jail/cc-1787705979",
			"3980493-30867",
		)
	}
}
