package main

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"hostops/pfm/internal/store"
)

const sameNameTarget = "COSMOS:ORCHESTRATOR"

type namedCodexFixture struct {
	id     string
	socket string
	prompt string
}

func spawnNamedCodexFixture(
	t *testing.T,
	jail *runJail,
	id, tag string,
) namedCodexFixture {
	t.Helper()
	rollout := filepath.Join(
		jail.root, "codex", "sessions",
		fmt.Sprintf("rollout-2026-08-28T03-00-00-%s.jsonl", id),
	)
	prompt := filepath.Join(jail.root, "prompt-"+tag)
	t.Setenv("CX_STUB_ROLLOUT", rollout)
	t.Setenv("CX_STUB_NAME", filepath.Join(jail.root, "name-"+tag))
	t.Setenv("CX_STUB_PROMPT", prompt)
	t.Setenv("CX_STUB_MODALS", "0")

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"chat", "new", "--engine", "codex",
		"--name", sameNameTarget,
		"--cwd", filepath.Join(jail.root, "work"),
		"seed " + tag,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("spawn %s exit=%d stdout=%q stderr=%q", tag, code, stdout.String(), stderr.String())
	}
	socket := socketFromRun(t, stdout.String())
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertCxName(context.Background(), store.CxName{
		ID: id, ThreadName: sameNameTarget, Source: store.CxNameSourceSessionIndex,
	}); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return namedCodexFixture{id: id, socket: socket, prompt: prompt}
}

func startPreRolloutCodexFixture(
	t *testing.T,
	jail *runJail,
	socket, name string,
) {
	t.Helper()
	t.Setenv("CX_STUB_ROLLOUT", "")
	t.Setenv("CX_STUB_MODALS", "0")
	command := exec.Command(
		"tmux", "-S", filepath.Join(jail.tmuxDir, socket),
		"new-session", "-d", "-s", socket, "-n", name,
		"-c", filepath.Join(jail.root, "work"), "codex",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("start pre-rollout %s: %v: %s", socket, err, output)
	}
}

// A roster name is the fleet's naming judgment. Raw tmux window names are a
// fallback, so abandoned panes carrying the same historical name cannot veto
// the one row the roster actually names.
func TestInjectAndResolvePreferUniqueRosterNameOverDuplicateRawWindows(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newRunJail(t)
	statedTestSender(t)
	defer jail.killSockets(t)

	live := spawnNamedCodexFixture(
		t, jail, "11111111-1111-4111-8111-111111111111", "live",
	)
	startPreRolloutCodexFixture(t, jail, "cx-1700000001-1-1", sameNameTarget)
	startPreRolloutCodexFixture(t, jail, "cx-1700000002-2-2", sameNameTarget)

	var resolved, resolveErr bytes.Buffer
	if code := run([]string{"chat", "resolve", sameNameTarget}, &resolved, &resolveErr); code != 0 {
		t.Fatalf("resolve exit=%d stdout=%q stderr=%q", code, resolved.String(), resolveErr.String())
	}
	if !strings.Contains(resolved.String(), live.socket) ||
		!strings.Contains(resolved.String(), live.id) {
		t.Fatalf("resolve=%q, want named live socket %q and id %q", resolved.String(), live.socket, live.id)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"chat", "inject", sameNameTarget, "deliver to the named incarnation"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("inject exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if got := jail.await(
		t,
		strings.TrimPrefix(live.prompt, jail.root+string(filepath.Separator)),
		"named incarnation",
	); !strings.Contains(
		got,
		"deliver to the named incarnation",
	) {
		t.Fatalf("named target prompt=%q, want delivered message", got)
	}
}

// If the roster itself names two live seats alike, refusing is honest. The
// refusal has to identify both candidates and name the stable disambiguator.
func TestInjectRefusesRosterAmbiguityWithThreadIDsAndSockets(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newRunJail(t)
	statedTestSender(t)
	defer jail.killSockets(t)

	first := spawnNamedCodexFixture(
		t, jail, "22222222-2222-4222-8222-222222222222", "first",
	)
	second := spawnNamedCodexFixture(
		t, jail, "33333333-3333-4333-8333-333333333333", "second",
	)

	var stdout, stderr bytes.Buffer
	code := run([]string{"chat", "inject", sameNameTarget, "must refuse"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("inject exit=%d, want 2; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	for _, want := range []string{first.id, second.id, first.socket, second.socket, "thread id"} {
		if !strings.Contains(strings.ToLower(stderr.String()), strings.ToLower(want)) {
			t.Fatalf("ambiguity=%q, want candidate detail %q", stderr.String(), want)
		}
	}
}

// A new Codex pane can exist before its first rollout makes a roster row.
// Roster-first therefore means roster-then-raw, never roster-only.
func TestChatResolveFallsBackToUniquePreRolloutWindow(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newRunJail(t)
	defer jail.killSockets(t)
	const socket = "cx-1700000003-3-3"
	const name = "COSMOS:EXISTENTIALIST"
	startPreRolloutCodexFixture(t, jail, socket, name)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"chat", "resolve", name}, &stdout, &stderr); code != 0 {
		t.Fatalf("resolve exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), socket) {
		t.Fatalf("resolve=%q, want pre-rollout socket %q", stdout.String(), socket)
	}
}
