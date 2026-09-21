package reload

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/index"
	"github.com/rezzminator/professor/pfm/internal/inject"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/store"
)

// renameTmux is delayedThenTmux plus a record of EVERY literal typed, in
// order, and a composer that accepts a second prompt after the first was
// submitted — the shape a /rename typed after the --then steer needs.
type renameTmux struct {
	*delayedThenTmux
	literals []string
	// claudeRoot, when set, makes this fake stand in for the harness on the
	// other end of the keystrokes: a /rename it EXECUTES writes the reborn
	// session's custom-title record, exactly as Claude Code does. Left unset,
	// the keystrokes land and nothing is renamed — the state the "typed but
	// NOT confirmed" warning exists for.
	claudeRoot string
	// busyLeft is how many captures after a submit render the engine's busy
	// footer: the steer's turn, still running.
	busyLeft int
	// typedWhileBusy records a literal typed while the pane still read busy —
	// the state a /rename must never be typed in, because a slash command
	// typed into a running turn is conversation text, not a TUI command.
	typedWhileBusy []string
}

// Capture renders the steer's turn as a RUNNING turn for busyLeft polls after
// its submit — the state deliverThen leaves behind and never waits out.
func (tmux *renameTmux) Capture(ctx context.Context, socket, pane string) (string, error) {
	capture, err := tmux.delayedThenTmux.Capture(ctx, socket, pane)
	if err != nil || tmux.busyLeft <= 0 || !tmux.submitted {
		return capture, err
	}
	tmux.busyLeft--
	return capture + "\nWorking on it\n  esc to interrupt\n", nil
}

func (tmux *renameTmux) SendLiteral(ctx context.Context, socket, pane, value string) error {
	if capture, err := tmux.Capture(ctx, socket, pane); err == nil && inject.IsBusy(capture) {
		tmux.typedWhileBusy = append(tmux.typedWhileBusy, value)
	}
	tmux.literals = append(tmux.literals, value)
	tmux.submitted = false
	if name, ok := strings.CutPrefix(value, "/rename "); ok && tmux.claudeRoot != "" {
		writeRebornTranscript(tmux.claudeRoot, name)
	}
	return tmux.delayedThenTmux.SendLiteral(ctx, socket, pane, value)
}

// rebornSID is the session the --new reboot started; its transcript is where
// Claude records the name /rename gave it.
const rebornSID = "33333333-3333-4333-8333-333333333333"

// writeRebornTranscript is the harness half of a /rename: the live session's
// transcript gains the custom-title record every by-name verb reads.
func writeRebornTranscript(claudeRoot, name string) {
	dir := filepath.Join(claudeRoot, "project-x")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	line := `{"type":"custom-title","customTitle":"` + name + `","sessionId":"` + rebornSID + `"}` + "\n"
	_ = os.WriteFile(filepath.Join(dir, rebornSID+".jsonl"), []byte(line), 0o600)
}

const leftBehindSID = "22222222-2222-4222-8222-222222222222"

// writeLeftBehindTranscript lays a Claude transcript down the way the index
// walks them (root/project/<sid>.jsonl) with the record shapes real ones
// carry: a user turn stating its sessionId and a custom-title naming it.
func writeLeftBehindTranscript(t *testing.T, claudeRoot, title string) string {
	t.Helper()
	dir := filepath.Join(claudeRoot, "project-x")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, leftBehindSID+".jsonl")
	lines := `{"type":"user","cwd":"/jail/project","sessionId":"` + leftBehindSID + `","uuid":"u1",` +
		`"timestamp":"2026-09-18T00:00:00.000Z","message":{"role":"user","content":"first prompt"}}` + "\n"
	if title != "" {
		lines += `{"type":"custom-title","customTitle":"` + title + `","sessionId":"` + leftBehindSID + `"}` + "\n"
	}
	if err := os.WriteFile(path, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func runNew(t *testing.T, tmux *renameTmux, home, transcript, name, then string, stderr *bytes.Buffer) Result {
	t.Helper()
	// Run's rename and --then proofs sleep fixed real intervals between polls;
	// on the real clock these tests took 15-34 s each. The fake clock resolves
	// every sleep at once and changes nothing the tests assert.
	fakeClock := clock.NewFake(time.Unix(0, 0))
	var result Result
	var err error
	driveFakeClock(t, fakeClock, func() {
		result, err = runNewOnClock(tmux, home, transcript, name, then, stderr, t.TempDir(), fakeClock)
	})
	if err != nil {
		t.Fatalf("Run(--new) = %v, want success — the reboot happened", err)
	}
	return result
}

func runNewOnClock(
	tmux *renameTmux, home, transcript, name, then string, stderr *bytes.Buffer, sidDir string, clk clock.Clock,
) (Result, error) {
	return Run(
		context.Background(),
		Request{
			Engine:     pfmengine.Claude,
			SocketPath: "/tmp/tmux-1000/probe-reload-new",
			Pane:       "%7",
			PanePID:    700,
			SessionID:  "",
			Transcript: transcript,
			Name:       name,
			CWD:        "/jail/project",
			Account:    1,
			AccountIDs: []int{1},
			Then:       then,
		},
		Options{
			Home:        home,
			SIDDir:      sidDir,
			ClaudeRoots: []string{filepath.Join(home, "projects")},
			Delay:       -1,
			Poll:        -1,
			ExitTries:   2,
			ThenTries:   2,
			IdleTries:   4,
			Clock:       clk,
		},
		tmux,
		promptReadyProc{tmux: tmux.delayedThenTmux},
		stderr,
	)
}

// indexedCustomTitle runs the REAL reader (internal/index) over claudeRoot and
// returns what it resolves the left-behind transcript's CustomTitle to — the
// record this package writes is only right if that reader agrees.
func indexedCustomTitle(t *testing.T, home, claudeRoot string) string {
	t.Helper()
	t.Setenv(paths.EnvHome, home)
	t.Setenv(paths.EnvDB, filepath.Join(home, "state", "fleet.db"))
	t.Setenv(paths.EnvSIDDir, filepath.Join(home, "sid"))
	t.Setenv(paths.EnvTmuxDir, filepath.Join(home, "tmux"))
	t.Setenv("TMUX_TMPDIR", filepath.Join(home, "t"))
	database, err := store.Open()
	if err != nil {
		t.Fatalf("store.Open() = %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	var counters index.Counters
	if err := index.SyncClaude(context.Background(), database, []string{claudeRoot}, &counters); err != nil {
		t.Fatalf("index.SyncClaude() = %v", err)
	}
	transcript, found, err := database.Transcript(context.Background(), leftBehindSID)
	if err != nil || !found {
		t.Fatalf("Transcript(%s) found=%t err=%v", leftBehindSID, found, err)
	}
	return transcript.CustomTitle
}

// TestRunNewRenamesTheRebornChatAfterTheSteerAndRelabelsTheSessionLeftBehind
// is Wave 8 item 7 (beat E1.06): after a --new reboot the name follows the
// LIVE pane — `/rename <name>` typed after the --then steer, so the steer
// stays the first prompt — and the abandoned transcript gains exactly one
// trailing custom-title record the real reader resolves to
// "<name> (before --new)", so no by-name verb reaches the dead session.
func TestRunNewRenamesTheRebornChatAfterTheSteerAndRelabelsTheSessionLeftBehind(t *testing.T) {
	home := t.TempDir()
	claudeRoot := filepath.Join(home, "projects")
	transcript := writeLeftBehindTranscript(t, claudeRoot, "Wave lead")
	before, err := os.ReadFile(transcript)
	if err != nil {
		t.Fatal(err)
	}
	tmux := &renameTmux{delayedThenTmux: &delayedThenTmux{}, claudeRoot: claudeRoot}
	var stderr bytes.Buffer

	result := runNew(t, tmux, home, transcript, "Wave lead", "continue the task", &stderr)

	if !result.New {
		t.Fatalf("result = %+v, want New", result)
	}
	wantTyped := []string{"/exit", "continue the task", "/rename Wave lead"}
	if !reflect.DeepEqual(tmux.literals, wantTyped) {
		t.Fatalf("typed %q, want %q — the rename must follow the steer, never precede it", tmux.literals, wantTyped)
	}
	after, err := os.ReadFile(transcript)
	if err != nil {
		t.Fatal(err)
	}
	wantRecord := `{"type":"custom-title","customTitle":"Wave lead (before --new)","sessionId":"` +
		leftBehindSID + `"}` + "\n"
	if string(after) != string(before)+wantRecord {
		t.Fatalf(
			"transcript after --new:\n%s\nwant the original plus exactly one trailing record:\n%s",
			after,
			wantRecord,
		)
	}
	backups, err := filepath.Glob(filepath.Join(TranscriptBackupDir(home), leftBehindSID+"-*-left-behind.jsonl"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("pre-append backups = %q (%v), want exactly one", backups, err)
	}
	if got := indexedCustomTitle(t, home, claudeRoot); got != "Wave lead (before --new)" {
		t.Fatalf("the index reader resolves the left-behind title to %q, want %q", got, "Wave lead (before --new)")
	}
	if !strings.Contains(stderr.String(), `renamed "Wave lead"`) ||
		!strings.Contains(stderr.String(), `relabelled "Wave lead (before --new)"`) {
		t.Fatalf("worker log does not report both halves: %q", stderr.String())
	}
}

// TestRunNewWithoutANameTypesNothingAndAppendsNothing: a chat named from
// its prompts has no name to carry — the reboot is exactly what it was.
func TestRunNewWithoutANameTypesNothingAndAppendsNothing(t *testing.T) {
	home := t.TempDir()
	transcript := writeLeftBehindTranscript(t, filepath.Join(home, "projects"), "")
	before, err := os.ReadFile(transcript)
	if err != nil {
		t.Fatal(err)
	}
	tmux := &renameTmux{delayedThenTmux: &delayedThenTmux{}}
	var stderr bytes.Buffer

	runNew(t, tmux, home, transcript, "", "continue the task", &stderr)

	if want := []string{"/exit", "continue the task"}; !reflect.DeepEqual(tmux.literals, want) {
		t.Fatalf("typed %q, want %q — nothing after the steer", tmux.literals, want)
	}
	after, err := os.ReadFile(transcript)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("transcript changed with no name to carry:\n%s", after)
	}
	if strings.Contains(stderr.String(), "renamed") || strings.Contains(stderr.String(), "relabelled") {
		t.Fatalf("worker log claims a rename that never happened: %q", stderr.String())
	}
}

// TestRunNewRenamesEvenWithoutASteer: the name follows the pane whether or
// not a --then was given.
func TestRunNewRenamesEvenWithoutASteer(t *testing.T) {
	home := t.TempDir()
	transcript := writeLeftBehindTranscript(t, filepath.Join(home, "projects"), "Wave lead")
	tmux := &renameTmux{delayedThenTmux: &delayedThenTmux{}}
	var stderr bytes.Buffer

	runNew(t, tmux, home, transcript, "Wave lead", "", &stderr)

	if want := []string{"/exit", "/rename Wave lead"}; !reflect.DeepEqual(tmux.literals, want) {
		t.Fatalf("typed %q, want %q", tmux.literals, want)
	}
}

// TestRunNewWarnsWhenTheLeftBehindTranscriptCannotBeLabelled: the append is
// a courtesy to a reboot that already happened — its failure is a warning
// NAMING the transcript, and Run still returns success.
func TestRunNewWarnsWhenTheLeftBehindTranscriptCannotBeLabelled(t *testing.T) {
	home := t.TempDir()
	transcript := filepath.Join(home, "projects", "project-x", leftBehindSID+".jsonl")
	if err := os.MkdirAll(transcript, 0o700); err != nil {
		t.Fatal(err)
	}
	tmux := &renameTmux{delayedThenTmux: &delayedThenTmux{}, claudeRoot: filepath.Join(home, "projects")}
	var stderr bytes.Buffer

	result := runNew(t, tmux, home, transcript, "Wave lead", "continue the task", &stderr)

	if !result.New {
		t.Fatalf("result = %+v, want a successful New reboot despite the failed label", result)
	}
	if want := []string{"/exit", "continue the task", "/rename Wave lead"}; !reflect.DeepEqual(tmux.literals, want) {
		t.Fatalf("typed %q, want %q — the rename does not depend on the label", tmux.literals, want)
	}
	if !strings.Contains(stderr.String(), "NOT relabelled") || !strings.Contains(stderr.String(), transcript) {
		t.Fatalf("worker log does not warn by path: %q", stderr.String())
	}
}

func TestTranscriptTitleReadsTheLastCustomTitleLikeTheIndexReader(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "last custom-title wins",
			path: write("last.jsonl", `{"type":"custom-title","customTitle":"first"}`+"\n"+
				`{"type":"user","message":{"content":"x"}}`+"\n"+
				`{"type":"custom-title","customTitle":"second","sessionId":"s"}`+"\n"),
			want: "second",
		},
		{
			name: "agent-name is the fallback spelling",
			path: write("agent.jsonl", `{"type":"agent-name","agentName":"qa seat"}`+"\n"),
			want: "qa seat",
		},
		{
			name: "no title record",
			path: write("none.jsonl", `{"type":"user","message":{"content":"x"}}`+"\n"),
			want: "",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := TranscriptTitle(test.path)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("TranscriptTitle() = %q, want %q", got, test.want)
			}
		})
	}
	if _, err := TranscriptTitle(filepath.Join(dir, "missing.jsonl")); err == nil {
		t.Fatal("TranscriptTitle(missing) = nil error, want the read error — absence is not an empty title")
	}
}

// TestRunNewRefusesToTypeANameThatIsNotOneLine: the typist sends the name as
// keystrokes, so a line break would submit half a command; /rename never
// records one, so such a record is reported, not typed.
func TestRunNewRefusesToTypeANameThatIsNotOneLine(t *testing.T) {
	home := t.TempDir()
	transcript := writeLeftBehindTranscript(t, filepath.Join(home, "projects"), "Wave lead")
	tmux := &renameTmux{delayedThenTmux: &delayedThenTmux{}}
	var stderr bytes.Buffer

	runNew(t, tmux, home, transcript, "Wave\nlead", "continue the task", &stderr)

	if want := []string{"/exit", "continue the task"}; !reflect.DeepEqual(tmux.literals, want) {
		t.Fatalf("typed %q, want %q — a two-line name must never reach the keyboard", tmux.literals, want)
	}
	if !strings.Contains(stderr.String(), "not one line") {
		t.Fatalf("worker log does not report the refused name: %q", stderr.String())
	}
}

// TestRunNewTypesTheRenameOnlyAfterTheSteersTurnSettles pins F5's first half:
// deliverThen proves the steer was TYPED and submitted, never that its turn
// ended, so the /rename went in while the model was still answering — where a
// slash command is ordinary conversation text, not a TUI command, and the
// chat keeps its auto-name. The rename now waits out that turn through the
// one settled-turn wait this repo has (inject.SettledTurn).
func TestRunNewTypesTheRenameOnlyAfterTheSteersTurnSettles(t *testing.T) {
	home := t.TempDir()
	claudeRoot := filepath.Join(home, "projects")
	transcript := writeLeftBehindTranscript(t, claudeRoot, "Wave lead")
	tmux := &renameTmux{delayedThenTmux: &delayedThenTmux{}, claudeRoot: claudeRoot, busyLeft: 4}
	var stderr bytes.Buffer

	runNew(t, tmux, home, transcript, "Wave lead", "continue the task", &stderr)

	if want := []string{"/exit", "continue the task", "/rename Wave lead"}; !reflect.DeepEqual(tmux.literals, want) {
		t.Fatalf("typed %q, want %q", tmux.literals, want)
	}
	if len(tmux.typedWhileBusy) != 0 {
		t.Fatalf(
			"typed %q into a pane that still read BUSY with the steer's turn — a /rename typed into a running turn is conversation text, not a command",
			tmux.typedWhileBusy,
		)
	}
	if !strings.Contains(stderr.String(), `renamed "Wave lead"`) {
		t.Fatalf("worker log does not report the rename it confirmed: %q", stderr.String())
	}
}

// TestRunNewWarnsWhenTheRenameWasTypedButNeverTookEffect pins F5's second
// half: the success line used to rest on the keystrokes having landed. Here
// they land and nothing renames the chat — no custom-title record appears —
// so the line must be a warning naming the recovery command, never a claim
// that the chat was renamed.
func TestRunNewWarnsWhenTheRenameWasTypedButNeverTookEffect(t *testing.T) {
	home := t.TempDir()
	transcript := writeLeftBehindTranscript(t, filepath.Join(home, "projects"), "Wave lead")
	// claudeRoot unset: the keystrokes land, the harness does nothing with them.
	tmux := &renameTmux{delayedThenTmux: &delayedThenTmux{}}
	var stderr bytes.Buffer

	runNew(t, tmux, home, transcript, "Wave lead", "continue the task", &stderr)

	if want := []string{"/exit", "continue the task", "/rename Wave lead"}; !reflect.DeepEqual(tmux.literals, want) {
		t.Fatalf("typed %q, want %q", tmux.literals, want)
	}
	log := stderr.String()
	if strings.Contains(log, `reborn chat renamed "Wave lead"`) {
		t.Fatalf("worker log claims a rename that never took effect — typed is not renamed: %q", log)
	}
	if !strings.Contains(log, "typed but NOT confirmed") ||
		!strings.Contains(log, `pfm chat name %7 "Wave lead"`) {
		t.Fatalf("worker log does not name the unconfirmed rename and its recovery command: %q", log)
	}
}
