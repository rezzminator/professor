package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
)

// TestRunReloadIntercept pins the UserPromptSubmit hook body end to end: a
// prompt that is not "/reload" or "/reload …" never touches the front and
// always exits 0 (the model still sees the prompt); a match always exits 2
// and always writes "reload: " + the front's own captured output to stderr,
// whatever the front returned — the point of the hook is that a matched
// prompt never reaches the model, success or failure.
func TestRunReloadIntercept(t *testing.T) {
	original := reloadInterceptRun
	t.Cleanup(func() { reloadInterceptRun = original })

	for _, test := range []struct {
		name         string
		prompt       string
		frontOutput  string
		frontCode    int
		frontWanted  bool
		wantArgs     []string
		wantExit     int
		wantStderr   string
		wantNoStderr bool
		// wantQuiet: the front succeeded, so the prompt is swallowed with the
		// quiet JSON block on stdout — no banner text, no prompt echo.
		wantQuiet bool
	}{
		{
			name:         "plain prompt never touches reload",
			prompt:       "hello",
			wantExit:     0,
			wantNoStderr: true,
		},
		{
			name:         "near miss slash command is not a match",
			prompt:       "/reloaded",
			wantExit:     0,
			wantNoStderr: true,
		},
		{
			name:         "bare /reload calls the front with no args",
			prompt:       "/reload",
			frontOutput:  "reload scheduled in place (log x)\n",
			frontCode:    0,
			frontWanted:  true,
			wantArgs:     []string{},
			wantExit:     0,
			wantNoStderr: true,
			wantQuiet:    true,
		},
		{
			name:         "quoted --then payload splits into one word",
			prompt:       `/reload --account 2 --then "go on, friend"`,
			frontOutput:  "reload scheduled in place (log y)\n",
			frontCode:    0,
			frontWanted:  true,
			wantArgs:     []string{"--account", "2", "--then", "go on, friend"},
			wantExit:     0,
			wantNoStderr: true,
			wantQuiet:    true,
		},
		{
			name:        "front failure text still lands on stderr behind the exit-2 block",
			prompt:      "/reload --account 2",
			frontOutput: "pfm chat reload: account 2 is not in the configured roster\n",
			frontCode:   1,
			frontWanted: true,
			wantArgs:    []string{"--account", "2"},
			wantExit:    2,
			wantStderr:  "reload: pfm chat reload: account 2 is not in the configured roster\n",
		},
		{
			name:     "unterminated quote is reported without ever calling the front",
			prompt:   `/reload --then "go on`,
			wantExit: 2,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls [][]string
			reloadInterceptRun = func(args []string, stdout, _ io.Writer, _ commandRuntime) int {
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
			code := runReloadIntercept(bytes.NewReader(payload), &stdout, &stderr, commandRuntime{})
			if test.wantQuiet && stdout.String() != quietPromptBlock {
				t.Fatalf("stdout=%q, want the quiet block %q", stdout.String(), quietPromptBlock)
			}
			if !test.wantQuiet && stdout.Len() != 0 {
				t.Fatalf("stdout=%q, want empty when the prompt is not quietly swallowed", stdout.String())
			}

			if code != test.wantExit {
				t.Fatalf("exit=%d, want %d; stderr=%q", code, test.wantExit, stderr.String())
			}
			if test.wantNoStderr && stderr.Len() != 0 {
				t.Fatalf("stderr=%q, want empty", stderr.String())
			}
			if test.frontWanted {
				if len(calls) != 1 {
					t.Fatalf("front called %d times, want exactly 1: %v", len(calls), calls)
				}
				if !reflect.DeepEqual(calls[0], test.wantArgs) {
					t.Fatalf("front args=%#v, want %#v", calls[0], test.wantArgs)
				}
			}
			if test.wantStderr != "" && !strings.HasPrefix(stderr.String(), test.wantStderr) {
				t.Fatalf("stderr=%q, want it to start with %q", stderr.String(), test.wantStderr)
			}
			if test.name == "unterminated quote is reported without ever calling the front" &&
				!strings.Contains(stderr.String(), "quote") {
				t.Fatalf("stderr=%q does not name the unterminated quote", stderr.String())
			}
		})
	}
}

// TestRunReloadInterceptRealFrontSurfacesValidationHint runs the ACTUAL
// front (not a recorder) against an args mistake a human might type —
// "/reload cache off" instead of --1h — and checks the hint the front
// already owns (reloadArgumentHint) reaches the human on stderr unmodified.
// validateReloadArgs fails before runChatReloadWithRuntime ever touches a
// pane or the filesystem, so this needs no jail setup.
func TestRunReloadInterceptRealFrontSurfacesValidationHint(t *testing.T) {
	original := reloadInterceptRun
	reloadInterceptRun = runChatReloadWithRuntime
	t.Cleanup(func() { reloadInterceptRun = original })

	payload, err := json.Marshal(map[string]string{"prompt": "/reload cache off"})
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	code := runReloadIntercept(bytes.NewReader(payload), io.Discard, &stderr, commandRuntime{})
	if code != 2 {
		t.Fatalf("exit=%d, want 2; stderr=%q", code, stderr.String())
	}
	if !strings.HasPrefix(stderr.String(), "reload: ") {
		t.Fatalf("stderr=%q, want it to start with \"reload: \"", stderr.String())
	}
	if !strings.Contains(stderr.String(), "did you mean --1h on|off?") {
		t.Fatalf("stderr=%q is missing the --1h hint", stderr.String())
	}
}

// TestRunReloadInterceptMalformedJSONNeverBlocks pins the hook's own-bug
// refusal: a payload it cannot decode must never cost the human their
// prompt (exit 0), and it reports exactly what went wrong in exactly one
// stderr line — never silence, never a multi-line dump.
func TestRunReloadInterceptMalformedJSONNeverBlocks(t *testing.T) {
	original := reloadInterceptRun
	reloadInterceptRun = func(args []string, _, _ io.Writer, _ commandRuntime) int {
		t.Fatalf("front called on malformed JSON: %v", args)
		return 0
	}
	t.Cleanup(func() { reloadInterceptRun = original })

	var stderr bytes.Buffer
	code := runReloadIntercept(strings.NewReader("{not valid json"), io.Discard, &stderr, commandRuntime{})
	if code != 0 {
		t.Fatalf("exit=%d, want 0; stderr=%q", code, stderr.String())
	}
	lines := strings.Split(strings.TrimRight(stderr.String(), "\n"), "\n")
	if len(lines) != 1 || lines[0] == "" {
		t.Fatalf("stderr=%q, want exactly one non-empty line", stderr.String())
	}
}
