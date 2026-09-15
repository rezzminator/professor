package resolve

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmtmux "hostops/pfm/internal/tmux"
)

type fakeTmux struct {
	captures map[string]string
}

func (fake fakeTmux) ListPanes(context.Context, string) ([]Pane, error) {
	return nil, nil
}

func (fake fakeTmux) CapturePane(
	_ context.Context,
	socketPath, paneID string,
) (string, error) {
	return fake.captures[socketPath+"\x00"+paneID], nil
}

func TestResolveLabelExactCaseInsensitiveAndMirrorSkip(t *testing.T) {
	panes := []Pane{
		{
			SocketPath:     "/jail/cc-100-1-1",
			PaneID:         "%1",
			CurrentCommand: "claude",
		},
		{
			SocketPath:     "/jail/vsct",
			PaneID:         "%2",
			CurrentCommand: "tmux",
		},
	}
	resolver := &Resolver{tmux: fakeTmux{captures: map[string]string{
		"/jail/cc-100-1-1\x00%1": "noise\n🥇 │ 🔖 Fleet Builder │ main\n",
		"/jail/vsct\x00%2":       "🥇 │ 🔖 Fleet Builder │ mirror\n",
	}}}

	outcome, err := resolver.resolveLabel(context.Background(), "fleet builder", panes)
	if err != nil {
		t.Fatalf("resolveLabel() error = %v", err)
	}
	assertOutcome(t, outcome, 0, "/jail/cc-100-1-1\t%1\n", "")

	outcome, err = resolver.resolveLabel(context.Background(), "fleet", panes)
	if err != nil {
		t.Fatalf("prefix resolveLabel() error = %v", err)
	}
	assertOutcome(t, outcome, 1, "", "")

	outcome, err = resolver.resolveLabel(context.Background(), "", panes)
	if err != nil {
		t.Fatalf("empty resolveLabel() error = %v", err)
	}
	assertOutcome(t, outcome, 1, "", "")
}

// Every account medal marks a 🔖 label line, including 🍀 — the retired
// account-4 medal. The account is gone, but chats labelled while it was live
// still render it, and a chat whose label the resolver cannot see is a chat
// nothing can address.
func TestResolveLabelAcceptsEveryMedalIncludingTheRetiredOne(t *testing.T) {
	for _, medal := range []string{"🥇", "🥈", "🥉", "🍀"} {
		panes := []Pane{{
			SocketPath:     "/jail/cc-100-1-1",
			PaneID:         "%1",
			CurrentCommand: "claude",
		}}
		resolver := &Resolver{tmux: fakeTmux{captures: map[string]string{
			"/jail/cc-100-1-1\x00%1": "noise\n" + medal + " │ 🔖 Elder │ main\n",
		}}}
		outcome, err := resolver.resolveLabel(
			context.Background(),
			"elder",
			panes,
		)
		if err != nil {
			t.Fatalf("medal %s resolveLabel() error = %v", medal, err)
		}
		assertOutcome(t, outcome, 0, "/jail/cc-100-1-1\t%1\n", "")
	}
	// A 🔖 line with no medal at all is still not a label line.
	resolver := &Resolver{tmux: fakeTmux{captures: map[string]string{
		"/jail/cc-100-1-1\x00%1": "🔖 Elder │ main\n",
	}}}
	outcome, err := resolver.resolveLabel(context.Background(), "elder", []Pane{{
		SocketPath:     "/jail/cc-100-1-1",
		PaneID:         "%1",
		CurrentCommand: "claude",
	}})
	if err != nil {
		t.Fatalf("medalless resolveLabel() error = %v", err)
	}
	assertOutcome(t, outcome, 1, "", "")
}

func TestResolveLabelAcceptsConfiguredAccountEmoji(t *testing.T) {
	resolver := &Resolver{
		tmux:          fakeTmux{captures: map[string]string{"/jail/cc-custom\x00%1": "🟣 │ 🔖 Custom Seat │ main\n"}},
		accountEmojis: []string{"🟣"},
	}
	outcome, err := resolver.resolveLabel(context.Background(), "custom seat", []Pane{{
		SocketPath:     "/jail/cc-custom",
		PaneID:         "%1",
		CurrentCommand: "claude",
	}})
	if err != nil {
		t.Fatalf("configured emoji resolveLabel() error = %v", err)
	}
	assertOutcome(t, outcome, 0, "/jail/cc-custom\t%1\n", "")
}

func TestResolveLabelAmbiguityAndSameChatNewestTieBreak(t *testing.T) {
	sidDir := t.TempDir()
	oldSocket := "/jail/cc-100-1-1"
	newSocket := "/jail/cc-200-1-1"
	panes := []Pane{
		{SocketPath: oldSocket, PaneID: "%1", CurrentCommand: "claude"},
		{SocketPath: newSocket, PaneID: "%2", CurrentCommand: "claude"},
	}
	resolver := &Resolver{
		tmux: fakeTmux{captures: map[string]string{
			oldSocket + "\x00%1": "🥇 🔖 Same │ x\n",
			newSocket + "\x00%2": "🥈 🔖 Same │ x\n",
		}},
		sidDir: sidDir,
	}
	writeCrumb(t, sidDir, "cc-100-1-1.%1", "/tx/one.jsonl")
	writeCrumb(t, sidDir, "cc-200-1-1.%2", "/tx/two.jsonl")

	outcome, err := resolver.resolveLabel(context.Background(), "Same", panes)
	if err != nil {
		t.Fatalf("ambiguous resolveLabel() error = %v", err)
	}
	if outcome.Code != 2 ||
		outcome.Stdout != "" ||
		!strings.Contains(outcome.Stderr, "ambiguous 🔖 label 'Same'") {
		t.Fatalf("different-chat ambiguity = %+v", outcome)
	}

	writeCrumb(t, sidDir, "cc-100-1-1.%1", "/tx/shared.jsonl")
	writeCrumb(t, sidDir, "cc-200-1-1.%2", "/tx/shared.jsonl")
	outcome, err = resolver.resolveLabel(context.Background(), "Same", panes)
	if err != nil {
		t.Fatalf("same-chat resolveLabel() error = %v", err)
	}
	if outcome.Code != 0 ||
		outcome.Stdout != newSocket+"\t%2\n" ||
		!strings.Contains(outcome.Stderr, "⚠2srv") {
		t.Fatalf("same-chat tie break = %+v", outcome)
	}
}

func TestResolveSessionExactAndSplitRules(t *testing.T) {
	resolver := &Resolver{}
	single := []Pane{{
		SocketPath:  "/jail/cc-1-1-1",
		SessionName: "exact",
		PaneID:      "%1",
	}}
	assertOutcome(
		t,
		resolver.resolveSession("exact", single),
		0,
		"/jail/cc-1-1-1\t%1\n",
		"",
	)
	assertOutcome(t, resolver.resolveSession("ex", single), 1, "", "")

	split := []Pane{
		{
			SocketPath:     "/jail/cc-1-1-1",
			SessionName:    "split",
			PaneID:         "%1",
			CurrentCommand: "bash",
		},
		{
			SocketPath:     "/jail/cc-1-1-1",
			SessionName:    "split",
			PaneID:         "%2",
			CurrentCommand: "2.1.215",
		},
	}
	assertOutcome(
		t,
		resolver.resolveSession("split", split),
		0,
		"/jail/cc-1-1-1\t%2\n",
		"",
	)
	split[0].CurrentCommand = "claude-wrapper"
	split[1].CurrentCommand = "bash"
	assertOutcome(
		t,
		resolver.resolveSession("split", split),
		0,
		"/jail/cc-1-1-1\t%1\n",
		"",
	)
	split[1].CurrentCommand = "2.1.215"
	split[0].CurrentCommand = "claude"
	outcome := resolver.resolveSession("split", split)
	if outcome.Code != 2 || !strings.Contains(outcome.Stderr, "2 running claude") {
		t.Fatalf("two-Claude split outcome = %+v", outcome)
	}

	multiple := append(single, Pane{
		SocketPath:  "/jail/cc-2-1-1",
		SessionName: "exact",
		PaneID:      "%3",
	})
	outcome = resolver.resolveSession("exact", multiple)
	if outcome.Code != 2 || !strings.Contains(outcome.Stderr, "ambiguous session 'exact'") {
		t.Fatalf("multi-socket session outcome = %+v", outcome)
	}
}

func TestResolveCxWindowClipsQueryByRunes(t *testing.T) {
	longName := strings.Repeat("界", 30)
	clipped := strings.Repeat("界", 24)
	resolver := &Resolver{}
	panes := []Pane{
		{
			SocketPath: "/jail/cx-10-1-1",
			PaneID:     "%1",
			WindowName: clipped,
		},
		{
			SocketPath: "/jail/cc-20-1-1",
			PaneID:     "%2",
			WindowName: clipped,
		},
	}
	assertOutcome(
		t,
		resolver.resolveCxWindow(longName, panes),
		0,
		"/jail/cx-10-1-1\t%1\n",
		"",
	)
	assertOutcome(t, resolver.resolveCxWindow(strings.Repeat("界", 23), panes), 1, "", "")
	assertOutcome(t, resolver.resolveCxWindow("", panes), 1, "", "")
	assertOutcome(
		t,
		resolver.resolveCxWindow(longName, []Pane{{
			SocketPath: "/jail/cx-30-1-1",
			PaneID:     "%3",
			WindowName: longName,
		}}),
		0,
		"/jail/cx-30-1-1\t%3\n",
		"",
	)
	assertOutcome(
		t,
		resolver.resolveCxWindow(strings.Repeat("x", 500), panes),
		1,
		"",
		"",
	)
}

func writeCrumb(t *testing.T, directory, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o600); err != nil {
		t.Fatalf("write crumb %q: %v", name, err)
	}
}

func assertOutcome(
	t *testing.T,
	outcome Outcome,
	code int,
	stdout, stderr string,
) {
	t.Helper()
	if outcome.Code != code ||
		outcome.Stdout != stdout ||
		outcome.Stderr != stderr {
		t.Fatalf(
			"outcome = %+v, want code=%d stdout=%q stderr=%q",
			outcome,
			code,
			stdout,
			stderr,
		)
	}
}

// TestResolveFailsLoudWhenTmuxCannotRun is the resolve-ladder half of the
// deaf-daemon regression: allPanes dropped every list-panes error, so a tmux
// missing from PATH made every name resolve to "no match" — chat_inject then
// refused a live chat as "matched no live chat". A tmux that never started
// read no socket, so resolution is an error naming the cause, never a miss.
func TestResolveFailsLoudWhenTmuxCannotRun(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	tmuxDir := t.TempDir()
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(tmuxDir, "cc-1-2-3"), Net: "unix"})
	if err != nil {
		t.Fatalf("create socket: %v", err)
	}
	defer listener.Close()
	resolver := &Resolver{tmux: CommandTmux{Binary: "pfm-test-missing-tmux"}, tmuxDir: tmuxDir}
	for _, kind := range []Kind{Session, Label, CxWindow} {
		outcome, err := resolver.Resolve(context.Background(), kind, "any-chat")
		if err == nil {
			t.Fatalf(
				"Resolve(%s) with an unstartable tmux = %+v, nil — a miss that means \"could not look\"",
				kind,
				outcome,
			)
		}
		if !pfmtmux.CouldNotRun(err) {
			t.Fatalf("Resolve(%s) error %v does not carry the could-not-run cause", kind, err)
		}
	}
}

// TestResolveStillMissesPastADeadSocket pins the other side: a socket whose
// server is gone is one ended chat, not a fault — resolution skips it and
// reports a plain miss, never an error.
func TestResolveStillMissesPastADeadSocket(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "tmux-no-server")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\necho 'no server running' >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	tmuxDir := t.TempDir()
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(tmuxDir, "cc-1-2-3"), Net: "unix"})
	if err != nil {
		t.Fatalf("create socket: %v", err)
	}
	defer listener.Close()
	resolver := &Resolver{tmux: CommandTmux{Binary: binary}, tmuxDir: tmuxDir}
	outcome, err := resolver.Resolve(context.Background(), Session, "any-chat")
	if err != nil {
		t.Fatalf("a dead socket failed resolution: %v", err)
	}
	if outcome.Code != 1 {
		t.Fatalf("dead-socket resolution = %+v, want a plain miss (code 1)", outcome)
	}
}
