package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/agentrole"
	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	"github.com/rezzminator/professor/pfm/internal/codexprompt"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/headless"
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
	); rooted(fmt.Sprint(err), filepath.Dir(home)) != "agent role: no cc role \"vanished\" found\n  searched {root}/001/.claude/agents: directory not found\n  searched {root}/002/.claude/agents: directory not found\navailable roles: none found" {
		t.Fatalf(
			"gone role error = %q, want %q",
			rooted(fmt.Sprint(err), filepath.Dir(home)),
			"agent role: no cc role \"vanished\" found\n  searched {root}/001/.claude/agents: directory not found\n  searched {root}/002/.claude/agents: directory not found\navailable roles: none found",
		)
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
	pfmcall.Screen // reloadTarget lists panes only; it never types or reads.
	panes          []reload.Pane
}

func (tmux reloadTargetTmux) ListPanes(context.Context, string) ([]reload.Pane, error) {
	return tmux.panes, nil
}
func (reloadTargetTmux) SetRemain(context.Context, string, string, bool) error { return nil }
func (reloadTargetTmux) Respawn(context.Context, string, string, string, string) error {
	return nil
}
func (reloadTargetTmux) Display(context.Context, string, string, string) error { return nil }
func (reloadTargetTmux) KillPane(context.Context, string, string) error        { return nil }

func TestReloadUsageIsCanonical(t *testing.T) {
	if reload.Usage != `usage: pfm chat reload [--account N] [--model M] [--effort E] [--cache 1h|5m (or --1h, --5m, --cache on|off)] [--new [--hide]] [--then "prompt"] [--sock socket]
       with no --sock, the calling chat's own pane is detected automatically;
       --hide (with --new) hides the conversation left behind from the picker` {
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
	socket, pane, state, _, code := reloadTarget(
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
	_, _, _, _, code := reloadTarget(
		context.Background(), "/jail/tmux/cc-gone", "%9",
		paths.Values{}, commandRuntime{}, reloadTargetTmux{panes: panes}, &stderr, nil,
	)
	if code == 0 || stderr.String() != "pfm chat reload: pane %9 is not live on /jail/tmux/cc-gone\n" {
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
	_, _, _, _, code := reloadTarget(
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
	socket, pane, state, _, code := reloadTarget(
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

	socket, pane, state, _, code := reloadTargetFromIdentity(
		context.Background(), identity, paths.Values{}, reloadTargetTmux{panes: panes}, &stderr, nil,
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
	prompt, err := codexprompt.FleetPrompt(t.TempDir())
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
	if want := "pfm chat kill: --exit requires a live --self tmux pane\n"; code != 1 || stderr.String() != want ||
		stdout.Len() != 0 {
		t.Fatalf("native kill code=%d out=%q stderr = %q, want %q", code, stdout.String(), stderr.String(), want)
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
	if want := "pfm chat open: restored session ID is required\n"; code != 2 || stderr.String() != want {
		t.Fatalf("code=%d out=%q stderr = %q, want %q", code, out.String(), stderr.String(), want)
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
	line := regexp.MustCompile(`^\S+ `).ReplaceAllString(stderr.String(), "{time} ")
	if want := "{time} pfm chat end worker: failed: seat end-exact has no pending end intent\n"; code != 1 ||
		line != want {
		t.Fatalf("worker code=%d out=%s stderr = %q, want %q", code, &stdout, line, want)
	}
}
