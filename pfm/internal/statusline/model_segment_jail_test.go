//go:build jail

// Subject: auto-effort's keeper as a real process (kill 0, /proc argv) behind the effort segment.

package statusline

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// While auto-effort's live keeper holds a level, the line shows that
// level marked "requested", the same in every mode: the plugin rewrites each
// request's effort, so the payload's session level is not what is sent. Off, no
// level, no file or no live keeper reads exactly as without the plugin; only a
// broken state file adds ERR. Thinking is separate.
func TestModelSegmentCarriesEffort(t *testing.T) {
	withSession := strings.Replace(styledPayload, `"session_name"`, `"session_id":"sess-1","session_name"`, 1)
	withoutEffort := strings.Replace(withSession, `"effort":{"level":"high"},`, "", 1)
	thinkingOff := strings.Replace(withSession, `"enabled":true`, `"enabled":false`, 1)
	atLevel := func(level string) string {
		return strings.Replace(withSession, `"effort":{"level":"high"}`, `"effort":{"level":"`+level+`"}`, 1)
	}
	byTranscript := strings.Replace(
		styledPayload,
		`"session_name"`,
		`"transcript_path":"/work/projects/sess-2.jsonl","session_name"`,
		1,
	)
	keeper := startKeeper(t)
	live := strconv.Itoa(keeper)
	dead := strconv.Itoa(reapedPID(t))
	self := strconv.Itoa(os.Getpid())
	// line is the plugin's state line with its keeper ("" leaves the key out).
	line := func(mode, level, keeper string) string {
		keeperField := ""
		if keeper != "" {
			keeperField = `,"keeper":` + keeper
		}
		return `{"version":3,"mode":"` + mode + `","level":` + level + `,"reason":"a lookup over logs"` +
			keeperField + `,"updatedAt":"2026-10-17T09:30:00.000Z"}` + "\n"
	}
	state := func(mode, level string) string { return line(mode, level, live) }
	const sess1 = ".local/state/auto-effort/state.sess-1.json"
	for _, testcase := range []struct {
		name, payload, file, content, want, colour string
		// barColour is the level's own colour on the bar of a segment in the
		// effort colour; ERR, STALE and a level-less segment leave it "": their
		// bar wears the segment's colour.
		barColour                                 string
		directory, fifo, wantWarn, unreadableArgs bool
	}{
		{name: "auto at medium", payload: atLevel("medium"), file: sess1, content: state("auto", `"medium"`), want: "◆ Opus 4·▰▰ medium · requested │ ", colour: cEffort, barColour: cGood},
		{name: "auto at low", payload: atLevel("low"), file: sess1, content: state("auto", `"low"`), want: "◆ Opus 4·▰ low · requested │ ", colour: cEffort, barColour: cWindow},
		{name: "auto before the first request shows the session's level", payload: withSession, file: sess1, content: state("auto", "null"), want: "◆ Opus 4·▰▰▰ high │ ", colour: cEffort, barColour: cWarn},
		{name: "auto before the first request with no session level", payload: withoutEffort, file: sess1, content: state("auto", "null"), want: "◆ Opus 4·effort unknown │ ", colour: cEffort},
		{name: "the main chat's own level", payload: withSession, file: sess1, content: state("model", `"high"`), want: "◆ Opus 4·▰▰▰ high · requested │ ", colour: cEffort, barColour: cWarn},
		{name: "the main chat at max", payload: atLevel("max"), file: sess1, content: state("model", `"max"`), want: "◆ Opus 4·▰▰▰▰▰ max · requested │ ", colour: cEffort, barColour: cBad},
		{name: "the user's pin", payload: atLevel("xhigh"), file: sess1, content: state("pin", `"xhigh"`), want: "◆ Opus 4·▰▰▰▰ xhigh · requested │ ", colour: cEffort, barColour: cElapsed},
		{name: "off at the payload level", payload: withSession, file: sess1, content: state("off", "null"), want: "◆ Opus 4·▰▰▰ high │ ", colour: cEffort, barColour: cWarn},
		{name: "off ignores a held level", payload: atLevel("medium"), file: sess1, content: state("off", `"xhigh"`), want: "◆ Opus 4·▰▰ medium │ ", colour: cEffort, barColour: cGood},
		{name: "off without a payload level", payload: withoutEffort, file: sess1, content: state("off", "null"), want: "◆ Opus 4·effort unknown │ ", colour: cEffort},
		{name: "auto's level replaces the session's", payload: withSession, file: sess1, content: state("auto", `"medium"`), want: "◆ Opus 4·▰▰ medium · requested │ ", colour: cEffort, barColour: cGood},
		{name: "the pin's level replaces the session's", payload: withSession, file: sess1, content: state("pin", `"xhigh"`), want: "◆ Opus 4·▰▰▰▰ xhigh · requested │ ", colour: cEffort, barColour: cElapsed},
		{name: "the model's level shows without a session level", payload: withoutEffort, file: sess1, content: state("model", `"high"`), want: "◆ Opus 4·▰▰▰ high · requested │ ", colour: cEffort, barColour: cWarn},
		{name: "session from the transcript name", payload: byTranscript, file: ".local/state/auto-effort/state.sess-2.json", content: state("auto", `"high"`), want: "◆ Opus 4·▰▰▰ high · requested │ ", colour: cEffort, barColour: cWarn},
		{name: "unreadable", payload: withSession, file: sess1, directory: true, want: "◆ Opus 4·▰▰▰ high · ERR │ ", colour: cBad, wantWarn: true},
		{name: "a pipe, never waited on", payload: withSession, file: sess1, fifo: true, want: "◆ Opus 4·▰▰▰ high · ERR │ ", colour: cBad, wantWarn: true},
		{name: "garbage", payload: withSession, file: sess1, content: "not json\n", want: "◆ Opus 4·▰▰▰ high · ERR │ ", colour: cBad, wantWarn: true},
		{name: "empty file", payload: withSession, file: sess1, content: "", want: "◆ Opus 4·▰▰▰ high · ERR │ ", colour: cBad, wantWarn: true},
		{name: "unknown version", payload: withSession, file: sess1, content: strings.Replace(state("auto", `"medium"`), `"version":3`, `"version":4`, 1), want: "◆ Opus 4·▰▰▰ high · ERR │ ", colour: cBad, wantWarn: true},
		{name: "version 2", payload: withSession, file: sess1, content: strings.Replace(state("auto", `"medium"`), `"version":3`, `"version":2`, 1), want: "◆ Opus 4·▰▰▰ high · ERR │ ", colour: cBad, wantWarn: true},
		{name: "version 2 the retired plugin's keeper holds: as without the plugin", payload: withSession, file: sess1, content: strings.Replace(line("model", `"high"`, self), `"version":3`, `"version":2`, 1), want: "◆ Opus 4·▰▰▰ high │ ", colour: cEffort, barColour: cWarn},
		{name: "version 2 left by a gone keeper: as without the plugin", payload: withSession, file: sess1, content: strings.Replace(line("model", `"high"`, dead), `"version":3`, `"version":2`, 1), want: "◆ Opus 4·▰▰▰ high │ ", colour: cEffort, barColour: cWarn},
		{name: "no keeper: a retired producer's line reads as without the plugin", payload: withSession, file: sess1, content: line("auto", `"medium"`, ""), want: "◆ Opus 4·▰▰▰ high │ ", colour: cEffort, barColour: cWarn},
		{name: "keeper not an integer", payload: withSession, file: sess1, content: line("auto", `"medium"`, "1.5"), want: "◆ Opus 4·▰▰▰ high · ERR │ ", colour: cBad, wantWarn: true},
		{name: "keeper a string", payload: withSession, file: sess1, content: line("auto", `"medium"`, `"`+live+`"`), want: "◆ Opus 4·▰▰▰ high · ERR │ ", colour: cBad, wantWarn: true},
		{name: "keeper negative", payload: withSession, file: sess1, content: line("auto", `"medium"`, "-3"), want: "◆ Opus 4·▰▰▰ high · ERR │ ", colour: cBad, wantWarn: true},
		{name: "keeper null", payload: withSession, file: sess1, content: line("auto", `"medium"`, "null"), want: "◆ Opus 4·▰▰▰ high │ ", colour: cEffort, barColour: cWarn},
		{name: "no keeper vouches: as without the plugin", payload: withSession, file: sess1, content: line("model", `"high"`, "0"), want: "◆ Opus 4·▰▰▰ high │ ", colour: cEffort, barColour: cWarn},
		{name: "stale without a payload level: as without the plugin", payload: withoutEffort, file: sess1, content: line("auto", `"medium"`, "0"), want: "◆ Opus 4·effort unknown │ ", colour: cEffort},
		{name: "the keeper is gone", payload: withSession, file: sess1, content: line("pin", `"xhigh"`, dead), want: "◆ Opus 4·▰▰▰ high │ ", colour: cEffort, barColour: cWarn},
		{name: "the pid is no keeper", payload: withSession, file: sess1, content: line("auto", `"medium"`, self), want: "◆ Opus 4·▰▰▰ high │ ", colour: cEffort, barColour: cWarn},
		{name: "the keeper's argv unreadable", payload: withSession, file: sess1, content: line("auto", `"medium"`, self), unreadableArgs: true, want: "◆ Opus 4·▰▰▰ high · ERR │ ", colour: cBad, wantWarn: true},
		{name: "unknown mode", payload: withSession, file: sess1, content: state("turbo", `"medium"`), want: "◆ Opus 4·▰▰▰ high · ERR │ ", colour: cBad, wantWarn: true},
		{name: "level not in the five shows its word as requested", payload: withSession, file: sess1, content: state("auto", `"ultra"`), want: "◆ Opus 4·effort ultra (unknown) · requested │ ", colour: cEffort},
		{name: "the main chat's level missing", payload: withSession, file: sess1, content: state("model", "null"), want: "◆ Opus 4·▰▰▰ high · ERR │ ", colour: cBad, wantWarn: true},
		{name: "the pin's level missing", payload: withSession, file: sess1, content: state("pin", "null"), want: "◆ Opus 4·▰▰▰ high · ERR │ ", colour: cBad, wantWarn: true},
		{name: "error without a payload level", payload: withoutEffort, file: sess1, content: "{", want: "◆ Opus 4·effort unknown · ERR │ ", colour: cBad, wantWarn: true},
		{name: "no state file", payload: withSession, want: "◆ Opus 4·▰▰▰ high │ ", colour: cEffort, barColour: cWarn},
		{name: "no session to name a file", payload: styledPayload, file: ".local/state/auto-effort/state.json", content: state("auto", `"medium"`), want: "◆ Opus 4·▰▰▰ high │ ", colour: cEffort, barColour: cWarn},
		{name: "thinking off keeps the requested level", payload: thinkingOff, file: sess1, content: state("auto", `"high"`), want: "◆ Opus 4·▰▰▰ high · requested │ ", colour: cEffort, barColour: cWarn},
		{name: "thinking absent keeps effort", payload: strings.Replace(withSession, `"thinking":{"enabled":true},`, "", 1), want: "◆ Opus 4·▰▰▰ high │ ", colour: cEffort, barColour: cWarn},
		{name: "thinking off still reports state errors", payload: thinkingOff, file: sess1, content: "{", want: "◆ Opus 4·▰▰▰ high · ERR │ ", colour: cBad, wantWarn: true},
		{name: "without effort", payload: withoutEffort, want: "◆ Opus 4·effort unknown │ ", colour: cEffort},
		{name: "gpt without thinking keeps reported effort", payload: strings.Replace(strings.Replace(withSession, "Opus 4", "gpt-6-Astra", 1), `"thinking":{"enabled":true},`, "", 1), file: sess1, content: state("off", "null"), want: "·▰▰▰ high │ ", colour: cEffort, barColour: cWarn},
		{name: "gpt effort unproved remains unknown", payload: strings.Replace(strings.Replace(withoutEffort, "Opus 4", "gpt-6-Astra", 1), `"enabled":true`, `"enabled":false`, 1), file: sess1, content: state("off", "null"), want: "·effort unknown │ ", colour: cEffort},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			home := t.TempDir()
			if err := os.MkdirAll(filepath.Join(home, "tmux"), 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(home, testcase.file)
			if testcase.file != "" {
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if testcase.directory {
					if err := os.Mkdir(path, 0o700); err != nil {
						t.Fatal(err)
					}
				} else if testcase.fifo {
					if err := syscall.Mkfifo(path, 0o600); err != nil {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(path, []byte(testcase.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if testcase.unreadableArgs {
				// A proc tree at the runtime's root whose argv entry for the pid
				// cannot be read: the process is alive, its argv unknown.
				if err := os.MkdirAll(filepath.Join(home, "proc", self, "cmdline"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			l1, warn := renderEffort(t, home, testcase.payload)
			plain := styleANSI.ReplaceAllString(l1, "")
			if !strings.Contains(plain, testcase.want) {
				t.Fatalf("line 1 = %q, want %q", plain, testcase.want)
			}
			if testcase.colour != "" {
				effort := testcase.want[strings.Index(testcase.want, "·")+len("·") : strings.Index(testcase.want, " │ ")]
				// A painted bar opens the segment in its own colour; the level word
				// and what follows wear the effort colour.
				open := testcase.colour
				if testcase.barColour != "" {
					bar, rest, _ := strings.Cut(effort, " ")
					open, effort = testcase.barColour, bar+reset+testcase.colour+" "+rest
				}
				if !strings.Contains(l1, open+effort+reset) {
					t.Fatalf("line 1 = %q, want %q in colour %q", l1, effort, testcase.colour)
				}
			}
			if !testcase.wantWarn && warn != "" {
				t.Fatalf("warn = %q, want none: only a broken state file is a failure", warn)
			}
			if testcase.wantWarn {
				lines := strings.Split(strings.TrimSuffix(warn, "\n"), "\n")
				if len(lines) != 1 || !strings.Contains(lines[0], path) ||
					!strings.Contains(lines[0], "auto-effort state") {
					t.Fatalf("warn = %q, want one auto-effort state line naming %s", warn, path)
				}
			}
		})
	}
}

// startKeeper starts a process with auto-effort's keeper argv (its src/state.ts
// KEEPER_ARGV, last element KEEPER_MARK) for the test's life and returns its pid.
func startKeeper(t *testing.T) int {
	t.Helper()
	keeper := exec.Command(
		"/bin/sh", "-c", `echo $$; while kill -0 "$PPID" 2>/dev/null; do sleep 2; done`, "auto-effort-keeper",
	)
	// Stdout stays /dev/null: a pipe would hold Wait until the loop's orphaned
	// sleep exits.
	if err := keeper.Start(); err != nil {
		t.Fatalf("start the keeper: %v", err)
	}
	t.Cleanup(func() {
		if err := keeper.Process.Kill(); err != nil {
			t.Errorf("kill the keeper %d: %v", keeper.Process.Pid, err)
		}
		_ = keeper.Wait() // killed: its exit status is the signal
	})
	return keeper.Process.Pid
}

// reapedPID is the pid of a child that ran, exited and was reaped: no process.
func reapedPID(t *testing.T) int {
	t.Helper()
	child := exec.Command("/bin/sh", "-c", "exit 0")
	if err := child.Run(); err != nil {
		t.Fatalf("run a child to reap: %v", err)
	}
	return child.Process.Pid
}

// renderEffort renders one main statusline over a throwaway runtime rooted at
// home and returns the raw first line and the render's warn output.
func renderEffort(t *testing.T, home, payload string) (string, string) {
	t.Helper()
	var warn bytes.Buffer
	type rendered struct {
		text string
		err  error
	}
	done := make(chan rendered, 1)
	go func() {
		text, err := Render(context.Background(), []byte(payload), Runtime{
			Now:      func() time.Time { return time.Unix(1_786_838_400, 0) },
			Home:     home,
			CacheDir: filepath.Join(home, "cache"),
			TmuxDir:  filepath.Join(home, "tmux"),
			ProcRoot: filepath.Join(home, "proc"),
			Columns:  120,
			UID:      1000,
			Env:      map[string]string{},
			Command:  quietRunner{},
			Warn:     &warn,
		})
		done <- rendered{text, err}
	}()
	// A render that waits on what sits at the state path fails here, never
	// hangs the suite.
	var got rendered
	select {
	case got = <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("render still blocked after 5s under %s", home)
	}
	if got.err != nil {
		t.Fatal(got.err)
	}
	return strings.Split(got.text, "\n")[0], warn.String()
}
