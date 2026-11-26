package hookentry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/cmdparse"
	"github.com/rezzminator/professor/pfm/internal/config"
)

func TestRunCommandInterceptReload(t *testing.T) {
	const (
		green, red, bold, muted, reset = "\x1b[32m", "\x1b[31m", "\x1b[1m", "\x1b[90m", "\x1b[0m"
		frontLine                      = "pfm chat reload: reload scheduled in place (log /sid/reload-a.log)"
		restarts                       = green + "  This chat restarts when the current turn ends." + reset
		logLine                        = muted + "  " + frontLine + reset
		failed                         = red + bold + "✗ Reload failed — this /pfm:reload was not scheduled" + reset
		wentWrong                      = red + "  What went wrong:" + reset
	)
	for _, test := range []struct {
		name, prompt, frontOutput, wantReason string
		frontCode                             int
		frontWanted                           bool
		wantArgs                              []string
	}{
		{name: "plain prompt never touches reload", prompt: "hello"},
		{name: "near miss slash command is not a match", prompt: "/pfm:reloaded"},
		{
			name: "bare reload keeps every setting", prompt: "/pfm:reload", frontOutput: frontLine + "\n",
			frontWanted: true, wantArgs: []string{},
			wantReason: green + bold + "↻ Reloading — same account, model and effort" + reset + "\n" + restarts + "\n" + logLine,
		},
		{
			name: "typed settings are named back", prompt: "/pfm:reload --account 1 --model opus --effort high --1h on",
			frontOutput: frontLine + "\n", frontWanted: true,
			wantArgs: []string{"--account", "1", "--model", "opus", "--effort", "high", "--1h", "on"},
			wantReason: green + bold + "↻ Reloading — account 1 · model opus · effort high · cache 1h" + reset +
				"\n" + restarts + "\n" + logLine,
		},
		{
			name: "quoted then payload splits into one word", prompt: `/pfm:reload 2 --new --hide --then "go on, friend"`,
			frontOutput: frontLine + "\n", frontWanted: true,
			wantArgs: []string{"2", "--new", "--hide", "--then", "go on, friend"},
			wantReason: green + bold + `↻ Reloading — account 2 · new conversation · old conversation hidden · then "go on, friend"` +
				reset + "\n" + restarts + "\n" + logLine,
		},
		{
			name: "backslash inside double quotes stays as the shell keeps it", prompt: `/pfm:reload --then "C:\dir \"x\" a\\b"`,
			frontOutput: frontLine + "\n", frontWanted: true, wantArgs: []string{"--then", `C:\dir "x" a\b`},
			wantReason: green + bold + `↻ Reloading — then "C:\\dir \"x\" a\\b"` + reset + "\n" + restarts + "\n" + logLine,
		},
		{
			name: "front failure names the typed text and every reason line", prompt: "/pfm:reload --account 9",
			frontOutput: "pfm chat reload: account 9 is not configured\nsee pfm config show\n", frontCode: 2,
			frontWanted: true, wantArgs: []string{"--account", "9"},
			wantReason: failed + "\n" + red + "  You typed: /pfm:reload --account 9" + reset + "\n" + wentWrong + "\n" +
				red + "    pfm chat reload: account 9 is not configured" + reset + "\n" + red + "    see pfm config show" + reset,
		},
		{
			name: "silent front failure points at the log", prompt: "/pfm:reload", frontCode: 1, frontWanted: true,
			wantArgs: []string{},
			wantReason: failed + "\n" + red + "  You typed: /pfm:reload" + reset + "\n" + wentWrong + "\n" + red +
				"    pfm chat reload refused and printed no reason; `pfm log` holds its reload.refused record" + reset,
		},
		{
			name: "unquoted operator is reported at its column in the prompt", prompt: "/pfm:reload --then go; on",
			wantReason: failed + "\n" + red + "  You typed: /pfm:reload --then go; on" + reset + "\n" + wentWrong + "\n" +
				red + "    split shell words: 1:22: `;` is not a valid word" + reset,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			cleared := recordPromptClears(t)
			var calls [][]string
			front := func(_ context.Context, args []string, stdout, _ io.Writer, _ config.Runtime) int {
				if !test.frontWanted {
					t.Fatalf("front called unexpectedly with %v", args)
				}
				calls = append(calls, append([]string{}, args...))
				fmt.Fprint(stdout, test.frontOutput)
				return test.frontCode
			}
			payload, err := json.Marshal(map[string]string{"prompt": test.prompt})
			if err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			code := CommandIntercept(nil, bytes.NewReader(payload), &stdout, &stderr, config.Runtime{}, front, nil)
			if code != 0 || stderr.Len() != 0 {
				t.Fatalf("exit=%d stderr=%q, want exit 0 and no stderr", code, stderr.String())
			}
			if test.wantReason == "" {
				if stdout.Len() != 0 {
					t.Fatalf("stdout=%q, want empty for a prompt the intercept does not take", stdout.String())
				}
			} else {
				want, err := json.Marshal(promptBlockDecision{
					Decision: "block",
					Reason:   test.wantReason,
					HookSpecificOutput: promptBlockHookOutput{
						HookEventName: "UserPromptSubmit", SuppressOriginalPrompt: true,
					},
				})
				if err != nil {
					t.Fatal(err)
				}
				if stdout.String() != string(want)+"\n" {
					t.Fatalf("stdout=%q\nwant  %q", stdout.String(), string(want)+"\n")
				}
			}
			if test.frontWanted && (len(calls) != 1 || !reflect.DeepEqual(calls[0], test.wantArgs)) {
				t.Fatalf("front calls=%#v, want one %#v", calls, test.wantArgs)
			}
			// A refused reload leaves its command in the input box; a
			// scheduled one is stashed by the reload itself.
			var wantCleared []string
			if strings.HasPrefix(test.wantReason, failed) {
				wantCleared = []string{test.prompt}
			}
			if !reflect.DeepEqual(*cleared, wantCleared) {
				t.Fatalf("input box clears = %q, want %q", *cleared, wantCleared)
			}
		})
	}
}

// An unterminated quote never reaches the front: the block names the typed
// text and the parser's own words about the quote.
func TestRunCommandInterceptUnterminatedQuote(t *testing.T) {
	front := func(_ context.Context, args []string, _, _ io.Writer, _ config.Runtime) int {
		t.Fatalf("front called on an unterminated quote: %v", args)
		return 0
	}
	cleared := recordPromptClears(t)
	payload := `{"prompt":"/pfm:reload --then \"go on"}`
	var stdout, stderr bytes.Buffer
	if code := CommandIntercept(
		nil,
		strings.NewReader(payload),
		&stdout,
		&stderr,
		config.Runtime{},
		front,
		nil,
	); code != 0 {
		t.Fatalf("exit=%d, want 0; stderr=%q", code, stderr.String())
	}
	var decision promptBlockDecision
	if err := json.Unmarshal(stdout.Bytes(), &decision); err != nil {
		t.Fatalf("stdout=%q is not JSON: %v", stdout.String(), err)
	}
	_, wantErr := cmdparse.SplitShellWords(`            --then "go on`)
	if wantErr == nil {
		t.Fatal("SplitShellWords accepted an unterminated quote")
	}
	lines := strings.Split(decision.Reason, "\n")
	if len(lines) != 4 || lines[1] != "\x1b[31m  You typed: /pfm:reload --then \"go on\x1b[0m" ||
		lines[3] != "\x1b[31m    "+wantErr.Error()+"\x1b[0m" {
		t.Fatalf("reason lines=%q, want the typed text and the split error", lines)
	}
	if want := []string{`/pfm:reload --then "go on`}; !reflect.DeepEqual(*cleared, want) {
		t.Fatalf("input box clears = %q, want %q", *cleared, want)
	}
}

func TestRunCommandInterceptMalformedJSONNeverBlocks(t *testing.T) {
	front := func(_ context.Context, args []string, _, _ io.Writer, _ config.Runtime) int {
		t.Fatalf("front called on malformed JSON: %v", args)
		return 0
	}
	var stderr bytes.Buffer
	code := CommandIntercept(
		nil,
		strings.NewReader("{not valid json"),
		io.Discard,
		&stderr,
		config.Runtime{},
		front,
		nil,
	)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; stderr=%q", code, stderr.String())
	}
	lines := strings.Split(strings.TrimRight(stderr.String(), "\n"), "\n")
	if len(lines) != 1 || lines[0] == "" {
		t.Fatalf("stderr=%q, want exactly one non-empty line", stderr.String())
	}
}

func TestRunCommandInterceptSpace(t *testing.T) {
	const (
		green, red, bold, reset = "\x1b[32m", "\x1b[31m", "\x1b[1m", "\x1b[0m"
		failed                  = red + bold + "✗ Space failed — this /pfm:space did not move the chat" + reset
		wentWrong               = red + "  What went wrong:" + reset
		movedLine               = `chat is in space "deep work", Herdr workspace "◇ deep work"`
		clearedLine             = "chat space cleared: the chat is back in its folder place, Herdr workspace \"proj\""
	)
	usage := func(typed string) string {
		return failed + "\n" + red + "  You typed: " + typed + reset + "\n" + wentWrong + "\n" +
			red + "    usage: pfm chat space [name]" + reset
	}
	for _, test := range []struct {
		name, prompt, frontLine, wantReason string
		frontErr                            error
		frontWanted                         bool
		wantName                            string
	}{
		{name: "near miss slash command is not a match", prompt: "/pfm:spaces work"},
		{name: "the bare word without the namespace is not a match", prompt: "/space work"},
		{
			name: "a quoted name is one word", prompt: ` /pfm:space "deep work" `, frontLine: movedLine,
			frontWanted: true, wantName: "deep work", wantReason: green + bold + movedLine + reset,
		},
		{
			name: "bare command clears the space", prompt: "/pfm:space", frontLine: clearedLine,
			frontWanted: true, wantName: "", wantReason: green + bold + clearedLine + reset,
		},
		{name: "two words are refused with the usage line", prompt: "/pfm:space deep work", wantReason: usage("/pfm:space deep work")},
		{name: "an empty quoted name is refused with the usage line", prompt: `/pfm:space ""`, wantReason: usage(`/pfm:space ""`)},
		{name: "a flag is refused with the usage line, never taken as a name", prompt: "/pfm:space --help", wantReason: usage("/pfm:space --help")},
		{
			name: "an unquoted operator is reported at its column in the prompt", prompt: "/pfm:space a;b",
			wantReason: failed + "\n" + red + "  You typed: /pfm:space a;b" + reset + "\n" + wentWrong + "\n" +
				red + "    split shell words: 1:13: `;` is not a valid word" + reset,
		},
		{
			name: "the door's error is shown verbatim", prompt: "/pfm:space work", frontWanted: true, wantName: "work",
			frontErr: fmt.Errorf("identify the calling chat: no pane\nrun it inside a chat"),
			wantReason: failed + "\n" + red + "  You typed: /pfm:space work" + reset + "\n" + wentWrong + "\n" +
				red + "    identify the calling chat: no pane" + reset + "\n" + red + "    run it inside a chat" + reset,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			cleared := recordPromptClears(t)
			var names []string
			reload := func(_ context.Context, args []string, _, _ io.Writer, _ config.Runtime) int {
				t.Fatalf("reload front called on a space prompt: %v", args)
				return 0
			}
			space := func(_ context.Context, _ *config.Runtime, name string) (string, error) {
				if !test.frontWanted {
					t.Fatalf("space front called unexpectedly with %q", name)
				}
				names = append(names, name)
				return test.frontLine, test.frontErr
			}
			payload, err := json.Marshal(map[string]string{"prompt": test.prompt})
			if err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			code := CommandIntercept(nil, bytes.NewReader(payload), &stdout, &stderr, config.Runtime{}, reload, space)
			if code != 0 || stderr.Len() != 0 {
				t.Fatalf("exit=%d stderr=%q, want exit 0 and no stderr", code, stderr.String())
			}
			want := ""
			if test.wantReason != "" {
				encoded, err := json.Marshal(promptBlockDecision{
					Decision: "block",
					Reason:   test.wantReason,
					HookSpecificOutput: promptBlockHookOutput{
						HookEventName: "UserPromptSubmit", SuppressOriginalPrompt: true,
					},
				})
				if err != nil {
					t.Fatal(err)
				}
				want = string(encoded) + "\n"
			}
			if stdout.String() != want {
				t.Fatalf("stdout=%q\nwant  %q", stdout.String(), want)
			}
			if test.frontWanted && (len(names) != 1 || names[0] != test.wantName) {
				t.Fatalf("space front names=%q, want one %q", names, test.wantName)
			}
			var wantCleared []string // every /pfm:space taken, moved or refused, leaves the box clear
			if test.wantReason != "" {
				wantCleared = []string{strings.TrimSpace(test.prompt)}
			}
			if !reflect.DeepEqual(*cleared, wantCleared) {
				t.Fatalf("input box clears = %q, want %q", *cleared, wantCleared)
			}
		})
	}
}

// recordPromptClears swaps the input box clear for a recorder of the prompts
// it was asked to clear.
func recordPromptClears(t *testing.T) *[]string {
	t.Helper()
	var cleared []string
	original := startPromptClear
	startPromptClear = func(_ context.Context, prompt string) error {
		cleared = append(cleared, prompt)
		return nil
	}
	t.Cleanup(func() { startPromptClear = original })
	return &cleared
}
