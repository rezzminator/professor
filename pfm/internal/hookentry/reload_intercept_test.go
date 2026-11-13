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

func TestRunReloadIntercept(t *testing.T) {
	const (
		green, red, bold, muted, reset = "\x1b[32m", "\x1b[31m", "\x1b[1m", "\x1b[90m", "\x1b[0m"
		frontLine                      = "pfm chat reload: reload scheduled in place (log /sid/reload-a.log)"
		restarts                       = green + "  This chat restarts when the current turn ends." + reset
		logLine                        = muted + "  " + frontLine + reset
		failed                         = red + bold + "✗ Reload failed — this /reload was not scheduled" + reset
		wentWrong                      = red + "  What went wrong:" + reset
	)
	for _, test := range []struct {
		name, prompt, frontOutput, wantReason string
		frontCode                             int
		frontWanted                           bool
		wantArgs                              []string
	}{
		{name: "plain prompt never touches reload", prompt: "hello"},
		{name: "near miss slash command is not a match", prompt: "/reloaded"},
		{
			name: "bare reload keeps every setting", prompt: "/reload", frontOutput: frontLine + "\n",
			frontWanted: true, wantArgs: []string{},
			wantReason: green + bold + "↻ Reloading — same account, model and effort" + reset + "\n" + restarts + "\n" + logLine,
		},
		{
			name: "typed settings are named back", prompt: "/reload --account 1 --model opus --effort high --1h on",
			frontOutput: frontLine + "\n", frontWanted: true,
			wantArgs: []string{"--account", "1", "--model", "opus", "--effort", "high", "--1h", "on"},
			wantReason: green + bold + "↻ Reloading — account 1 · model opus · effort high · cache 1h" + reset +
				"\n" + restarts + "\n" + logLine,
		},
		{
			name: "quoted then payload splits into one word", prompt: `/reload 2 --new --hide --then "go on, friend"`,
			frontOutput: frontLine + "\n", frontWanted: true,
			wantArgs: []string{"2", "--new", "--hide", "--then", "go on, friend"},
			wantReason: green + bold + `↻ Reloading — account 2 · new conversation · old conversation hidden · then "go on, friend"` +
				reset + "\n" + restarts + "\n" + logLine,
		},
		{
			name: "backslash inside double quotes stays as the shell keeps it", prompt: `/reload --then "C:\dir \"x\" a\\b"`,
			frontOutput: frontLine + "\n", frontWanted: true, wantArgs: []string{"--then", `C:\dir "x" a\b`},
			wantReason: green + bold + `↻ Reloading — then "C:\\dir \"x\" a\\b"` + reset + "\n" + restarts + "\n" + logLine,
		},
		{
			name: "front failure names the typed text and every reason line", prompt: "/reload --account 9",
			frontOutput: "pfm chat reload: account 9 is not configured\nsee pfm config show\n", frontCode: 2,
			frontWanted: true, wantArgs: []string{"--account", "9"},
			wantReason: failed + "\n" + red + "  You typed: /reload --account 9" + reset + "\n" + wentWrong + "\n" +
				red + "    pfm chat reload: account 9 is not configured" + reset + "\n" + red + "    see pfm config show" + reset,
		},
		{
			name: "silent front failure points at the log", prompt: "/reload", frontCode: 1, frontWanted: true,
			wantArgs: []string{},
			wantReason: failed + "\n" + red + "  You typed: /reload" + reset + "\n" + wentWrong + "\n" + red +
				"    pfm chat reload refused and printed no reason; `pfm log` holds its reload.refused record" + reset,
		},
		{
			name: "unquoted operator is reported at its column in the prompt", prompt: "/reload --then go; on",
			wantReason: failed + "\n" + red + "  You typed: /reload --then go; on" + reset + "\n" + wentWrong + "\n" +
				red + "    split shell words: 1:18: `;` is not a valid word" + reset,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
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
			code := ReloadIntercept(bytes.NewReader(payload), &stdout, &stderr, config.Runtime{}, front)
			if code != 0 || stderr.Len() != 0 {
				t.Fatalf("exit=%d stderr=%q, want exit 0 and no stderr", code, stderr.String())
			}
			if test.wantReason == "" {
				if stdout.Len() != 0 {
					t.Fatalf("stdout=%q, want empty for a prompt reload does not take", stdout.String())
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
		})
	}
}

// An unterminated quote never reaches the front: the block names the typed
// text and the parser's own words about the quote.
func TestRunReloadInterceptUnterminatedQuote(t *testing.T) {
	front := func(_ context.Context, args []string, _, _ io.Writer, _ config.Runtime) int {
		t.Fatalf("front called on an unterminated quote: %v", args)
		return 0
	}
	payload := `{"prompt":"/reload --then \"go on"}`
	var stdout, stderr bytes.Buffer
	if code := ReloadIntercept(strings.NewReader(payload), &stdout, &stderr, config.Runtime{}, front); code != 0 {
		t.Fatalf("exit=%d, want 0; stderr=%q", code, stderr.String())
	}
	var decision promptBlockDecision
	if err := json.Unmarshal(stdout.Bytes(), &decision); err != nil {
		t.Fatalf("stdout=%q is not JSON: %v", stdout.String(), err)
	}
	_, wantErr := cmdparse.SplitShellWords(`        --then "go on`)
	if wantErr == nil {
		t.Fatal("SplitShellWords accepted an unterminated quote")
	}
	lines := strings.Split(decision.Reason, "\n")
	if len(lines) != 4 || lines[1] != "\x1b[31m  You typed: /reload --then \"go on\x1b[0m" ||
		lines[3] != "\x1b[31m    "+wantErr.Error()+"\x1b[0m" {
		t.Fatalf("reason lines=%q, want the typed text and the split error", lines)
	}
}

func TestRunReloadInterceptMalformedJSONNeverBlocks(t *testing.T) {
	front := func(_ context.Context, args []string, _, _ io.Writer, _ config.Runtime) int {
		t.Fatalf("front called on malformed JSON: %v", args)
		return 0
	}
	var stderr bytes.Buffer
	code := ReloadIntercept(strings.NewReader("{not valid json"), io.Discard, &stderr, config.Runtime{}, front)
	if code != 0 {
		t.Fatalf("exit=%d, want 0; stderr=%q", code, stderr.String())
	}
	lines := strings.Split(strings.TrimRight(stderr.String(), "\n"), "\n")
	if len(lines) != 1 || lines[0] == "" {
		t.Fatalf("stderr=%q, want exactly one non-empty line", stderr.String())
	}
}
