package tmux

import (
	"context"
	"errors"
	"testing"
)

func TestFakeCapturePaneScriptedBySubcommand(t *testing.T) {
	fake := &Fake{}
	fake.Script("capture-pane", FakeResult{Stdout: []byte("hello\n")})

	out, err := fake.CapturePane(context.Background(), "sock-a", "-p")
	if err != nil {
		t.Fatalf("CapturePane() error = %v", err)
	}
	if string(out) != "hello\n" {
		t.Fatalf("CapturePane() = %q, want %q", out, "hello\n")
	}
}

func TestFakeScriptForOverridesTheSubcommandWideScript(t *testing.T) {
	fake := &Fake{}
	fake.Script("list-sessions", FakeResult{Stdout: []byte("default\n")})
	fake.ScriptFor("list-sessions", "sock-b", FakeResult{Stdout: []byte("sock-b-specific\n")})

	fromA, err := fake.ListSessions(context.Background(), "sock-a", "-F", "#{session_name}")
	if err != nil {
		t.Fatalf("ListSessions(sock-a) error = %v", err)
	}
	if string(fromA) != "default\n" {
		t.Fatalf("ListSessions(sock-a) = %q, want the subcommand-wide script %q", fromA, "default\n")
	}

	fromB, err := fake.ListSessions(context.Background(), "sock-b", "-F", "#{session_name}")
	if err != nil {
		t.Fatalf("ListSessions(sock-b) error = %v", err)
	}
	if string(fromB) != "sock-b-specific\n" {
		t.Fatalf("ListSessions(sock-b) = %q, want the socket-specific script %q", fromB, "sock-b-specific\n")
	}
}

func TestFakeUnscriptedCallFailsLoudWithTheSubcommandAndSocket(t *testing.T) {
	fake := &Fake{}
	_, err := fake.SendKeys(context.Background(), "sock-a", "Enter")
	if err == nil {
		t.Fatal("SendKeys() with nothing scripted returned nil error")
	}
	var unscripted ErrUnscripted
	if !errors.As(err, &unscripted) {
		t.Fatalf("error = %v (%T), want ErrUnscripted", err, err)
	}
	if unscripted.Subcommand != "send-keys" || unscripted.Socket != "sock-a" {
		t.Fatalf("ErrUnscripted = %+v, want Subcommand=send-keys Socket=sock-a", unscripted)
	}
}

func TestFakeEveryNamedSubcommandSurfaceIsReachable(t *testing.T) {
	fake := &Fake{}
	for _, subcommand := range []string{
		"capture-pane", "list-sessions", "list-windows", "list-panes",
		"send-keys", "display-message", "respawn-pane", "kill-server",
	} {
		fake.Script(subcommand, FakeResult{Stdout: []byte(subcommand + "-ok")})
	}
	ctx := context.Background()
	calls := []struct {
		name string
		run  func() ([]byte, error)
	}{
		{"CapturePane", func() ([]byte, error) { return fake.CapturePane(ctx, "s", "-p") }},
		{"ListSessions", func() ([]byte, error) { return fake.ListSessions(ctx, "s", "-F", "x") }},
		{"ListWindows", func() ([]byte, error) { return fake.ListWindows(ctx, "s", "-F", "x") }},
		{"ListPanes", func() ([]byte, error) { return fake.ListPanes(ctx, "s", "-F", "x") }},
		{"SendKeys", func() ([]byte, error) { return fake.SendKeys(ctx, "s", "Enter") }},
		{"DisplayMessage", func() ([]byte, error) { return fake.DisplayMessage(ctx, "s", "-p", "#S") }},
		{"RespawnPane", func() ([]byte, error) { return fake.RespawnPane(ctx, "s", "-k") }},
		{"KillServer", func() ([]byte, error) { return fake.KillServer(ctx, "s") }},
	}
	for _, call := range calls {
		out, err := call.run()
		if err != nil {
			t.Fatalf("%s() error = %v", call.name, err)
		}
		if len(out) == 0 {
			t.Fatalf("%s() returned no stdout", call.name)
		}
	}
}

func TestFakeCallsRecordsEveryCallInOrderAsACopy(t *testing.T) {
	fake := &Fake{}
	fake.Script("send-keys", FakeResult{})
	if _, err := fake.SendKeys(context.Background(), "sock-a", "Down"); err != nil {
		t.Fatalf("SendKeys() error = %v", err)
	}
	if _, err := fake.SendKeys(context.Background(), "sock-a", "Enter"); err != nil {
		t.Fatalf("SendKeys() error = %v", err)
	}

	calls := fake.Calls()
	if len(calls) != 2 {
		t.Fatalf("Calls() returned %d entries, want 2", len(calls))
	}
	if calls[0].Args[1] != "Down" || calls[1].Args[1] != "Enter" {
		t.Fatalf("Calls() out of order: %+v", calls)
	}

	calls[0].Args[1] = "mutated"
	if fresh := fake.Calls(); fresh[0].Args[1] != "Down" {
		t.Fatalf("Calls()[0].Args[1] = %q after external mutation, want unaffected %q", fresh[0].Args[1], "Down")
	}
}
