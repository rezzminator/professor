package spawn

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeCodex is a Codex TUI as far as this package can tell: it draws a
// composer, offers /rename only when that exact text is typed, opens its
// rename prompt only on Enter, and refuses an empty name — the four states the
// choreography navigates. The flags turn each capability off so the abort
// paths are driven by a TUI that behaves differently, not by a stubbed
// timeout.
type fakeCodex struct {
	mutex sync.Mutex

	offersRename bool
	opensPrompt  bool
	refusesEmpty bool
	// startupModals is how many full-screen overlays stand between boot and
	// the composer, each cleared by one Escape. A real Codex has at least one
	// (hooks review, trust, plugin notices) and they swallow every keystroke
	// sent to them — the state no stub modelled the first time around.
	startupModals   int
	stuckModal      bool
	trustModal      bool
	dead            bool
	modalAfterReads int
	reads           int
	// dropsEnters is how many of the prompt's Enters the composer swallows
	// before one takes, and deafComposer swallows every one of them. This is
	// the live failure that made a chat sit there holding its orders: the
	// keystroke was sent, the engine never took it, and nothing checked.
	dropsEnters  int
	deafComposer bool
	// silentRename is Codex 0.154: a rename lands without a word on screen —
	// no "Session renamed to", no name in a status line the user's config
	// leaves out. hintOnlyWhenEmpty is its modal: the placeholder hint shows
	// only while the field is empty, so a PRE-FILLED field (any thread that
	// already has a name) shows the dialog title and the old name alone.
	silentRename      bool
	hintOnlyWhenEmpty bool
	// ledger is a Codex home: a rename the modal takes is appended to its
	// session_index.jsonl exactly as Codex's thread/name/set does, so the
	// tests drive the real proof reader.
	ledger string

	sessions []SessionSpec
	keys     []string
	composer string
	stage    string
	name     string
}

// fakeStatusLine is the idle-composer status row, whose token meter is the
// half of readiness a modal cannot fake.
const fakeStatusLine = "  019f · ~/work/alpha · Full Access · Context 0% used · 0 in · 0 out\n"

func newFakeCodex() *fakeCodex {
	return &fakeCodex{offersRename: true, opensPrompt: true, stage: "composer"}
}

func (fake *fakeCodex) NewSession(_ context.Context, spec SessionSpec) error {
	fake.mutex.Lock()
	defer fake.mutex.Unlock()
	fake.sessions = append(fake.sessions, spec)
	return nil
}

func (fake *fakeCodex) Capture(_ context.Context, _, _ string) (string, error) {
	fake.mutex.Lock()
	defer fake.mutex.Unlock()
	if fake.dead {
		return "", errors.New("chat exited")
	}
	if fake.trustModal {
		return "Do you trust the contents of this directory?\n" +
			"› 1. Yes, continue\n  2. No, quit\n\n  Press enter to continue\n", nil
	}
	// modalAfterReads reproduces the exact live failure: Codex paints its
	// composer first and raises its startup modal a beat LATER, so the
	// composer is visible for a flash before the screen is stolen.
	if fake.modalAfterReads > 0 {
		fake.reads++
		if fake.reads > fake.modalAfterReads {
			fake.modalAfterReads = 0
			fake.startupModals++
		}
	}
	if fake.startupModals > 0 || fake.stuckModal {
		// No composer glyph anywhere: this screen eats keystrokes.
		// The selection cursor is the SAME glyph the composer uses — the live
		// false positive this fake exists to reproduce.
		return "codex\n  Hooks\n  1 hook needs review before it can run.\n" +
			"› 2. Trust all and continue\n" +
			"  Press enter to confirm or esc to go back\n", nil
	}
	switch fake.stage {
	case "offered":
		return "codex\n› " + fake.composer +
			"\n  /rename  rename the current thread\n" + fakeStatusLine, nil
	case "prompt":
		// The rename modal paints over the composer, exactly as the real one
		// does — no composer glyph on screen while it is up.
		if fake.hintOnlyWhenEmpty && fake.composer != "" {
			// Codex 0.154's pre-filled dialog, as captured live.
			return "codex\n▌ Rename thread\n▌ Generating a title suggestion…\n▌\n▌ " + fake.composer +
				"\n\nPress enter to confirm or esc to go back\n", nil
		}
		return "codex\n▌ Name thread\n▌\n▌ Type a name and press Enter\n", nil
	case "empty":
		return "codex\n▌ Name thread\n▌ Type a name and press Enter\n" +
			"Thread name cannot be empty.\n", nil
	default:
		header := "codex"
		if fake.name != "" && !fake.silentRename {
			header = "• Session renamed to " + fake.name + ".\ncodex · " + fake.name
		}
		return header + "\n› " + fake.composer + "\n" + fakeStatusLine, nil
	}
}

func (fake *fakeCodex) SendLiteral(_ context.Context, _, _, text string) error {
	fake.mutex.Lock()
	defer fake.mutex.Unlock()
	fake.keys = append(fake.keys, "literal:"+text)
	fake.composer += text
	if fake.stage == "composer" &&
		fake.offersRename &&
		strings.HasPrefix(fake.composer, codexRenameCommand) {
		fake.stage = "offered"
	}
	return nil
}

func (fake *fakeCodex) SendKey(_ context.Context, _, _, key string) error {
	fake.mutex.Lock()
	defer fake.mutex.Unlock()
	fake.keys = append(fake.keys, "key:"+key)
	if fake.trustModal {
		switch key {
		case "Enter":
			fake.trustModal = false
		case "Escape":
			fake.trustModal = false
			fake.dead = true
		}
		return nil
	}
	switch key {
	case "Enter":
		if fake.stage == "composer" && fake.name != "" &&
			(fake.deafComposer || fake.dropsEnters > 0) {
			// A busy engine reading its input in bursts drops the newline that
			// arrives glued to the text.
			if fake.dropsEnters > 0 {
				fake.dropsEnters--
			}
			return nil
		}
		switch fake.stage {
		case "offered":
			// The real rename modal opens PRE-FILLED with the thread's
			// current name, so a spawn that types straight into it appends.
			fake.composer = fake.name
			if fake.opensPrompt {
				fake.stage = "prompt"
			} else {
				fake.composer = ""
				fake.stage = "composer"
			}
		case "prompt":
			if fake.composer == "" && fake.refusesEmpty {
				fake.stage = "empty"
				return nil
			}
			fake.name = fake.composer
			fake.recordRename()
			fake.composer = ""
			fake.stage = "composer"
		default:
			fake.composer = ""
		}
	case "BSpace":
		runes := []rune(fake.composer)
		if len(runes) > 0 {
			fake.composer = string(runes[:len(runes)-1])
		}
	case "Escape":
		if fake.startupModals > 0 {
			fake.startupModals--
			return nil
		}
		fake.composer = ""
		fake.stage = "composer"
	}
	return nil
}

func TestCodexTrustDialogUsesAffirmativeEnter(t *testing.T) {
	fake := newFakeCodex()
	fake.trustModal = true
	result, err := Run(context.Background(), fake, codexRequest())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !result.Named || !result.Prompted || fake.dead {
		t.Fatalf("trust launch result=%#v dead=%v keys=%v", result, fake.dead, fake.keys)
	}
	if len(fake.keys) == 0 || fake.keys[0] != "key:Enter" {
		t.Fatalf("trust dialog keys=%v, want affirmative Enter first", fake.keys)
	}
}

// collapseClears folds a run of BSpace presses into one "clear" token: the
// count is a bound on the field's length, not a contract.
func collapseClears(keys []string) []string {
	collapsed := make([]string, 0, len(keys))
	for _, key := range keys {
		if key == "key:BSpace" {
			if len(collapsed) > 0 && collapsed[len(collapsed)-1] == "clear" {
				continue
			}
			collapsed = append(collapsed, "clear")
			continue
		}
		collapsed = append(collapsed, key)
	}
	return collapsed
}

func testTimings() Timings {
	return Timings{
		Poll:  time.Millisecond,
		Boot:  200 * time.Millisecond,
		Step:  200 * time.Millisecond,
		Typed: time.Nanosecond,
	}
}

func codexRequest() Request {
	return Request{
		Engine:  "cx",
		Name:    "_KILL codex worker",
		Socket:  "cx-1-2-3",
		CWD:     "/work/alpha",
		Run:     "codex",
		Prompt:  "read the incident report",
		Timings: testTimings(),
	}
}

// TestCodexThreadIsRenamedThenPrompted is the whole point of the Codex path:
// the name lands through the engine's own rename UI BEFORE the first prompt
// starts a turn.
func TestCodexThreadIsRenamedThenPrompted(t *testing.T) {
	fake := newFakeCodex()
	result, err := Run(context.Background(), fake, codexRequest())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !result.Named || !result.Prompted || len(result.Warnings) != 0 {
		t.Fatalf("result = %#v", result)
	}
	if fake.name != "_KILL codex worker" {
		t.Fatalf("thread name = %q", fake.name)
	}
	want := []string{
		"literal:/rename",
		"key:Enter",
		"clear",
		"literal:_KILL codex worker",
		"key:Enter",
		"literal:read the incident report",
		"key:Enter",
	}
	if got := collapseClears(fake.keys); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("keystrokes\n got: %v\nwant: %v", got, want)
	}
}

// TestCodexBootsThroughStartupModals is the regression for the bug a stub
// without overlays could never catch: a real Codex boots into a full-screen
// hooks/trust modal that SWALLOWS keystrokes, so a spawn that starts typing at
// the first settled screen loses both the rename and the first prompt. The
// modals must be dismissed and the composer seen before anything is typed.
func TestCodexBootsThroughStartupModals(t *testing.T) {
	fake := newFakeCodex()
	fake.startupModals = 2
	result, err := Run(context.Background(), fake, codexRequest())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !result.Named || !result.Prompted || len(result.Warnings) != 0 {
		t.Fatalf("result = %#v", result)
	}
	if fake.name != "_KILL codex worker" {
		t.Fatalf("thread name = %q", fake.name)
	}
	escapes := 0
	for index, key := range fake.keys {
		if key != "key:Escape" {
			// Every keystroke that is not one of the dismissals must come
			// AFTER the last of them: nothing may be typed into a modal.
			if escapes < 2 {
				t.Fatalf("keystroke %d (%s) was typed before the modals cleared: %v",
					index, key, fake.keys)
			}
			continue
		}
		escapes++
	}
	if escapes != 2 {
		t.Fatalf("dismissals = %d, want 2: %v", escapes, fake.keys)
	}
}

// TestCodexComposerFlashBeforeAModalIsNotReadiness is the live bug itself: the
// composer appeared, the modal painted over it a beat later, and the rename
// went into the modal. One sighting of a composer is not a ready chat.
func TestCodexComposerFlashBeforeAModalIsNotReadiness(t *testing.T) {
	fake := newFakeCodex()
	fake.modalAfterReads = 2
	result, err := Run(context.Background(), fake, codexRequest())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !result.Named || !result.Prompted || len(result.Warnings) != 0 {
		t.Fatalf("result = %#v (warnings must be empty — the modal was survivable)", result)
	}
	if fake.name != "_KILL codex worker" {
		t.Fatalf("thread name = %q", fake.name)
	}
}

// TestCodexStuckAtAStartupScreenTypesNothing: when the overlay will not clear,
// the chat is left strictly alone — an unnamed, unprompted, LIVE chat the user
// is told to go clear by hand beats a name and a prompt fed to a modal.
func TestCodexStuckAtAStartupScreenTypesNothing(t *testing.T) {
	fake := newFakeCodex()
	fake.stuckModal = true
	result, err := Run(context.Background(), fake, codexRequest())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Named || result.Prompted {
		t.Fatalf("result = %#v", result)
	}
	if len(result.Warnings) != 1 ||
		!strings.Contains(result.Warnings[0], "startup screen") {
		t.Fatalf("warnings = %#v", result.Warnings)
	}
	for _, key := range fake.keys {
		if key != "key:Escape" {
			t.Fatalf("something was typed into a startup modal: %v", fake.keys)
		}
	}
}

// TestCodexWithoutARenameCommandStaysUnnamed is the version-drift guard: a
// Codex build that does not offer /rename must leave the composer EMPTY (the
// typed command cleared, never submitted) and report the chat unnamed.
func TestCodexWithoutARenameCommandStaysUnnamed(t *testing.T) {
	fake := newFakeCodex()
	fake.offersRename = false
	request := codexRequest()
	request.Prompt = ""
	result, err := Run(context.Background(), fake, request)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Named {
		t.Fatal("unnamed chat reported as named")
	}
	if len(result.Warnings) != 1 ||
		!strings.Contains(result.Warnings[0], "did not offer /rename") {
		t.Fatalf("warnings = %#v", result.Warnings)
	}
	if fake.composer != "" {
		t.Fatalf("composer still holds %q — it would ride along with the next prompt",
			fake.composer)
	}
	for _, key := range fake.keys {
		if key == "key:Enter" {
			t.Fatalf("an unoffered /rename was submitted to the model: %v", fake.keys)
		}
	}
}

// TestCodexRenamePromptNeverOpensStaysUnnamed covers the middle step failing:
// the command exists but its prompt never appears, so the NAME must not be
// typed into whatever is on screen.
func TestCodexRenamePromptNeverOpensStaysUnnamed(t *testing.T) {
	fake := newFakeCodex()
	fake.opensPrompt = false
	request := codexRequest()
	request.Prompt = ""
	result, err := Run(context.Background(), fake, request)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Named {
		t.Fatal("unnamed chat reported as named")
	}
	if len(result.Warnings) != 1 ||
		!strings.Contains(result.Warnings[0], "never asked for a thread name") {
		t.Fatalf("warnings = %#v", result.Warnings)
	}
	for _, key := range fake.keys {
		if key == "literal:_KILL codex worker" {
			t.Fatalf("the name was typed with no prompt open: %v", fake.keys)
		}
	}
}

// TestCodexRefusingAnEmptyNameIsReportedAsSuch covers the last rename failure
// Codex itself can raise, so the warning names the real cause instead of
// blaming the prompt for never closing.
func TestCodexRefusingAnEmptyNameIsReportedAsSuch(t *testing.T) {
	fake := newFakeCodex()
	fake.refusesEmpty = true
	request := codexRequest()
	request.Name = ""
	request.Prompt = ""
	result, err := Run(context.Background(), fake, request)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Named {
		t.Fatal("an empty name was reported as landed")
	}
	if len(result.Warnings) != 1 ||
		!strings.Contains(result.Warnings[0], "refused the name as empty") {
		t.Fatalf("warnings = %#v", result.Warnings)
	}
}

// TestClaudeNeedsNoChoreography: Claude takes its name on the command line, so
// the spawn must type NOTHING into it.
func TestClaudeNeedsNoChoreography(t *testing.T) {
	fake := newFakeCodex()
	result, err := Run(context.Background(), fake, Request{
		Engine:              "cc",
		Name:                "worker",
		Socket:              "cc-1-2-3",
		CWD:                 "/work/alpha",
		Run:                 "claude --name worker",
		Prompt:              "audit the firewall",
		PromptOnCommandLine: true,
		Timings:             testTimings(),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !result.Named || !result.Prompted {
		t.Fatalf("result = %#v", result)
	}
	if len(fake.keys) != 0 {
		t.Fatalf("keys typed into a Claude chat: %v", fake.keys)
	}
	if len(fake.sessions) != 1 {
		t.Fatalf("sessions = %#v", fake.sessions)
	}
	session := fake.sessions[0]
	if session.Socket != "cc-1-2-3" || session.Session != "cc-1-2-3" ||
		session.Window != "worker" || session.CWD != "/work/alpha" {
		t.Fatalf("session spec = %#v", session)
	}
	if session.Width != 220 || session.Height != 50 {
		t.Fatalf("headless geometry = %dx%d, want 220x50", session.Width, session.Height)
	}
}

// deadPane is a server whose session never comes up — the chat died at birth.
type deadPane struct{ fakeCodex }

func (fake *deadPane) Capture(context.Context, string, string) (string, error) {
	return "", errNoSession{}
}

type errNoSession struct{}

func (errNoSession) Error() string { return "no server running on socket" }

func TestChatThatDiesAtBirthIsReportedAsSuch(t *testing.T) {
	fake := &deadPane{}
	_, err := Run(context.Background(), fake, codexRequest())
	if err == nil {
		t.Fatal("Run() reported a dead chat as healthy")
	}
	if !strings.Contains(err.Error(), "died at birth") {
		t.Fatalf("error = %v", err)
	}
}

func TestWindowName(t *testing.T) {
	for _, testCase := range []struct{ in, want string }{
		{"_KILL worker 3", "_KILL worker 3"},
		{"a:b.c", "a-b-c"},
		{"  spaced   out\t", "spaced out"},
		{"", "chat"},
		{strings.Repeat("x", 60), strings.Repeat("x", 40)},
	} {
		if got := WindowName(testCase.in); got != testCase.want {
			t.Fatalf("WindowName(%q) = %q, want %q", testCase.in, got, testCase.want)
		}
	}
}

// TestCodexPromptIsResentUntilItLeavesTheComposer is the failure this package
// shipped with: the prompt was typed, one Enter was sent, the engine dropped
// it, and pfm reported a working chat that had never been asked anything.
func TestCodexPromptIsResentUntilItLeavesTheComposer(t *testing.T) {
	fake := newFakeCodex()
	fake.dropsEnters = 2
	result, err := Run(context.Background(), fake, codexRequest())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !result.Prompted || len(result.Warnings) != 0 {
		t.Fatalf("result = %#v, want a delivered prompt and no warning", result)
	}
	if fake.composer != "" {
		t.Fatalf("composer still holds %q", fake.composer)
	}
}

// TestCodexPromptThatNeverSubmitsIsReportedUndelivered: when the composer will
// not let go of the text, the chat is still up — and the report says plainly
// that the prompt is sitting in it.
func TestCodexPromptThatNeverSubmitsIsReportedUndelivered(t *testing.T) {
	fake := newFakeCodex()
	fake.deafComposer = true
	result, err := Run(context.Background(), fake, codexRequest())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !result.Named {
		t.Fatalf("result = %#v, want the rename to have landed", result)
	}
	if result.Prompted {
		t.Fatal("Prompted = true for a prompt the composer never released")
	}
	if len(result.Warnings) != 1 ||
		!strings.Contains(result.Warnings[0], "not delivered") {
		t.Fatalf("warnings = %q, want one naming the undelivered prompt", result.Warnings)
	}
}

// recordRename appends the rename to the fake's Codex ledger, the way Codex
// 0.154 records one it never announces on screen. Called with the mutex held.
func (fake *fakeCodex) recordRename() {
	if fake.ledger == "" {
		return
	}
	file, err := os.OpenFile(
		filepath.Join(fake.ledger, "session_index.jsonl"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY,
		0o600,
	)
	if err != nil {
		panic(err)
	}
	defer file.Close()
	fmt.Fprintf(file, "{\"id\":\"fake-thread\",\"thread_name\":%q,\"updated_at\":%q}\n",
		fake.name, time.Now().UTC().Format(time.RFC3339Nano))
}

// useCodexHomes points this process's rename proof at homes for one test.
func useCodexHomes(t *testing.T, homes ...string) {
	t.Helper()
	UseCodexHomes(homes)
	t.Cleanup(func() { codexHomes.Store(nil) })
}

func countKey(keys []string, want string) int {
	count := 0
	for _, key := range keys {
		if key == want {
			count++
		}
	}
	return count
}

// TestCodexSilentRenameIsProvenFromTheIndex is the regression for the live
// PING_PROBE launch on Codex 0.154: the rename landed on the first try
// (session_index.jsonl recorded it three seconds in), but 0.154 no longer
// prints "Session renamed to", so pfm called it unconfirmed, retried /rename
// into a pre-filled modal whose hint never showed, and reported "Codex never
// asked for a thread name" about a chat that was named all along. Codex's own
// ledger is the proof; the screen is only a second witness.
func TestCodexSilentRenameIsProvenFromTheIndex(t *testing.T) {
	fake := newFakeCodex()
	fake.silentRename = true
	fake.hintOnlyWhenEmpty = true
	fake.ledger = t.TempDir()
	useCodexHomes(t, fake.ledger)
	request := codexRequest()
	result, err := Run(context.Background(), fake, request)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !result.Named || !result.Prompted || len(result.Warnings) != 0 {
		t.Fatalf("result = %#v, keys = %v", result, fake.keys)
	}
	if fake.name != request.Name {
		t.Fatalf("thread name = %q, want %q", fake.name, request.Name)
	}
	if got := countKey(fake.keys, "literal:"+codexRenameCommand); got != 1 {
		t.Fatalf(
			"%s typed %d times, want once — a proven rename is never retried: %v",
			codexRenameCommand,
			got,
			fake.keys,
		)
	}
}

// TestCodexRenameOfANamedThreadFindsItsRetitledModal: renaming a thread that
// already has a name (the post-/clear re-apply, any retry) opens the modal
// PRE-FILLED, and on 0.154 a filled field shows its title, not the hint. The
// modal is found by its title, so the rename goes through.
func TestCodexRenameOfANamedThreadFindsItsRetitledModal(t *testing.T) {
	fake := newFakeCodex()
	fake.name = "old name"
	fake.silentRename = true
	fake.hintOnlyWhenEmpty = true
	fake.ledger = t.TempDir()
	useCodexHomes(t, fake.ledger)
	warning, err := RenameCodex(context.Background(), fake, "cx-1-2-3", "cx-1-2-3", "new name", testTimings(), Trace{})
	if err != nil {
		t.Fatalf("RenameCodex() error = %v", err)
	}
	if warning != "" || fake.name != "new name" {
		t.Fatalf("warning = %q, thread name = %q, keys = %v", warning, fake.name, fake.keys)
	}
}

// TestCodexRenameThatCannotBeVerifiedSaysSo: when the ledger cannot be read
// and the screen shows nothing, the verdict is "could not verify" — never
// "unnamed" about a chat that may well be named — and the rename is not
// typed again into a modal that would only repeat the same blind step.
func TestCodexRenameThatCannotBeVerifiedSaysSo(t *testing.T) {
	fake := newFakeCodex()
	fake.silentRename = true
	// A ledger path that exists but cannot be read as a file.
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "session_index.jsonl"), 0o700); err != nil {
		t.Fatal(err)
	}
	useCodexHomes(t, home)
	request := codexRequest()
	result, err := Run(context.Background(), fake, request)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Named {
		t.Fatalf("an unverifiable rename was reported as proven: %#v", result)
	}
	joined := strings.Join(result.Warnings, " | ")
	if !strings.Contains(joined, "could not verify") || !strings.Contains(joined, "session_index.jsonl") ||
		strings.Contains(joined, "unnamed") {
		t.Fatalf("warnings = %q, want the unverifiable rename named with its cause", result.Warnings)
	}
	if got := countKey(fake.keys, "literal:"+codexRenameCommand); got != 1 {
		t.Fatalf("%s typed %d times, want once: %v", codexRenameCommand, got, fake.keys)
	}
}

// TestRenameModalOpenReadsOnlyTheDialog pins the dialog detector against the
// live 0.154 capture, and against its dangerous false positive: the title as
// transcript text under a live composer. Taking that for the dialog would
// clear and type the name into the composer — a prompt sent to the model.
func TestRenameModalOpenReadsOnlyTheDialog(t *testing.T) {
	livePrefilled := "────────────────\n\n▌ Rename thread\n▌ Generating a title suggestion…\n▌\n▌ PING_PROBE\n\n" +
		"Press enter to confirm or esc to go back\n"
	for _, test := range []struct {
		name    string
		capture string
		want    bool
	}{
		{name: "0.154 pre-filled dialog", capture: livePrefilled, want: true},
		{name: "empty-field dialog", capture: "codex\n▌ Name thread\n▌\n▌ Type a name and press Enter\n", want: true},
		{name: "title quoted in transcript under a live composer", capture: "• the dialog says\n▌ Rename thread\n› \n" + fakeStatusLine, want: false},
		{name: "title inside prose, no dialog", capture: "Rename thread is the dialog title\n", want: false},
		{name: "the /rename offer", capture: "codex\n› /rename\n  /rename  rename the current thread\n" + fakeStatusLine, want: false},
	} {
		if got := renameModalOpen(test.capture); got != test.want {
			t.Errorf("%s: renameModalOpen = %v, want %v", test.name, got, test.want)
		}
	}
}
