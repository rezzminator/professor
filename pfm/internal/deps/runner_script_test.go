package deps

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"
)

func TestFakeRunnerScriptsByLongestPrefixMatch(t *testing.T) {
	fake := &FakeRunner{}
	fake.Script([]string{"git"}, RunResult{Stdout: []byte("broad")}, nil)
	fake.Script([]string{"git", "push"}, RunResult{Stdout: []byte("narrow")}, nil)

	broad, err := fake.Run(context.Background(), []string{"git", "status"}, RunOptions{})
	if err != nil {
		t.Fatalf("Run(git status) error = %v", err)
	}
	if got := string(broad.Stdout); got != "broad" {
		t.Fatalf("Run(git status) Stdout = %q, want %q", got, "broad")
	}

	narrow, err := fake.Run(context.Background(), []string{"git", "push", "origin"}, RunOptions{})
	if err != nil {
		t.Fatalf("Run(git push origin) error = %v", err)
	}
	if got := string(narrow.Stdout); got != "narrow" {
		t.Fatalf("Run(git push origin) Stdout = %q, want %q (longest prefix must win)", got, "narrow")
	}
}

func TestFakeRunnerUnscriptedCallFailsLoudWithTheArgv(t *testing.T) {
	fake := &FakeRunner{}
	_, err := fake.Run(context.Background(), []string{"codex", "doctor"}, RunOptions{})
	if err == nil {
		t.Fatal("Run() on an unscripted argv returned nil error")
	}
	var unscripted UnscriptedError
	if !errors.As(err, &unscripted) {
		t.Fatalf("Run() error = %v (%T), want an UnscriptedError", err, err)
	}
	if len(unscripted.Argv) != 2 || unscripted.Argv[0] != "codex" || unscripted.Argv[1] != "doctor" {
		t.Fatalf("UnscriptedError.Argv = %v, want [codex doctor]", unscripted.Argv)
	}
}

func TestFakeRunnerCallsRecordsEveryRunInOrder(t *testing.T) {
	fake := &FakeRunner{}
	fake.Script([]string{"git"}, RunResult{}, nil)
	if _, err := fake.Run(context.Background(), []string{"git", "status"}, RunOptions{Dir: "/repo"}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if _, err := fake.Run(context.Background(), []string{"git", "log"}, RunOptions{}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	calls := fake.Calls()
	if len(calls) != 2 {
		t.Fatalf("Calls() returned %d entries, want 2", len(calls))
	}
	if calls[0].Argv[1] != "status" || calls[0].Opts.Dir != "/repo" {
		t.Fatalf("Calls()[0] = %+v, want argv[1]=status, Dir=/repo", calls[0])
	}
	if calls[1].Argv[1] != "log" {
		t.Fatalf("Calls()[1] = %+v, want argv[1]=log", calls[1])
	}

	// Calls() must return a copy: mutating it never reaches the ledger.
	calls[0].Argv[0] = "mutated"
	if fresh := fake.Calls(); fresh[0].Argv[0] != "git" {
		t.Fatalf("Calls()[0].Argv[0] = %q after external mutation, want unaffected %q", fresh[0].Argv[0], "git")
	}
}

// TestFakeRunnerHandleAnswersAtCallTime: a handler sees the call and its
// context — one that waits on the context returns at the caller's deadline,
// one that counts keeps state between calls — and competes with Script by the
// longest prefix.
func TestFakeRunnerHandleAnswersAtCallTime(t *testing.T) {
	fake := &FakeRunner{}
	fake.Script([]string{"tmux"}, RunResult{Stdout: []byte("scripted")}, nil)
	fake.Handle(
		[]string{"tmux", "-S", "/s/hung", "list-panes"},
		func(ctx context.Context, _ RunCall) (RunResult, error) {
			<-ctx.Done()
			return RunResult{ExitCode: -1}, ctx.Err()
		},
	)
	count := 0
	fake.Handle([]string{"tmux", "-S", "/s/count"}, func(_ context.Context, call RunCall) (RunResult, error) {
		count++
		return RunResult{Stdout: []byte(call.Argv[3] + "=" + strconv.Itoa(count))}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := fake.Run(
		ctx,
		[]string{"tmux", "-S", "/s/hung", "list-panes", "-a"},
		RunOptions{},
	); !errors.Is(
		err,
		context.DeadlineExceeded,
	) {
		t.Fatalf("hung handler error = %v, want context.DeadlineExceeded", err)
	}
	for _, want := range []string{"has-session=1", "has-session=2"} {
		result, err := fake.Run(context.Background(), []string{"tmux", "-S", "/s/count", "has-session"}, RunOptions{})
		if err != nil || string(result.Stdout) != want {
			t.Fatalf("counting handler = (%q, %v), want (%q, nil)", result.Stdout, err, want)
		}
	}
	scripted, err := fake.Run(context.Background(), []string{"tmux", "-S", "/s/other", "list-panes"}, RunOptions{})
	if err != nil || string(scripted.Stdout) != "scripted" {
		t.Fatalf("broad script = (%q, %v), want (scripted, nil)", scripted.Stdout, err)
	}
	if got := len(fake.Calls()); got != 4 {
		t.Fatalf("Calls() = %d, want 4: a handled call is recorded like a scripted one", got)
	}
}
