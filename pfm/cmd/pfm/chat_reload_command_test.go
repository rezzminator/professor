package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/agentrole"
	"github.com/rezzminator/professor/pfm/internal/codexprompt"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/reload"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/testjail"
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
				clone := t.TempDir()
				if err := paths.WriteSourceRepoMarker(home, clone); err != nil {
					t.Fatal(err)
				}
				fleetPrompt, err := action.ProfessorPromptPath(home)
				if err != nil {
					t.Fatal(err)
				}
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

func (reloadTargetTmux) CaptureStyled(context.Context, string, string) (string, error) {
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

func TestReloadTargetWarnsAndPreservesTmuxWhenNativeSeatsUnreadable(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		wantError string
	}{
		{name: "newer-schema", wantError: "newer than binary"},
		{name: "corrupt", wantError: "not a database"},
		{name: "stat-error", wantError: "not a directory"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("TMUX", "")
			t.Setenv("TMUX_PANE", "")
			t.Setenv("HERDR_ENV", "")
			statePath := filepath.Join(t.TempDir(), "state.sqlite")
			switch testCase.name {
			case "newer-schema":
				db, err := sql.Open("sqlite", statePath)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := db.Close(); err != nil {
						t.Error(err)
					}
				})
				if _, err := db.Exec("PRAGMA user_version=2147483647"); err != nil {
					t.Fatal(err)
				}
			case "corrupt", "stat-error":
				if err := os.WriteFile(statePath, []byte("not a SQLite database"), 0o600); err != nil {
					t.Fatal(err)
				}
				if testCase.name == "stat-error" {
					statePath = filepath.Join(statePath, "state.sqlite")
				}
			}
			want := reload.Pane{ID: "%1", PID: 11, CurrentPath: "/worktree"}
			var stderr bytes.Buffer
			socket, pane, state, code := reloadTarget(
				context.Background(), "/jail/tmux/cc-test", "%1",
				paths.Values{StateDB: statePath}, commandRuntime{},
				reloadTargetTmux{panes: []reload.Pane{want}}, &stderr, nil,
			)
			if code != 0 || socket != "/jail/tmux/cc-test" || pane != want.ID || state != want {
				t.Fatalf(
					"tmux target=(%q,%q,%+v,%d), want live pane despite unreadable native seats; stderr=%q",
					socket,
					pane,
					state,
					code,
					stderr.String(),
				)
			}
			for _, text := range []string{"WARNING", "read native seats", testCase.wantError} {
				if !strings.Contains(stderr.String(), text) {
					t.Errorf("native-seat warning=%q, want %q", stderr.String(), text)
				}
			}
		})
	}
}

func TestReloadUsageIsCanonical(t *testing.T) {
	if !strings.Contains(reload.Usage, "usage: pfm chat reload") {
		t.Fatalf("reload usage=%q", reload.Usage)
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
	prompt, err := codexprompt.FleetPrompt()
	if err != nil {
		t.Fatal(err)
	}
	return prompt
}

func TestRefreshReloadKeepsTheRecordedHarnessPrompt(t *testing.T) {
	repo, home, sidDir := t.TempDir(), t.TempDir(), t.TempDir()
	harness := filepath.Join(t.TempDir(), "alt.md")
	if err := os.WriteFile(harness, []byte("ALT PROMPT"), 0o600); err != nil {
		t.Fatal(err)
	}

	// A roleless seat reborn on the file it was launched with, not the staged prompt.
	if err := agentrole.WriteHarnessPromptRecord(sidDir, "cc-plain", "", harness); err != nil {
		t.Fatal(err)
	}
	channel, err := agentrole.RefreshSeatPrompt(pfmengine.Claude, sidDir, "cc-plain", "%1", repo, home)
	if err != nil || channel != harness {
		t.Fatalf("roleless harness seat = channel %q error %v, want %q", channel, err, harness)
	}

	// A role seat composes its role onto the recorded prompt; no staged prompt exists in home.
	agentPath := filepath.Join(repo, ".claude", "agents", "reviewer.md")
	if err := os.MkdirAll(filepath.Dir(agentPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agentPath, []byte("---\nname: reviewer\n---\nROLE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := agentrole.WriteHarnessPromptRecord(sidDir, "cc-role", "", harness); err != nil {
		t.Fatal(err)
	}
	if err := agentrole.WriteSeatPrompt(sidDir, "cc-role", "", "<!-- pfm agent-role: reviewer -->\nSTALE"); err != nil {
		t.Fatal(err)
	}
	channel, err = agentrole.RefreshSeatPrompt(pfmengine.Claude, sidDir, "cc-role", "%1", repo, home)
	if err != nil || channel != mustReloadSeatPromptPath(t, sidDir, "cc-role", "") {
		t.Fatalf("role harness seat = channel %q error %v", channel, err)
	}
	if _, prompt, _, _, err := agentrole.ReadSeatPrompt(sidDir, "cc-role", ""); err != nil ||
		prompt != "ALT PROMPT\n\n---\n\nROLE\n" {
		t.Fatalf("role harness seat prompt = %q error %v", prompt, err)
	}

	// The recorded file gone is a refusal naming it, never a silent fall back to the staged prompt.
	if err := os.Remove(harness); err != nil {
		t.Fatal(err)
	}
	if _, err := agentrole.RefreshSeatPrompt(pfmengine.Claude, sidDir, "cc-plain", "%1", repo, home); err == nil ||
		!strings.Contains(err.Error(), harness) {
		t.Fatalf("gone harness prompt error = %v, want the path", err)
	}
}

func TestNativeChatKillUsesBackendRatchet(t *testing.T) {
	jailTest(t)
	var stdout, stderr bytes.Buffer
	code := runResolvedChatKillContext(
		context.Background(),
		headless.Chat{
			Backend: "herdr",
			Live:    true,
			ID:      "native",
			Engine:  pfmengine.Claude,
			Socket:  "/fixture/herdr.sock",
			Pane:    "w6:p3",
		},
		true,
		&stdout,
		&stderr,
	)
	if code != 1 || !strings.Contains(stderr.String(), "pfm chat kill:") || stdout.Len() != 0 {
		t.Fatalf("native kill code=%d out=%q err=%q", code, stdout.String(), stderr.String())
	}
}

func TestNativeChatReloadSchedulesResolvedPane(t *testing.T) {
	for _, mode := range []string{"ambient", "socket", "identity"} {
		t.Run(mode, func(t *testing.T) {
			jail := newRunJail(t)
			defer jail.killSockets(t)
			runtime, err := pfmconfig.LoadRuntime("")
			if err != nil {
				t.Fatal(err)
			}
			socket := filepath.Join(jail.root, "herdr.sock")
			t.Setenv("TMUX", "")
			t.Setenv("HERDR_ENV", "1")
			t.Setenv("HERDR_SOCKET_PATH", socket)
			t.Setenv("HERDR_PANE_ID", "w6:p3")
			var args []string
			if mode == "socket" {
				args = []string{"--sock", socket, "--pane", "w6:p3"}
			}
			{
				db := fleetdb.OpenSharedState(context.Background(), runtime.Paths)
				err := db.PutSeat(
					context.Background(),
					fleetdb.Seat{
						SeatID:      "native",
						SessionID:   "native-session",
						Backend:     "herdr",
						Engine:      "cc",
						State:       "live",
						HerdrSocket: socket,
						HerdrPane:   "w6:p3",
					},
				)
				closeErr := db.Close()
				if err != nil || closeErr != nil {
					t.Fatalf("seed: %v %v", err, closeErr)
				}
			}
			started := false
			previous := startReloadWorker
			startReloadWorker = func(argv []string, _ deps.StartOptions) error {
				started = true
				if !strings.Contains(strings.Join(argv, " "), socket) ||
					!strings.Contains(strings.Join(argv, " "), "w6:p3") {
					t.Errorf("worker target %v", argv)
				}
				return nil
			}
			t.Cleanup(func() { startReloadWorker = previous })
			var stdout, stderr bytes.Buffer
			var code int
			if mode == "identity" {
				_, _, _, code = reloadTargetFromIdentity(
					context.Background(),
					resolve.Identity{Backend: "herdr", SocketPath: socket, Pane: "w6:p3"},
					reloadTargetTmux{panes: []reload.Pane{{ID: "w6:p3", PID: 1}}},
					&stderr,
					paths.OSEnv{},
				)
			} else {
				code = runChatReloadWithRuntime(args, &stdout, &stderr, runtime, paths.OSEnv{})
			}
			logs, logErr := filepath.Glob(filepath.Join(runtime.Paths.SIDDir, "reload-*.log"))
			if code != 0 || (mode != "identity" && (!started || len(logs) != 1)) || logErr != nil {
				t.Fatalf(
					"native %s reload code=%d out=%q err=%q started=%v logs=%v err=%v",
					mode,
					code,
					stdout.String(),
					stderr.String(),
					started,
					logs,
					logErr,
				)
			}
		})
	}
}

func TestChatOpenRestoredFlag(t *testing.T) {
	jailTest(t)
	runtime, err := pfmconfig.LoadRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	code := runChatOpen([]string{"--restored", ""}, &out, &stderr, runtime)
	if code != 2 || !strings.Contains(stderr.String(), "restored session ID is required") {
		t.Fatalf("code=%d out=%q err=%q", code, out.String(), stderr.String())
	}
}

func TestNativeReloadWorkerForwardsTypedRequest(t *testing.T) {
	for _, engine := range []pfmengine.ID{pfmengine.Claude, pfmengine.Codex} {
		t.Run(string(engine), func(t *testing.T) {
			jail := newRunJail(t)
			defer jail.killSockets(t)
			runtime, err := pfmconfig.LoadRuntime("")
			if err != nil {
				t.Fatal(err)
			}
			socket := filepath.Join(jail.root, "not-an-engine.sock")
			db := fleetdb.OpenSharedState(context.Background(), runtime.Paths)
			if err := db.PutSeat(
				context.Background(),
				fleetdb.Seat{
					SeatID:      "worker",
					SessionID:   "session",
					Engine:      string(engine),
					Backend:     "herdr",
					State:       "live",
					HerdrSocket: socket,
					HerdrPane:   "w6:p3",
				},
			); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			prior := runHerdrReload
			t.Cleanup(func() { runHerdrReload = prior })
			calls := 0
			runHerdrReload = func(_ context.Context, request reload.Request, options reload.Options, _ io.Writer) (reload.Result, error) {
				calls++
				if request.Engine != engine || request.SocketPath != socket || request.Pane != "w6:p3" ||
					request.Model != "new-model" ||
					request.Effort != "high" ||
					request.Then != "follow up" ||
					request.Cache1H ||
					!request.CacheGiven ||
					!request.AccountGiven ||
					options.SIDDir != runtime.Paths.SIDDir {
					t.Errorf("request=%+v options=%+v", request, options)
				}
				return reload.Result{}, nil
			}
			var out, stderr bytes.Buffer
			code := runChatReloadWorkerWithRuntime(
				[]string{
					"--sock",
					socket,
					"--pane",
					"w6:p3",
					"--model",
					"new-model",
					"--effort",
					"high",
					"--then",
					"follow up",
					"--cache",
					"5m",
					"--account",
					"1",
				},
				&out,
				&stderr,
				runtime,
				paths.OSEnv{},
			)
			if code != 0 || calls != 1 || !strings.Contains(out.String(), "respawned in place") {
				t.Fatalf("code=%d calls=%d out=%q err=%q", code, calls, out.String(), stderr.String())
			}
		})
	}
}

func TestReloadWorkerReportsSuppressedNativeNotification(t *testing.T) {
	t.Setenv(paths.EnvStateDB, filepath.Join(t.TempDir(), "pfm.db"))
	values, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(testjail.ShortRoot(t), "notify.sock")
	serveNativeAdapter(
		t,
		socket,
		map[string]string{"notification.show": `{"type":"notification_show","shown":false,"reason":"disabled"}`},
	)
	db := fleetdb.OpenSharedState(t.Context(), values)
	if err := db.PutSeat(
		t.Context(),
		fleetdb.Seat{
			SeatID:      "notify",
			Backend:     "herdr",
			Engine:      "claude",
			State:       "live",
			HerdrSocket: socket,
			HerdrPane:   "w6:p3",
		},
	); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	announceReloadWorkerFailure(
		[]string{"--sock", socket, "--pane", "w6:p3"},
		"reload failed",
		func(context.Context, string, string, string) error { return io.ErrUnexpectedEOF },
		&stderr,
	)
	if got := stderr.String(); !strings.Contains(got, "native failure notification:") ||
		!strings.Contains(got, "not shown (disabled)") {
		t.Fatalf("notification suppression was hidden: %q", got)
	}
}

func TestChatEndWorkerUsesExactDurableSeatWithoutRoster(t *testing.T) {
	values := paths.Values{StateDB: filepath.Join(t.TempDir(), "pfm.db")}
	db := fleetdb.OpenSharedState(context.Background(), values)
	defer func() { _ = db.Close() }()
	seat := fleetdb.Seat{SeatID: "end-exact", SessionID: "session", Backend: "herdr", State: "live"}
	if err := db.PutSeat(context.Background(), seat); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := runChatEnd([]string{"--native-end-worker", seat.SeatID}, &stdout, &stderr, commandRuntime{Paths: values})
	if code != 1 || !strings.Contains(stderr.String(), "no pending end intent") {
		t.Fatalf("worker scanned names instead of exact durable seat: code=%d out=%s err=%s", code, &stdout, &stderr)
	}
}

func TestChatEndRetryPendingIntentRunsRealDetachedCLIWorker(t *testing.T) {
	jail := newRunJail(t)
	defer jail.killSockets(t)
	runtime, err := pfmconfig.LoadRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(testjail.ShortRoot(t), "h.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			func() {
				defer func() { _ = conn.Close() }()
				var req struct {
					ID     string `json:"id"`
					Method string `json:"method"`
				}
				if err := json.NewDecoder(conn).Decode(&req); err != nil {
					t.Error(err)
					return
				}
				reply := map[string]any{"id": req.ID, "result": map[string]any{"type": "pane_list", "panes": []any{}}}
				if req.Method != "pane.list" {
					t.Errorf("unexpected pending reconciliation method %s", req.Method)
					return
				}
				if err := json.NewEncoder(conn).Encode(reply); err != nil {
					t.Error(err)
				}
			}()
		}
	}()

	seat := fleetdb.Seat{
		SeatID:          "pending-cli",
		SessionID:       "57c427ee-9946-472b-b0e1-c038d98e7ae8",
		Name:            "PENDING",
		Backend:         "herdr",
		State:           "live",
		EndPending:      true,
		HerdrSocket:     socket,
		HerdrPane:       "w6:p3",
		HerdrTerminalID: "old-terminal",
		SeatHostPID:     900001,
		SeatHostStart:   100,
	}
	db := fleetdb.OpenSharedState(context.Background(), runtime.Paths)
	defer func() { _ = db.Close() }()
	if err := db.PutSeat(context.Background(), seat); err != nil {
		t.Fatal(err)
	}
	if err := db.WriteSeatRun(seat.SeatID, fleetdb.SeatRun{Binary: "/fixture/engine"}); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(testPFMBinary, "chat", "end", seat.SessionID)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil || !strings.Contains(stdout.String(), "end scheduled") {
		t.Fatalf("real initiating CLI failed: err=%v out=%s stderr=%s", err, &stdout, &stderr)
	}

	deadline := time.Now().Add(8 * time.Second)
	for {
		_, found, err := db.Seat(context.Background(), seat.SeatID)
		if err != nil {
			t.Fatal(err)
		}
		if !found {
			if _, err := os.Stat(
				filepath.Join(filepath.Dir(runtime.Paths.StateDB), "seats", seat.SeatID+".json"),
			); !os.IsNotExist(
				err,
			) {
				t.Fatalf("pending recipe remains: %v", err)
			}
			break
		}
		if time.Now().After(deadline) {
			body, _ := os.ReadFile(filepath.Join(runtime.Paths.SIDDir, "end-"+seat.SeatID+".log"))
			t.Fatalf("real detached CLI worker did not reconcile: %s", body)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
