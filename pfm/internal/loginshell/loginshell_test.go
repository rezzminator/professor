package loginshell

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

func TestRunLookupAnswersTrimmedStdoutOrNamesTheFailure(t *testing.T) {
	for _, tc := range []struct {
		name      string
		result    deps.RunResult
		err       error
		want      string
		wantError string
	}{
		{name: "trimmed", result: deps.RunResult{Stdout: []byte("  /bin/zsh\n")}, want: "/bin/zsh"},
		{
			name: "start failed", result: deps.RunResult{Stderr: []byte("no such file")}, err: errors.New("exec failed"),
			wantError: `lookup login shell via ["tool" "arg"]: exec failed; stderr: no such file`,
		},
		{
			name: "nonzero exit", result: deps.RunResult{ExitCode: 2, Stderr: []byte("denied")},
			wantError: `lookup login shell via ["tool" "arg"]: exit 2; stderr: denied`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &deps.FakeRunner{}
			runner.Script([]string{"tool", "arg"}, tc.result, tc.err)
			got, err := runLookup(context.Background(), runner, "tool", "arg")
			gotError := ""
			if err != nil {
				gotError = err.Error()
			}
			if got != tc.want || gotError != tc.wantError {
				t.Fatalf("runLookup = %q, %q; want %q, %q", got, gotError, tc.want, tc.wantError)
			}
		})
	}
}

// TestScriptAccountAnswersLookup: the fake account database answers this
// kernel's Lookup with the scripted shell, or the scripted failure named by the
// lookup's first call.
func TestScriptAccountAnswersLookup(t *testing.T) {
	checkScriptedAccount(t, ScriptAccount, Lookup)
}

// checkScriptedAccount drives lookup through a runner script scripted, both ways.
func checkScriptedAccount(
	t *testing.T,
	script func(*deps.FakeRunner, int, string, error) [][]string,
	lookup func(context.Context, deps.Runner, int) (string, error),
) {
	t.Helper()
	uid := os.Getuid()
	runner := &deps.FakeRunner{}
	calls := script(runner, uid, "/fixture/bin/zsh", nil)
	got, err := lookup(context.Background(), runner, uid)
	ran := [][]string{}
	for _, call := range runner.Calls() {
		ran = append(ran, call.Argv)
	}
	if got != "/fixture/bin/zsh" || err != nil || !slices.EqualFunc(ran, calls, slices.Equal) {
		t.Fatalf("scripted lookup = %q, %v, calls %q; want /fixture/bin/zsh, <nil>, calls %q", got, err, ran, calls)
	}
	runner = &deps.FakeRunner{}
	calls = script(runner, uid, "", errors.New("account database: not found"))
	_, err = lookup(context.Background(), runner, uid)
	want := fmt.Sprintf("lookup login shell via %q: account database: not found; stderr: ", calls[0])
	if err == nil || err.Error() != want || len(calls) != 1 {
		t.Fatalf("failed scripted lookup = %v, calls %q; want %q from one call", err, calls, want)
	}
}
