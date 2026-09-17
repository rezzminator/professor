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

func TestRunReloadIntercept(t *testing.T) {
	for _, test := range []struct {
		name, prompt, frontOutput, wantStderr string
		frontCode, wantExit                   int
		frontWanted, wantNoStderr, wantQuiet  bool
		wantArgs                              []string
	}{
		{name: "plain prompt never touches reload", prompt: "hello", wantNoStderr: true},
		{name: "near miss slash command is not a match", prompt: "/reloaded", wantNoStderr: true},
		{name: "bare reload calls front with no args", prompt: "/reload", frontOutput: "reload scheduled\n", frontWanted: true, wantArgs: []string{}, wantNoStderr: true, wantQuiet: true},
		{name: "quoted then payload splits into one word", prompt: `/reload --account 2 --then "go on, friend"`, frontOutput: "reload scheduled\n", frontWanted: true, wantArgs: []string{"--account", "2", "--then", "go on, friend"}, wantNoStderr: true, wantQuiet: true},
		{name: "front failure stays loud", prompt: "/reload --account 2", frontOutput: "pfm chat reload: bad account\n", frontCode: 1, frontWanted: true, wantArgs: []string{"--account", "2"}, wantExit: 2, wantStderr: "reload: pfm chat reload: bad account\n"},
		{name: "unterminated quote is reported", prompt: `/reload --then "go on`, wantExit: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls [][]string
			front := func(args []string, stdout, _ io.Writer, _ config.Runtime) int {
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
			if test.wantQuiet && stdout.String() != quietPromptBlock {
				t.Fatalf("stdout=%q, want %q", stdout.String(), quietPromptBlock)
			}
			if !test.wantQuiet && stdout.Len() != 0 {
				t.Fatalf("stdout=%q, want empty", stdout.String())
			}
			if code != test.wantExit {
				t.Fatalf("exit=%d, want %d; stderr=%q", code, test.wantExit, stderr.String())
			}
			if test.wantNoStderr && stderr.Len() != 0 {
				t.Fatalf("stderr=%q, want empty", stderr.String())
			}
			if test.frontWanted && (len(calls) != 1 || !reflect.DeepEqual(calls[0], test.wantArgs)) {
				t.Fatalf("front calls=%#v, want one %#v", calls, test.wantArgs)
			}
			if test.wantStderr != "" && !strings.HasPrefix(stderr.String(), test.wantStderr) {
				t.Fatalf("stderr=%q, want prefix %q", stderr.String(), test.wantStderr)
			}
			if test.name == "unterminated quote is reported" && !strings.Contains(stderr.String(), "quote") {
				t.Fatalf("stderr=%q does not name quote", stderr.String())
			}
		})
	}
}

func TestRunReloadInterceptMalformedJSONNeverBlocks(t *testing.T) {
	front := func(args []string, _, _ io.Writer, _ config.Runtime) int {
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
