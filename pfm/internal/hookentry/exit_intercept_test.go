package hookentry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"hostops/pfm/internal/config"
)

func TestRunExitIntercept(t *testing.T) {
	for _, test := range []struct {
		name, prompt string
		frontWanted  bool
		wantExit     int
	}{
		{name: "bare e closes the chat", prompt: "e", frontWanted: true},
		{name: "slash e closes the chat", prompt: "/e", frontWanted: true},
		{name: "surrounding whitespace is trimmed", prompt: "  e  ", frontWanted: true},
		{name: "surrounding whitespace on slash form", prompt: "\t/e\n", frontWanted: true},
		{name: "the word exit is not a match", prompt: "exit"},
		{name: "uppercase E is not a match", prompt: "E"},
		{name: "trailing text after e is not a match", prompt: "e now"},
		{name: "a normal sentence is not a match", prompt: "let's talk about the export format"},
		{name: "empty prompt is not a match", prompt: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls [][]string
			front := func(args []string, stdout, _ io.Writer, _ ...config.Runtime) int {
				if !test.frontWanted {
					t.Fatalf("front called unexpectedly with %v", args)
				}
				calls = append(calls, append([]string{}, args...))
				_, _ = stdout.Write([]byte("exit closed the chat\n"))
				return 0
			}
			payload, err := json.Marshal(map[string]string{"prompt": test.prompt})
			if err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			code := ExitIntercept(bytes.NewReader(payload), &stdout, &stderr, config.Runtime{}, front)
			if code != test.wantExit {
				t.Fatalf("exit=%d, want %d; stderr=%q", code, test.wantExit, stderr.String())
			}
			if test.frontWanted {
				if len(calls) != 1 || !reflect.DeepEqual(calls[0], []string{"--self", "--exit"}) {
					t.Fatalf("front calls = %#v, want one [--self --exit]", calls)
				}
				if stdout.String() != quietPromptBlock || stderr.Len() != 0 {
					t.Fatalf("successful close not quiet: stdout=%q stderr=%q", stdout.String(), stderr.String())
				}
			} else if stderr.Len() != 0 {
				t.Fatalf("stderr=%q, want empty", stderr.String())
			}
		})
	}
}

func TestRunExitInterceptMalformedJSONNeverBlocks(t *testing.T) {
	front := func(args []string, _, _ io.Writer, _ ...config.Runtime) int {
		t.Fatalf("front called on malformed JSON: %v", args)
		return 0
	}
	var stderr bytes.Buffer
	if code := ExitIntercept(
		strings.NewReader("{not valid json"),
		io.Discard,
		&stderr,
		config.Runtime{},
		front,
	); code != 0 {
		t.Fatalf("exit=%d, want 0; stderr=%q", code, stderr.String())
	}
}

func TestRunExitInterceptFailureStaysLoud(t *testing.T) {
	front := func(_ []string, _, stderr io.Writer, _ ...config.Runtime) int {
		fmt.Fprintln(stderr, "pfm chat kill: no live pane")
		return 1
	}
	var stdout, stderr bytes.Buffer
	code := ExitIntercept(strings.NewReader(`{"prompt":"e"}`), &stdout, &stderr, config.Runtime{}, front)
	if code != 2 || stdout.Len() != 0 || !strings.HasPrefix(stderr.String(), "exit: pfm chat kill: no live pane") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}
