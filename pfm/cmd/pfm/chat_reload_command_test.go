package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/agentrole"
	"github.com/rezzminator/professor/pfm/internal/codexgen"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/reload"
	"github.com/rezzminator/professor/pfm/internal/resolve"
)

func TestRefreshReloadRolePromptReResolvesAndRewritesBothEngineChannels(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		engine     pfmengine.ID
		agentDir   string
		agentFile  string
		agentBody  string
		wantPrompt string
		wantPath   bool
	}{
		{
			name: "claude", engine: pfmengine.Claude,
			agentDir: ".claude/agents", agentFile: "reviewer.md",
			agentBody:  "---\nname: reviewer\n---\nCURRENT CLAUDE ROLE\n",
			wantPrompt: "FLEET PROMPT\n\n---\n\nCURRENT CLAUDE ROLE\n", wantPath: true,
		},
		{
			name: "codex", engine: pfmengine.Codex,
			agentDir: ".codex/agents", agentFile: "reviewer.toml",
			agentBody:  "name = \"reviewer\"\ndeveloper_instructions = \"\"\"\nCURRENT CODEX ROLE\n\"\"\"\n",
			wantPrompt: mustCodexFleetPrompt(t) + "\n---\n\nCURRENT CODEX ROLE\n",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repo, home, sidDir := t.TempDir(), t.TempDir(), t.TempDir()
			const socket, pane = "cc-seat", "%2"
			writePane := pane
			if testCase.engine == pfmengine.Claude {
				writePane = ""
			}
			agentPath := filepath.Join(repo, filepath.FromSlash(testCase.agentDir), testCase.agentFile)
			if err := os.MkdirAll(filepath.Dir(agentPath), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(agentPath, []byte(testCase.agentBody), 0o600); err != nil {
				t.Fatal(err)
			}
			if testCase.engine == pfmengine.Claude {
				fleetPrompt := action.ProfessorPromptPath(home)
				if err := os.MkdirAll(filepath.Dir(fleetPrompt), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(fleetPrompt, []byte("FLEET PROMPT"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := agentrole.WriteSeatPrompt(
				sidDir,
				socket,
				writePane,
				"<!-- pfm agent-role: reviewer -->\nSTALE ROLE",
			); err != nil {
				t.Fatal(err)
			}

			channel, err := agentrole.RefreshSeatPrompt(testCase.engine, sidDir, socket, pane, repo, home)
			if err != nil {
				t.Fatal(err)
			}
			role, prompt, _, found, err := agentrole.ReadSeatPrompt(sidDir, socket, pane)
			if err != nil || !found || role != "reviewer" || prompt != testCase.wantPrompt {
				t.Fatalf("refreshed seat prompt = role %q prompt %q found %v error %v", role, prompt, found, err)
			}
			wantChannel := testCase.wantPrompt
			if testCase.wantPath {
				wantChannel = mustReloadSeatPromptPath(t, sidDir, socket, writePane)
			}
			if channel != wantChannel {
				t.Fatalf("prompt channel = %q, want %q", channel, wantChannel)
			}
		})
	}
}

func TestRefreshReloadRolePromptTreatsMissingAsRolelessAndRejectsBadOrGoneRoles(t *testing.T) {
	repo, home, sidDir := t.TempDir(), t.TempDir(), t.TempDir()
	if channel, err := agentrole.RefreshSeatPrompt(
		pfmengine.Claude,
		sidDir,
		"cc-missing",
		"%1",
		repo,
		home,
	); err != nil ||
		channel != "" {
		t.Fatalf("missing role seat = channel %q error %v", channel, err)
	}
	path := mustReloadSeatPromptPath(t, sidDir, "cc-broken", "%1")
	if err := os.WriteFile(path, []byte("not a role marker\nbody"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := agentrole.RefreshSeatPrompt(pfmengine.Claude, sidDir, "cc-broken", "%1", repo, home); err == nil {
		t.Fatal("malformed seat prompt refreshed without an error")
	}
	if err := agentrole.WriteSeatPrompt(sidDir, "cc-gone", "%1", "<!-- pfm agent-role: vanished -->\nOLD"); err != nil {
		t.Fatal(err)
	}
	if _, err := agentrole.RefreshSeatPrompt(
		pfmengine.Claude,
		sidDir,
		"cc-gone",
		"%1",
		repo,
		home,
	); err == nil ||
		!strings.Contains(err.Error(), "vanished") {
		t.Fatalf("gone role error = %v, want the role name", err)
	}
}

func TestChatEndRemovesTheRoleSeatPrompt(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newRunJail(t)
	defer jail.killSockets(t)

	const launchName = "EndRoleWorker"
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"chat", "new", "--engine", "cc", "--name", launchName,
		"--cwd", filepath.Join(jail.root, "work"), "audit the firewall",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("chat new exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	entries, err := os.ReadDir(jail.tmuxDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("jailed tmux sockets=%v err=%v", entries, err)
	}
	target, targetCode := headlessTarget(
		context.Background(), entries[0].Name(), &stdout, &stderr, false,
	)
	if targetCode != 0 {
		t.Fatalf("resolve spawned seat code=%d stdout=%q stderr=%q", targetCode, stdout.String(), stderr.String())
	}
	sidDir := filepath.Join(jail.root, "sid")
	if err := agentrole.WriteSeatPrompt(
		sidDir, target.Socket, target.Pane, "<!-- pfm agent-role: reviewer -->\nROLE",
	); err != nil {
		t.Fatal(err)
	}
	if err := agentrole.WriteSeatPrompt(
		sidDir, target.Socket, "", "<!-- pfm agent-role: reviewer -->\nFALLBACK",
	); err != nil {
		t.Fatal(err)
	}
	promptPaths := []string{
		mustReloadSeatPromptPath(t, sidDir, target.Socket, target.Pane),
		mustReloadSeatPromptPath(t, sidDir, target.Socket, ""),
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"chat", "end", entries[0].Name()}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("chat end exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	for _, promptPath := range promptPaths {
		if _, err := os.Stat(promptPath); !os.IsNotExist(err) {
			t.Fatalf("role prompt %s outlived chat end: %v", promptPath, err)
		}
	}
}

func TestChatEndWarnsButSucceedsWhenRolePromptRemovalFails(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newRunJail(t)
	defer jail.killSockets(t)

	const launchName = "SabotagedRoleWorker"
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"chat", "new", "--engine", "cc", "--name", launchName,
		"--cwd", filepath.Join(jail.root, "work"), "audit the firewall",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("chat new exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	entries, err := os.ReadDir(jail.tmuxDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("jailed tmux sockets=%v err=%v", entries, err)
	}
	target, targetCode := headlessTarget(
		context.Background(), entries[0].Name(), &stdout, &stderr, false,
	)
	if targetCode != 0 {
		t.Fatalf("resolve spawned seat code=%d stdout=%q stderr=%q", targetCode, stdout.String(), stderr.String())
	}
	promptPath := mustReloadSeatPromptPath(t, filepath.Join(jail.root, "sid"), target.Socket, target.Pane)
	if err := os.MkdirAll(filepath.Join(promptPath, "litter"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(promptPath, "litter", "x"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"chat", "end", entries[0].Name()}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("chat end exit=%d, want 0; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "WARNING") || !strings.Contains(stderr.String(), "role prompt") {
		t.Fatalf("chat end stderr=%q, want role-prompt WARNING", stderr.String())
	}
}

func mustReloadSeatPromptPath(t *testing.T, sidDir, socket, pane string) string {
	t.Helper()
	path, err := agentrole.SeatPromptPath(sidDir, socket, pane)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

type reloadTargetTmux struct {
	panes []reload.Pane
}

func (tmux reloadTargetTmux) ListPanes(context.Context, string) ([]reload.Pane, error) {
	return tmux.panes, nil
}
func (reloadTargetTmux) SetRemain(context.Context, string, string, bool) error { return nil }
func (reloadTargetTmux) PaneInMode(context.Context, string, string) (bool, error) {
	return false, nil
}
func (reloadTargetTmux) CancelMode(context.Context, string, string) error { return nil }
func (reloadTargetTmux) Capture(context.Context, string, string) (string, error) {
	return "", nil
}
func (reloadTargetTmux) SendKey(context.Context, string, string, string) error { return nil }
func (reloadTargetTmux) SendLiteral(context.Context, string, string, string) error {
	return nil
}

func (reloadTargetTmux) Respawn(context.Context, string, string, string, string) error {
	return nil
}
func (reloadTargetTmux) Display(context.Context, string, string, string) error { return nil }

func TestReloadUsageIsCanonicalAndSwapIsNotMentioned(t *testing.T) {
	if !strings.Contains(reload.Usage, "usage: pfm chat reload") {
		t.Fatalf("reload usage=%q", reload.Usage)
	}
	if strings.Contains(reload.Usage, "chat swap") {
		t.Fatalf("legacy swap leaked into canonical usage: %q", reload.Usage)
	}
}

func TestReloadAcceptsBareSameAccountRequest(t *testing.T) {
	if err := validateReloadArgs(nil); err != nil {
		t.Fatalf("bare reload rejected: %v", err)
	}
}

// TestReloadPaneArgumentReportsWhatTheCallerTyped is reloadSocketArgument's
// sibling: the scheduler in runChatReloadWithRuntime reads this to decide
// whether it, or the caller, named the pane. A caller-typed --pane must be
// returned verbatim; its absence — or a trailing --pane with nothing after
// it — must report "", never panic on an out-of-range index.
func TestReloadPaneArgumentReportsWhatTheCallerTyped(t *testing.T) {
	for _, testCase := range []struct {
		name string
		args []string
		want string
	}{
		{name: "absent", args: []string{"--sock", "/jail/tmux/cc-1"}, want: ""},
		{name: "present", args: []string{"--sock", "/jail/tmux/cc-1", "--pane", "%3"}, want: "%3"},
		{name: "trailing with no value", args: []string{"--sock", "/jail/tmux/cc-1", "--pane"}, want: ""},
		{name: "no args at all", args: nil, want: ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := reloadPaneArgument(testCase.args); got != testCase.want {
				t.Fatalf("reloadPaneArgument(%#v) = %q, want %q", testCase.args, got, testCase.want)
			}
		})
	}
}

// TestReloadTargetWithPaneSelectsItAmongSeveralWithoutWhoami is the fix for
// the detached-worker bug: the scheduler in runChatReloadWithRuntime resolves
// the caller's pane once, while it still has ancestry or $TMUX to walk, and
// hands it to the worker via --pane. The worker must select THAT pane out of
// a multi-pane server directly — never fall through to resolve.NewWhoami,
// which a Setsid-detached, reparented worker cannot answer (no $TMUX, no
// tmux ancestor). TMUX/TMUX_PANE are cleared here so a wrongly-taken Whoami
// fallback would fail loudly instead of quietly picking up the test binary's
// own environment.
func TestReloadTargetWithPaneSelectsItAmongSeveralWithoutWhoami(t *testing.T) {
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	panes := []reload.Pane{
		{ID: "%1", PID: 11, CurrentPath: "/one"},
		{ID: "%2", PID: 22, CurrentPath: "/two"},
		{ID: "%3", PID: 33, CurrentPath: "/three"},
	}
	var stderr bytes.Buffer
	socket, pane, state, code := reloadTarget(
		context.Background(), "/jail/tmux/cc-multi", "%2",
		paths.Values{}, commandRuntime{}, reloadTargetTmux{panes: panes}, &stderr, nil,
	)
	if code != 0 || socket != "/jail/tmux/cc-multi" || pane != "%2" || state.PID != 22 {
		t.Fatalf(
			"reload target=(%q,%q,%+v,%d), want the requested pane among the multi-pane server; stderr=%q",
			socket, pane, state, code, stderr.String(),
		)
	}
}

// TestReloadTargetWithPaneRejectsAPaneThatIsNotLive covers the flip side: a
// --pane the resolved server no longer carries (the seat closed between the
// scheduler's own resolution and the worker running) is a named error, not a
// silent fall-back to "pick something".
func TestReloadTargetWithPaneRejectsAPaneThatIsNotLive(t *testing.T) {
	panes := []reload.Pane{{ID: "%1", PID: 11, CurrentPath: "/one"}}
	var stderr bytes.Buffer
	_, _, _, code := reloadTarget(
		context.Background(), "/jail/tmux/cc-gone", "%9",
		paths.Values{}, commandRuntime{}, reloadTargetTmux{panes: panes}, &stderr, nil,
	)
	if code == 0 || !strings.Contains(stderr.String(), "pane %9 is not live on /jail/tmux/cc-gone") {
		t.Fatalf("reload target with a dead pane code=%d stderr=%q", code, stderr.String())
	}
}

// TestReloadTargetWithSockOnlyKeepsTheSinglePaneRule pins the existing
// contract for the CALLER-facing form of --sock (no --pane, e.g. `pfm chat
// reload --sock X` typed by a human, or the top-level scheduler call before
// it has resolved a pane of its own): a multi-pane server is still ambiguous
// and still refused, exactly as before --pane existed.
func TestReloadTargetWithSockOnlyKeepsTheSinglePaneRule(t *testing.T) {
	panes := []reload.Pane{
		{ID: "%1", PID: 11, CurrentPath: "/one"},
		{ID: "%2", PID: 22, CurrentPath: "/two"},
	}
	var stderr bytes.Buffer
	_, _, _, code := reloadTarget(
		context.Background(), "/jail/tmux/cc-multi", "",
		paths.Values{}, commandRuntime{}, reloadTargetTmux{panes: panes}, &stderr, nil,
	)
	if code == 0 || !strings.Contains(stderr.String(), "multiple panes") {
		t.Fatalf(
			"reload target with --sock only and multiple panes code=%d stderr=%q, want the multi-pane refusal",
			code,
			stderr.String(),
		)
	}

	single := []reload.Pane{{ID: "%7", PID: 77, CurrentPath: "/solo"}}
	stderr.Reset()
	socket, pane, state, code := reloadTarget(
		context.Background(), "/jail/tmux/cc-solo", "",
		paths.Values{}, commandRuntime{}, reloadTargetTmux{panes: single}, &stderr, nil,
	)
	if code != 0 || socket != "/jail/tmux/cc-solo" || pane != "%7" || state.PID != 77 {
		t.Fatalf(
			"reload target with --sock only and one pane=(%q,%q,%+v,%d), want it auto-selected; stderr=%q",
			socket, pane, state, code, stderr.String(),
		)
	}
}

func TestReloadTargetAcceptsRecoveredCodexSeatWithoutAmbientTmux(t *testing.T) {
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	identity := resolve.Identity{
		Session:    "cx-1800000000-1-1",
		SocketPath: "/jail/tmux/cx-1800000000-1-1",
		SocketName: "cx-1800000000-1-1",
		Engine:     "codex",
		ID:         "11111111-1111-4111-8111-111111111111",
		Source:     "codex-thread",
		Recovered:  true,
	}
	panes := []reload.Pane{{ID: "%0", PID: 42, CurrentPath: "/worktree"}}
	var stderr bytes.Buffer

	socket, pane, state, code := reloadTargetFromIdentity(
		context.Background(), identity, reloadTargetTmux{panes: panes}, &stderr, nil,
	)
	if code != 0 || socket != identity.SocketPath || pane != "%0" || state.PID != 42 {
		t.Fatalf(
			"reload target=(%q,%q,%+v,%d), want recovered socket's only pane; stderr=%q",
			socket, pane, state, code, stderr.String(),
		)
	}
}

// mustCodexFleetPrompt is the composed Codex fleet prompt a cx role seat
// carries ahead of its compiled role body.
func mustCodexFleetPrompt(t *testing.T) string {
	t.Helper()
	prompt, err := codexgen.FleetPrompt()
	if err != nil {
		t.Fatal(err)
	}
	return prompt
}
