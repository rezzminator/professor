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

// TestRunExitIntercept pins the UserPromptSubmit hook body end to end: only
// an exact "e" or "/e" (surrounding whitespace trimmed) means "close this
// chat" — it calls the front with exactly ["--self", "--exit"] and always
// exits 2 so the prompt never reaches the model; everything else, including
// the near-misses a human's muscle memory could type, exits 0 untouched and
// never calls the front at all.
func TestRunExitIntercept(t *testing.T) {
	original := exitInterceptRun
	t.Cleanup(func() { exitInterceptRun = original })

	for _, test := range []struct {
		name        string
		prompt      string
		frontWanted bool
		wantExit    int
	}{
		{name: "bare e closes the chat", prompt: "e", frontWanted: true, wantExit: 0},
		{name: "slash e closes the chat", prompt: "/e", frontWanted: true, wantExit: 0},
		{name: "surrounding whitespace is trimmed", prompt: "  e  ", frontWanted: true, wantExit: 0},
		{name: "surrounding whitespace on the slash form is trimmed", prompt: "\t/e\n", frontWanted: true, wantExit: 0},
		{name: "the word exit is not a match", prompt: "exit", wantExit: 0},
		{name: "uppercase E is not a match", prompt: "E", wantExit: 0},
		{name: "trailing text after e is not a match", prompt: "e now", wantExit: 0},
		{name: "a normal sentence is not a match", prompt: "let's talk about the export format", wantExit: 0},
		{name: "empty prompt is not a match", prompt: "", wantExit: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls [][]string
			exitInterceptRun = func(args []string, stdout, stderr io.Writer, _ ...commandRuntime) int {
				if !test.frontWanted {
					t.Fatalf("front called unexpectedly with %v", args)
				}
				calls = append(calls, append([]string{}, args...))
				stdout.Write([]byte("exit closed the chat\n"))
				return 0
			}

			payload, err := json.Marshal(map[string]string{"prompt": test.prompt})
			if err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			code := runExitIntercept(bytes.NewReader(payload), &stdout, &stderr, commandRuntime{})

			if code != test.wantExit {
				t.Fatalf("exit=%d, want %d; stderr=%q", code, test.wantExit, stderr.String())
			}
			if test.frontWanted {
				if len(calls) != 1 {
					t.Fatalf("front called %d times, want exactly 1: %v", len(calls), calls)
				}
				if !reflect.DeepEqual(calls[0], []string{"--self", "--exit"}) {
					t.Fatalf("front args = %#v, want [--self --exit]", calls[0])
				}
				if stdout.String() != quietPromptBlock || stderr.Len() != 0 {
					t.Fatalf(
						"a successful close must be swallowed quietly: stdout=%q stderr=%q",
						stdout.String(),
						stderr.String(),
					)
				}
			} else if stderr.Len() != 0 {
				t.Fatalf("stderr=%q, want empty for a non-matching prompt", stderr.String())
			}
		})
	}
}

// TestRunExitInterceptMalformedJSONNeverBlocks pins the hook's own-bug
// refusal: a payload it cannot decode must never cost the human their
// prompt (exit 0) and must never call the front.
func TestRunExitInterceptMalformedJSONNeverBlocks(t *testing.T) {
	original := exitInterceptRun
	exitInterceptRun = func(args []string, _, _ io.Writer, _ ...commandRuntime) int {
		t.Fatalf("front called on malformed JSON: %v", args)
		return 0
	}
	t.Cleanup(func() { exitInterceptRun = original })

	var stderr bytes.Buffer
	code := runExitIntercept(strings.NewReader("{not valid json"), io.Discard, &stderr, commandRuntime{})
	if code != 0 {
		t.Fatalf("exit=%d, want 0; stderr=%q", code, stderr.String())
	}
}

// TestRunExitInterceptFailureStaysLoud pins the other half of the quiet
// block: a close that FAILED is the one message the human must see, so it
// keeps the exit-2 banner with the front's own text.
func TestRunExitInterceptFailureStaysLoud(t *testing.T) {
	original := exitInterceptRun
	t.Cleanup(func() { exitInterceptRun = original })
	exitInterceptRun = func(_ []string, _, stderr io.Writer, _ ...commandRuntime) int {
		fmt.Fprintln(stderr, "pfm chat kill: no live pane")
		return 1
	}
	var stdout, stderr bytes.Buffer
	code := runExitIntercept(strings.NewReader(`{"prompt":"e"}`), &stdout, &stderr, commandRuntime{})
	if code != 2 || stdout.Len() != 0 || !strings.HasPrefix(stderr.String(), "exit: pfm chat kill: no live pane") {
		t.Fatalf(
			"exit=%d stdout=%q stderr=%q, want 2 / empty / the front's error behind the banner",
			code,
			stdout.String(),
			stderr.String(),
		)
	}
}
