package inject

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/naming"
	"hostops/pfm/internal/resolve"
)

type fakeIdentifier struct {
	identity resolve.Identity
	err      error
}

func (fake fakeIdentifier) Identify(context.Context) (resolve.Identity, error) {
	return fake.identity, fake.err
}

func newSignatureEngine(
	t *testing.T,
	socket string,
	tmux *fakeTmux,
	identifier SelfIdentifier,
) *Engine {
	t.Helper()
	return newSignatureEngineWith(t, socket, tmux, identifier, &fakeSpawner{})
}

func newSignatureEngineWith(
	t *testing.T,
	socket string,
	tmux *fakeTmux,
	identifier SelfIdentifier,
	spawner ThenSpawner,
	tune ...func(*Options),
) *Engine {
	t.Helper()
	t.Setenv("PFM_HOME", t.TempDir())
	t.Setenv("PFM_TMUX_DIR", filepath.Join(t.TempDir(), "no-tmux"))
	t.Setenv("PFM_PROC_ROOT", filepath.Join(t.TempDir(), "no-proc"))
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("CODEX_THREAD_ID", "")
	t.Setenv("CHAT_INJECT_SOCKET", "")
	clearStatedSender(t)
	dependencies := Dependencies{
		Resolver: fakeResolver{
			socket: filepath.Join(string(filepath.Separator), "tmp", "tmux-jail", socket),
			target: "%1",
		},
		Tmux:       tmux,
		Spawner:    spawner,
		Identifier: identifier,
		Options: Options{
			Poll:        time.Nanosecond,
			EnterSettle: time.Nanosecond,
			ProofSettle: time.Nanosecond,
			BusyTries:   2,
			StashTries:  2,
			SettleTries: 2,
			EnterTries:  2,
			LockTimeout: time.Second,
			LockPoll:    time.Nanosecond,
			LockMaxHold: time.Second,
			LockRoot:    t.TempDir(),
			ThenLogRoot: t.TempDir(),
		},
	}
	for _, adjust := range tune {
		adjust(&dependencies.Options)
	}
	engine, err := New(dependencies)
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

// clearStatedSender keeps an ambient CHAT_SENDER_* out of the jail: a
// developer running the suite from inside a chat would otherwise sign every
// fixture with their own identity.
func clearStatedSender(t *testing.T) {
	t.Helper()
	t.Setenv(SenderSessionEnv, "")
	t.Setenv(SenderLabelEnv, "")
	t.Setenv(SenderIDEnv, "")
}

// TestSignatureUsesTheSenderStatedByTheSpawningChat is the fix for the defect
// this rung exists for: a --then waiter is detached with setsid, and a codex
// tool shell carries no $TMUX and no session id, so the waiter can derive
// NOTHING and every steer it delivered went out UNSIGNED. The chat resolves
// its identity while it still can and states it to the waiter.
func TestSignatureUsesTheSenderStatedByTheSpawningChat(t *testing.T) {
	fake := &fakeTmux{capture: "conversation\n❯ ", submitOnEnter: true}
	engine := newSignatureEngine(
		t,
		"cc-1-2-3",
		fake,
		fakeIdentifier{err: resolve.ErrNoTmux},
	)
	t.Setenv(SenderSessionEnv, "cx-1700000000-1-1")
	t.Setenv(SenderLabelEnv, "WAVE_ORCHESTRATOR")
	t.Setenv(SenderIDEnv, "019ffd1e-300f-7872")
	result, err := engine.Inject(context.Background(), Request{
		Target:  "chat",
		Message: "dispatch from a detached waiter",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 0 || result.Unsigned {
		t.Fatalf("Inject() = %+v", result)
	}
	for _, want := range []string{
		"sid 019ffd1e",
		"to reply: chat_inject WAVE_ORCHESTRATOR <message>",
	} {
		if !strings.Contains(lastLiteral(fake), want) {
			t.Fatalf("typed text %q lacks %q", lastLiteral(fake), want)
		}
	}
}

// TestSignatureStatesAnUnderivableIdentity covers chat.sh:648-657: when every
// derivation source is empty, NOTHING is sent.
//
// The old behaviour stamped "UNSIGNED — sender identity underivable" on the
// message and delivered it. That was strictly worse than silence: the
// recipient is handed an instruction from nobody and its only defensible
// response is to refuse to act on it, so the delivery bought nothing while
// looking like it had worked. The sender is the only party who can still
// repair the identity and the sender was the one told nothing.
func TestUnderivableIdentityRefusesToSendAtAll(t *testing.T) {
	fake := &fakeTmux{capture: "conversation\n❯ ", submitOnEnter: true}
	engine := newSignatureEngine(
		t,
		"cc-1-2-3",
		fake,
		fakeIdentifier{err: resolve.ErrNoTmux},
	)
	result, err := engine.Inject(context.Background(), Request{
		Target:  "chat",
		Message: "who is speaking",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code == 0 {
		t.Fatalf("an unsigned message was delivered: %+v", result)
	}
	if result.Unsigned {
		t.Fatalf("Unsigned describes what the RECIPIENT got, and it got nothing: %+v", result)
	}
	for _, want := range []string{
		"refusing to send an UNSIGNED message",
		"DETACHED",
		SenderSessionEnv,
		"--allow-unsigned",
	} {
		if !strings.Contains(result.Message, want) {
			t.Errorf("refusal %q does not mention %q", result.Message, want)
		}
	}
	if typed := lastLiteral(fake); strings.Contains(typed, "who is speaking") {
		t.Fatalf("the refused message reached the pane anyway: %q", typed)
	}
}

// The deliberate opt-in keeps the old contract intact: if an operator asks for
// an unsigned send, the message goes out with the absence STATED, never
// silently dropped, so the recipient can still tell it apart from a signed one.
func TestAllowUnsignedStillStatesTheAbsence(t *testing.T) {
	fake := &fakeTmux{capture: "conversation\n❯ ", submitOnEnter: true}
	engine := newSignatureEngineWith(
		t,
		"cc-1-2-3",
		fake,
		fakeIdentifier{err: resolve.ErrNoTmux},
		&fakeSpawner{},
		func(options *Options) { options.AllowUnsigned = true },
	)
	result, err := engine.Inject(context.Background(), Request{
		Target:  "chat",
		Message: "who is speaking",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 0 || !result.Unsigned {
		t.Fatalf("Inject() = %+v", result)
	}
	want := "UNSIGNED — sender identity underivable; " +
		"no tmux session (ambient or via ancestry); no session id"
	if !strings.Contains(lastLiteral(fake), want) {
		t.Fatalf("typed text %q lacks %q", lastLiteral(fake), want)
	}
	if !strings.Contains(result.Proof, want) {
		t.Fatalf("proof %q lacks the unsigned marker", result.Proof)
	}
}

// A long body takes the auto-file path, where the POINTER is signed separately
// from the body. That second signature must be refused too, or the exact shape
// the user saw — an UNSIGNED auto-file pointer ordering a protocol change —
// walks straight through the guard on the body.
func TestUnderivableIdentityRefusesTheAutoFilePointerToo(t *testing.T) {
	fake := &fakeTmux{capture: "conversation\n❯ ", submitOnEnter: true}
	engine := newSignatureEngine(
		t,
		"cc-1-2-3",
		fake,
		fakeIdentifier{err: resolve.ErrNoTmux},
	)
	result, err := engine.Inject(context.Background(), Request{
		Target:  "chat",
		Message: strings.Repeat("protocol change ", 4096),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code == 0 {
		t.Fatalf("an unsigned auto-file pointer was delivered: %+v", result)
	}
	if !strings.Contains(result.Message, "refusing to send an UNSIGNED message") {
		t.Fatalf("refusal %q is not the unsigned refusal", result.Message)
	}
	if result.AutoFilePath != "" {
		t.Fatalf("a refused send still reported an auto-file at %q", result.AutoFilePath)
	}
}

// TestUnderivableIdentityRefusesEvenWhenPasteWouldHaveCarriedIt pins Task
// C's "keep the unsigned refusal intact" requirement for the NEW default
// transport: an over-threshold body that would now route through bracketed
// paste must still refuse before ANY transport is chosen when the sender is
// underivable. refuseUnsigned fires inside prepareLiveMessage's own signing
// attempt, before pasteTransport is ever decided in the engine.go ladder —
// this proves the refusal governs "whatever is actually delivered" rather
// than only the old, narrower auto-file-pointer path.
func TestUnderivableIdentityRefusesEvenWhenPasteWouldHaveCarriedIt(t *testing.T) {
	fake := &fakeTmux{capture: "conversation\n❯ ", submitOnEnter: true}
	engine := newSignatureEngine(
		t,
		"cc-1-2-3",
		fake,
		fakeIdentifier{err: resolve.ErrNoTmux},
	)
	// Far over ClaudeInlineMax (720): the paste transport is what this body
	// would use if identity resolved.
	body := strings.Repeat("unsigned long paste candidate ", 60)
	result, err := engine.Inject(context.Background(), Request{
		Target:  "chat",
		Message: body,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code == 0 {
		t.Fatalf("an unsigned over-threshold body was delivered: %+v", result)
	}
	if !strings.Contains(result.Message, "refusing to send an UNSIGNED message") {
		t.Fatalf("refusal %q is not the unsigned refusal", result.Message)
	}
	if result.AutoFilePath != "" {
		t.Fatalf("a refused send still reported an auto-file at %q", result.AutoFilePath)
	}
	if fake.pasted || len(fake.literals) != 0 {
		t.Fatalf("a refused send still reached a transport: pasted=%v literals=%q", fake.pasted, fake.literals)
	}
}

// TestSignatureUsesAncestryRecoveredIdentity covers chat.sh:65-96: an inject
// from a codex-origin shell — no $TMUX, no session id in this process — still
// signs, because the handle is recovered from the sender's own process chain.
func TestSignatureUsesAncestryRecoveredIdentity(t *testing.T) {
	fake := &fakeTmux{capture: "conversation\n❯ ", submitOnEnter: true}
	engine := newSignatureEngine(t, "cc-1-2-3", fake, fakeIdentifier{
		identity: resolve.Identity{
			Session:    "cx-1700000000-1-1",
			SocketPath: "/tmp/tmux-jail/cx-1700000000-1-1",
			SocketName: "cx-1700000000-1-1",
			Pane:       "%3",
			Engine:     string(pfmengine.Codex),
			ID:         "thread-xyz",
			Source:     "ancestry",
			Recovered:  true,
		},
	})
	result, err := engine.Inject(context.Background(), Request{
		Target:  "chat",
		Message: "recovered sender",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 0 || result.Unsigned {
		t.Fatalf("Inject() = %+v", result)
	}
	for _, want := range []string{
		"sid thread-x",
		"to reply: chat_inject thread-x <message>",
	} {
		if !strings.Contains(lastLiteral(fake), want) {
			t.Fatalf("typed text %q lacks %q", lastLiteral(fake), want)
		}
	}
	if strings.Contains(lastLiteral(fake), "UNSIGNED") {
		t.Fatalf("recovered identity still signed UNSIGNED: %q", lastLiteral(fake))
	}
}

// TestSignatureUsesCodexThreadSeatAfterAncestryMiss covers the final sender
// rung: a Codex tool shell has a thread id but no tmux environment or useful
// process ancestry. The fleet seat lookup must still recover its immutable
// socket handle so the delivery is signed and replyable.
func TestSignatureUsesCodexThreadSeatAfterAncestryMiss(t *testing.T) {
	fake := &fakeTmux{
		capture:       "codex conversation\n› ",
		windowName:    "Delivery Trust",
		submitOnEnter: true,
	}
	engine := newSignatureEngine(
		t,
		"cc-1-2-3",
		fake,
		fakeIdentifier{err: resolve.ErrNoTmux},
	)
	t.Setenv(resolve.CodexThreadEnv, "019ffd1e-300f-7872")
	engine.codexSeat = fakeIdentifier{identity: resolve.Identity{
		Session:    "cc-1700000000-1-1",
		SocketPath: "/tmp/tmux-jail/cc-1700000000-1-1",
		SocketName: "cc-1700000000-1-1",
		Engine:     string(pfmengine.Codex),
		ID:         "019ffd1e-300f-7872",
		Source:     "codex-thread",
		Recovered:  true,
	}}
	result, err := engine.Inject(context.Background(), Request{
		Target:  "chat",
		Message: "codex sender",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 0 || result.Unsigned {
		t.Fatalf("Inject() = %+v", result)
	}
	for _, want := range []string{
		"sid 019ffd1e",
		`to reply: chat_inject "Delivery Trust" <message>`,
	} {
		if !strings.Contains(lastLiteral(fake), want) {
			t.Fatalf("typed text %q lacks %q", lastLiteral(fake), want)
		}
	}
}

// TestSignatureCodexLabelFallsBackToTheWindowName covers chat.sh:104-121: a
// codex chat has no 🔖 statusline, so its label is the tmux window name — the
// human thread name — which is what the reply hint must then advertise, so a
// reply by that label resolves. A window name may contain spaces, so the hint
// quotes it.
func TestSignatureCodexLabelFallsBackToTheWindowName(t *testing.T) {
	fake := &fakeTmux{
		capture:       "codex conversation\n› ",
		windowName:    "Fleet Porter",
		submitOnEnter: true,
	}
	engine := newSignatureEngine(t, "cx-1-2-3", fake, fakeIdentifier{
		identity: resolve.Identity{
			Session:    "cx-1700000000-1-1",
			SocketPath: "/tmp/tmux-jail/cx-1700000000-1-1",
			SocketName: "cx-1700000000-1-1",
			Pane:       "%3",
			Engine:     string(pfmengine.Codex),
		},
	})
	if _, err := engine.Inject(context.Background(), Request{
		Target:  "chat",
		Message: "labelled by window name",
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(lastLiteral(fake), `to reply: chat_inject "Fleet Porter" <message>`) {
		t.Fatalf("typed text %q lacks the codex window-name label", lastLiteral(fake))
	}

	// A real 🔖 statusline always wins — the Claude path is unchanged.
	labelled := &fakeTmux{
		capture:       "🥇 │ 🔖 Operator │ status\nconversation\n❯ ",
		windowName:    "Fleet Porter",
		submitOnEnter: true,
	}
	engine = newSignatureEngine(t, "cc-1-2-3", labelled, fakeIdentifier{
		identity: resolve.Identity{
			Session:    "cc-1700000000-1-1",
			SocketPath: "/tmp/tmux-jail/cc-1700000000-1-1",
			SocketName: "cc-1700000000-1-1",
			Engine:     string(pfmengine.Claude),
		},
	})
	if _, err := engine.Inject(context.Background(), Request{
		Target:  "chat",
		Message: "labelled by statusline",
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(lastLiteral(labelled), "to reply: chat_inject Operator <message>") ||
		strings.Contains(lastLiteral(labelled), "Fleet Porter") {
		t.Fatalf("statusline label lost to the window name: %q", lastLiteral(labelled))
	}
}

// TestCaptureLabelAcceptsTheRetiredMedal keeps 🍀 in the label anchor: the
// account is gone, but chats labelled while it was live still render it.
func TestCaptureLabelAcceptsTheRetiredMedal(t *testing.T) {
	for _, medal := range []string{"🥇", "🥈", "🥉", "🍀"} {
		line := medal + " │ 🔖 Operator │ ctx 12%"
		if label := captureLabel("noise\n" + line + "\nmore"); label != "Operator" {
			t.Fatalf("captureLabel(%q) = %q, want Operator", line, label)
		}
	}
	if label := captureLabel("🔖 Operator │ no medal here"); label != "" {
		t.Fatalf("medal-less line produced a label: %q", label)
	}
}

// fakeNames is a roster that answers only the reverse lookup: ResolveName
// always misses (the raw pane rungs resolve the target), SenderName answers
// with whatever name the test set last — a rename, from the engine's side.
type fakeNames struct {
	mu   sync.Mutex
	name string
	err  error
}

func (fake *fakeNames) ResolveName(context.Context, string, string) (Target, int, string, error) {
	return Target{}, CodeUnknown, "", nil
}

func (fake *fakeNames) SenderName(context.Context, resolve.Identity) (string, bool, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return fake.name, fake.name != "", fake.err
}

func (fake *fakeNames) set(name string, err error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.name, fake.err = name, err
}

func (fake *fakeTmux) setCapture(capture string) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.capture = capture
}

// TestSignatureLabelIsReadPerDeliveryFromTheRosterFirst is the
// LUNA:ORCHESTRATOR incident: one MCP server signed twenty-three messages as
// its tmux socket because its first capture, seconds after a reload, showed
// no statusline, and that blank was cached for the life of the process. The
// label must be read for every delivery, the fleet roster must outrank the
// screen (a rename lands there first), a roster that fails to answer is
// warned about and read around, and the fallback is the sid — never the
// session.
func TestSignatureLabelIsReadPerDeliveryFromTheRosterFirst(t *testing.T) {
	fake := &fakeTmux{capture: "conversation\n❯ ", submitOnEnter: true}
	names := &fakeNames{}
	engine := newSignatureEngine(t, "cc-1-2-3", fake, fakeIdentifier{
		identity: resolve.Identity{
			Session:    "cc-1788256324-1866070-42739",
			SocketPath: "/tmp/tmux-jail/cc-1788256324-1866070-42739",
			SocketName: "cc-1788256324-1866070-42739",
			Pane:       "%0",
			Engine:     string(pfmengine.Claude),
		},
	})
	engine.names = names
	var warnings strings.Builder
	engine.warningWriter = &warnings
	t.Setenv("CLAUDE_CODE_SESSION_ID", "5a3bb7cb-258d-4bdf-8a4e-7b0409fd329c")

	deliver := func(message string) string {
		t.Helper()
		result, err := engine.Inject(context.Background(), Request{Target: "chat", Message: message})
		if err != nil {
			t.Fatal(err)
		}
		if result.Code != 0 || result.Unsigned {
			t.Fatalf("Inject(%q) = %+v", message, result)
		}
		return lastLiteral(fake)
	}

	// Seconds after a reload: no statusline on screen, not in the roster yet.
	first := deliver("first, before the statusline rendered")
	if !strings.Contains(first, "sid 5a3bb7cb · to reply: chat_inject 5a3bb7cb <message>") ||
		strings.Contains(first, "cc-1788256324-1866070-42739") {
		t.Fatalf("blank-screen footer %q, want the sid and never the session", first)
	}

	// The statusline is up now. A label cached at the first delivery would
	// still say nothing here.
	fake.setCapture("🥇 │ 🔖 LUNA:ORCHESTRATOR │ ctx 12%\nconversation\n❯ ")
	second := deliver("second, statusline rendered")
	if !strings.Contains(second, "to reply: chat_inject LUNA:ORCHESTRATOR <message>") {
		t.Fatalf("footer after the statusline appeared %q, want the label read afresh", second)
	}

	// The roster is the registry a peer's reply resolves first; it outranks
	// the screen, and a rename shows there before the statusline catches up.
	names.set("LUNA:RENAMED", nil)
	third := deliver("third, renamed in the roster")
	if !strings.Contains(third, "to reply: chat_inject LUNA:RENAMED <message>") ||
		strings.Contains(third, "LUNA:ORCHESTRATOR") {
		t.Fatalf("footer after a rename %q, want the roster name over the screen", third)
	}

	// A roster that cannot be listed is a reported failure, and the screen
	// still signs the message. (Each submit replaces the fake pane's screen
	// with the delivered text, so the statusline is put back first.)
	names.set("", errors.New("fleet database busy"))
	fake.setCapture("🥇 │ 🔖 LUNA:ORCHESTRATOR │ ctx 12%\nconversation\n❯ ")
	fourth := deliver("fourth, roster unavailable")
	if !strings.Contains(fourth, "to reply: chat_inject LUNA:ORCHESTRATOR <message>") ||
		!strings.Contains(warnings.String(), "pfm: sender label: fleet database busy") {
		t.Fatalf(
			"footer with the roster down %q (warnings %q), want the screen's label and the failure reported",
			fourth,
			warnings.String(),
		)
	}
}

// lastLiteral is the text most recently typed into the fake pane; the fake
// clears its pending literal when a submit lands, so the history is what a
// signature assertion must read.
func lastLiteral(fake *fakeTmux) string {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.literals) == 0 {
		return ""
	}
	return fake.literals[len(fake.literals)-1]
}

// TestSignatureReplyHintCarriesTheLabelNotTheSessionID is the regression for
// the footer that taught every recipient the one address that does not
// resolve. signatureParts advertised sender.Session — a raw tmux session id —
// as the reply target, two lines above stating the sender's 🔖 label; and
// senderLabel's own contract is that the label is carried BARE precisely
// "since the recipient must be able to reply by exactly the label the
// operator gave the chat." A caller obeying the footer replied with the raw
// session id, which arrived in the comms ledger as the target and minted a
// cosmos node named after a socket.
//
// The label here carries a colon on purpose: ':' is tmux's own
// session:window separator, so a fix that interpolated the label into a tmux
// target would break on exactly the labels the operator actually uses
// (P:DO, W:ORCHESTRATOR). The reply hint is a chat_inject argument, never a
// tmux target, and this pins that.
func TestSignatureReplyHintCarriesTheLabelNotTheSessionID(t *testing.T) {
	fake := &fakeTmux{capture: "conversation\n❯ ", submitOnEnter: true}
	engine := newSignatureEngine(
		t,
		"cc-1-2-3",
		fake,
		fakeIdentifier{err: resolve.ErrNoTmux},
	)
	t.Setenv(SenderSessionEnv, "cc-1787705979-3980493-30867")
	t.Setenv(SenderLabelEnv, "P:DO")
	t.Setenv(SenderIDEnv, "019ffd1e-300f-7872")
	result, err := engine.Inject(context.Background(), Request{
		Target:  "chat",
		Message: "reply to me by name",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 0 || result.Unsigned {
		t.Fatalf("Inject() = %+v", result)
	}
	typed := lastLiteral(fake)
	if !strings.Contains(typed, "to reply: chat_inject P:DO <message>") {
		t.Fatalf("footer %q does not tell the recipient to reply by label", typed)
	}
	if strings.Contains(typed, "chat_inject cc-1787705979-3980493-30867") {
		t.Fatalf("footer %q still advertises the raw session id as the reply target", typed)
	}
}

// TestSignatureReplyHintFallsBackToTheSessionWhenUnlabelled keeps the other
// half honest: a chat whose label could not be scraped still has to be
// replyable, and its tmux session name is the only address it has. The raw
// id is legitimate HERE — it is the literal argument a caller must pass —
// and nowhere in a name position.
func TestSignatureReplyHintFallsBackToTheSidNeverTheSession(t *testing.T) {
	fake := &fakeTmux{capture: "conversation\n❯ ", submitOnEnter: true}
	engine := newSignatureEngine(
		t,
		"cc-1-2-3",
		fake,
		fakeIdentifier{err: resolve.ErrNoTmux},
	)
	t.Setenv(SenderSessionEnv, "cc-1787705979-3980493-30867")
	t.Setenv(SenderIDEnv, "019ffd1e-300f-7872")
	result, err := engine.Inject(context.Background(), Request{
		Target:  "chat",
		Message: "no label to offer",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 0 || result.Unsigned {
		t.Fatalf("Inject() = %+v", result)
	}
	typed := lastLiteral(fake)
	if !strings.Contains(typed, "sid 019ffd1e · to reply: chat_inject 019ffd1e <message>") {
		t.Fatalf("unlabelled footer %q does not offer the sid the roster resolves", typed)
	}
	if strings.Contains(typed, "cc-1787705979-3980493-30867") {
		t.Fatalf("footer %q advertises a tmux session — a chat never learns a socket", typed)
	}

	// With neither a label nor an id there is nothing a peer could resolve:
	// the footer says so instead of offering the socket.
	t.Setenv(SenderIDEnv, "")
	nameless := &fakeTmux{capture: "conversation\n❯ ", submitOnEnter: true}
	engine = newSignatureEngineWith(t, "cc-1-2-3", nameless, fakeIdentifier{err: resolve.ErrNoTmux}, &fakeSpawner{},
		func(options *Options) { options.AllowUnsigned = true })
	t.Setenv(SenderSessionEnv, "cc-1787705979-3980493-30867")
	result, err = engine.Inject(context.Background(), Request{Target: "chat", Message: "session only"})
	if err != nil {
		t.Fatal(err)
	}
	typed = lastLiteral(nameless)
	if !result.Unsigned || !strings.Contains(typed, "UNSIGNED") ||
		!strings.Contains(typed, "no name for this seat") || strings.Contains(typed, "to reply:") {
		t.Fatalf("session-only sender = %+v typed %q, want UNSIGNED naming the missing roster name", result, typed)
	}
}

// TestSignatureReplyHintQuotesALabelWithSpaces covers the codex label rung,
// where the label is a tmux WINDOW name and may legitimately contain spaces.
// Advertised bare, "chat_inject Delivery Trust <message>" reads as a target
// plus a stray word.
func TestSignatureReplyHintQuotesALabelWithSpaces(t *testing.T) {
	fake := &fakeTmux{capture: "conversation\n❯ ", submitOnEnter: true}
	engine := newSignatureEngine(
		t,
		"cc-1-2-3",
		fake,
		fakeIdentifier{err: resolve.ErrNoTmux},
	)
	t.Setenv(SenderSessionEnv, "cc-1787705979-3980493-30867")
	t.Setenv(SenderLabelEnv, "Delivery Trust")
	result, err := engine.Inject(context.Background(), Request{
		Target:  "chat",
		Message: "spaces in my name",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 0 || result.Unsigned {
		t.Fatalf("Inject() = %+v", result)
	}
	typed := lastLiteral(fake)
	if !strings.Contains(typed, `to reply: chat_inject "Delivery Trust" <message>`) {
		t.Fatalf("footer %q left a spaced label unquoted", typed)
	}
}

// TestSignatureFooterPlantsNoBookmarkMarkerInTheRecipientsPane is the
// regression for footer label poisoning, caught live: a chat named W5_TESTER
// had "🔖 W5_BUILDER" sitting in its own visible pane, put there by a message
// it RECEIVED. naming.BookmarkLabelFor resolves a pane's label by taking the
// last 🔖 line on screen, so a delivered footer could redefine the recipient's
// identity as its sender's.
//
// The footer's job is to say who spoke and how to answer — the reply hint
// above does both. 🔖 means "this pane's own label" and belongs to the
// statusline alone, so inject stops writing it into somebody else's screen.
func TestSignatureFooterPlantsNoBookmarkMarkerInTheRecipientsPane(t *testing.T) {
	fake := &fakeTmux{capture: "conversation\n❯ ", submitOnEnter: true}
	engine := newSignatureEngine(
		t,
		"cc-1-2-3",
		fake,
		fakeIdentifier{err: resolve.ErrNoTmux},
	)
	t.Setenv(SenderSessionEnv, "cc-1787705979-3980493-30867")
	t.Setenv(SenderLabelEnv, "W5_BUILDER")
	t.Setenv(SenderIDEnv, "019ffd1e-300f-7872")
	result, err := engine.Inject(context.Background(), Request{
		Target:  "chat",
		Message: "do the thing",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 0 || result.Unsigned {
		t.Fatalf("Inject() = %+v", result)
	}
	typed := lastLiteral(fake)
	if strings.Contains(typed, "🔖") {
		t.Fatalf("delivered footer %q plants a 🔖 label in the recipient's pane", typed)
	}
	// The sender is still named — by the one line that tells the recipient
	// what to do with the name.
	if !strings.Contains(typed, "to reply: chat_inject W5_BUILDER <message>") {
		t.Fatalf("footer %q no longer names its sender at all", typed)
	}
	// And what inject writes must be inert to the scraper that reads panes.
	if label := naming.BookmarkLabel(typed); label != "" {
		t.Fatalf("the delivered footer resolves as a pane label: %q", label)
	}
}
