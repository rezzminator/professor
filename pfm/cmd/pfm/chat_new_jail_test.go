package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/mcpserv"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// stubRecorder is the half of a stub engine that makes it a CHAT rather than a
// screen: it writes the engine's own transcript and the evidence that binds it
// to this tmux socket — a sid crumb for Claude, an open rollout descriptor
// under a jailed /proc for Codex.
//
// Without it a stub can only prove that keystrokes were sent. pfm now
// refuses to call a prompt delivered until the ENGINE has recorded being
// asked, so a jail that writes no transcript can no longer tell a delivered
// prompt from one that vanished into a modal — which is the whole failure
// being defended against.
const stubRecorder = `
_esc() { printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g'; }
_sock() { if [ -n "$TMUX" ]; then basename "${TMUX%%,*}"; else printf '%s' "$STUB_SOCKET"; fi; }
_append() { printf '%s\n' "$1" >> "$STUB_TRANSCRIPT"; }
cx_user()  { _append "{\"timestamp\":\"2026-08-12T00:00:00.000Z\",\"type\":\"response_item\",\"payload\":{\"type\":\"message\",\"role\":\"user\",\"content\":[{\"type\":\"input_text\",\"text\":\"$(_esc "$1")\"}]}}"; }
cx_agent() { _append "{\"timestamp\":\"2026-08-12T00:00:01.000Z\",\"payload\":{\"type\":\"agent_message\",\"message\":\"$(_esc "$1")\"}}"; }
cc_user()  { _append "{\"type\":\"user\",\"cwd\":\"$(_esc "$PWD")\",\"message\":{\"content\":\"$(_esc "$1")\"}}"; }
cc_agent() { _append "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"$(_esc "$1")\"}]}}"; }
answer() {
  if [ "$STUB_KIND" = cx ]; then cx_agent "${STUB_REPLY:-ack}: $1"; else cc_agent "${STUB_REPLY:-ack}: $1"; fi
}
turn() {
  [ -z "$STUB_TRANSCRIPT" ] && return 0
  if [ "$STUB_KIND" = cx ]; then cx_user "$1"; else cc_user "$1"; fi
  [ -n "$STUB_MUTE" ] && return 0
  if [ -n "$STUB_DELAY" ]; then ( sleep "$STUB_DELAY"; answer "$1" ) & else answer "$1"; fi
  return 0
}
_fakeproc() {
  [ -z "$PFM_PROC_ROOT" ] && return 0
  mkdir -p "$PFM_PROC_ROOT/$$/fd"
  printf '%s\0--jailed\0' "$1" > "$PFM_PROC_ROOT/$$/cmdline"
  printf '%s (%s) S %s 1 1 0 -1 0 0 0 0 0 0 0 0 0 0 0 20 0 100\n' "$$" "$1" "$PPID" \
    > "$PFM_PROC_ROOT/$$/stat"
  : > "$PFM_PROC_ROOT/$$/environ"
  # The entry goes when the engine does, so a chat that exits stops looking
  # alive to the very scan that has to notice it died.
  trap 'rm -rf "$PFM_PROC_ROOT/$$"' EXIT
  return 0
}
codex_live() {
  STUB_KIND=cx
  [ -z "$CX_STUB_ROLLOUT" ] && return 0
  STUB_TRANSCRIPT="$CX_STUB_ROLLOUT"
  mkdir -p "$(dirname "$STUB_TRANSCRIPT")"
  if [ ! -s "$STUB_TRANSCRIPT" ]; then
    rollout_name="${STUB_TRANSCRIPT##*/}"
    rollout_id="${rollout_name%.jsonl}"
    rollout_id="${rollout_id: -36}"
    printf '{"type":"session_meta","payload":{"id":"%s","source":"cli","thread_source":"user"}}\n' "$rollout_id" >> "$STUB_TRANSCRIPT"
  fi
  _fakeproc codex
  ln -sf "$STUB_TRANSCRIPT" "$PFM_PROC_ROOT/$$/fd/7"
  return 0
}
claude_live() {
  STUB_KIND=cc
  [ -z "$CC_STUB_TRANSCRIPT" ] && return 0
  STUB_TRANSCRIPT="$CC_STUB_TRANSCRIPT"
  mkdir -p "$(dirname "$STUB_TRANSCRIPT")" "$PFM_SID_DIR"
  : >> "$STUB_TRANSCRIPT"
  printf '%s' "$STUB_TRANSCRIPT" > "$PFM_SID_DIR/$(_sock)"
  _fakeproc claude
  return 0
}
`

// stubCodex is a Codex TUI reduced to the four states pfm's rename
// choreography navigates, driven over a REAL tty inside a REAL tmux server:
// canonical mode is turned off so a partial "/rename" is seen before Enter
// (exactly how the engine's own slash-command popup behaves), and every state
// repaints the screen so a marker that should be gone really leaves the
// capture.
const stubCodex = `#!/usr/bin/env bash
printf '%s\n' "$*" > "${CX_STUB_ARGV:-/dev/null}"
stty -icanon -echo -ixon min 1 time 0 2>/dev/null
` + stubRecorder + `
codex_live
stage=composer; buf=""; name=""
status='  019f · ~/work · Full Access · Context 0% used · 0 in · 0 out
'
modals="${CX_STUB_MODALS:-1}"
render() {
  printf '\033[2J\033[H'
  if [ "$modals" -gt 0 ]; then
    # The selection cursor is the same glyph the composer draws, and the
    # status line is gone: the exact screen that fooled the first fix.
    printf 'codex\n  Hooks\n  1 hook needs review before it can run.\n'
    printf '\u203a 2. Trust all and continue\n'
    printf '  Press enter to confirm or esc to go back\n'
    return
  fi
  case "$stage" in
    offered) printf 'codex\n› %s\n  /rename  rename the current thread\n%s' "$buf" "$status" ;;
    prompt)  printf 'codex\n| Name thread\n| Type a name and press Enter\n' ;;
    *)       if [ -n "$name" ]; then printf '* Session renamed to %s.\n' "$name"; fi
             printf 'codex\n› %s\n%s' "$buf" "$status" ;;
  esac
}
render
# -d '' -n1, never -N1: the darwin CI runner's /bin/bash is 3.2, whose read has no -N.
while IFS= read -r -d '' -n1 ch; do
  if [ "$modals" -gt 0 ]; then
    # A startup overlay: ONLY Escape gets out of it, everything else vanishes
    # into it exactly as the real hooks/trust modals swallow keystrokes.
    [ "$ch" = $'\033' ] && modals=$((modals - 1))
    render
    continue
  fi
  case "$ch" in
    $'\n'|$'\r')
      case "$stage" in
        offered) stage=prompt; buf="" ;;
        prompt)  name="$buf"; printf '%s' "$buf" > "$CX_STUB_NAME"; stage=composer; buf="" ;;
        *)       if [ -n "$buf" ]; then
                   printf '%s\n' "$buf" >> "$CX_STUB_PROMPT"
                   turn "$buf"
                 fi
                 buf="" ;;
      esac ;;
    $'\023')
      : ;;
    $'\177'|$'\b')
      buf="${buf%?}" ;;
    *)
      buf="$buf$ch"
      if [ "$stage" = composer ] && [ "$buf" = "/rename" ] &&
         [ -z "$CX_STUB_NO_RENAME" ]; then stage=offered; fi ;;
  esac
  render
done
`

// stubClaude records the argv it was launched with, answers the prompt that
// travelled on it, and then behaves like a composer: Claude's name and first
// prompt need no keystrokes, but every LATER message does, and a chat that
// cannot be spoken to a second time is not a conversation.
//
// CC_STUB_DEAF models the failure this whole verification exists for: the
// launch prompt arrives on the command line and is never recorded, exactly as
// a startup dialog eating it would look from the outside.
//
// CC_STUB_OVERLAY models the recoverable shape of that same failure: the
// prompt is held unsent behind a startup overlay, and an Escape followed by
// an Enter submits it. Deaf is unrecoverable, overlay is what a retry saves.
const stubClaude = `#!/usr/bin/env bash
printf '%s\n' "$*" > "$CC_STUB_ARGV"
stty -icanon -echo -ixon min 1 time 0 2>/dev/null
` + stubRecorder + `
claude_live
prompt=""
skip=0
for argument in "$@"; do
  if [ "$skip" = 1 ]; then skip=0; continue; fi
  case "$argument" in
    --name|--model|--effort) skip=1 ;;
    -*) ;;
    *) prompt="$argument"; break ;;
  esac
done
buf=""; note=""
render() {
  printf '\033[2J\033[H'; printf 'claude ready\n'
  [ -n "$note" ] && printf '%s\n' "$note"
  printf '❯ %s\n' "$buf"
}
if [ -n "$prompt" ] && [ -z "$CC_STUB_DEAF" ] && [ -z "$CC_STUB_OVERLAY" ]; then turn "$prompt"; fi
pending=""; dismissed=0
if [ -n "$CC_STUB_OVERLAY" ]; then pending="$prompt"; fi
render
# -d '' -n1, never -N1: the darwin CI runner's /bin/bash is 3.2, whose read has no -N.
while IFS= read -r -d '' -n1 ch; do
  case "$ch" in
    $'\033') dismissed=1 ;;
    $'\n'|$'\r')
      if [ -n "$buf" ]; then turn "$buf"
      elif [ "$dismissed" = 1 ] && [ -n "$pending" ]; then turn "$pending"; pending=""; fi
      buf=""; note="" ;;
    $'\023') buf=""; note="  draft stashed" ;;
    $'\177'|$'\b') buf="${buf%?}" ;;
    *) buf="$buf$ch" ;;
  esac
  render
done
`

type runJail struct {
	root    string
	tmuxDir string
	binDir  string
	// The transcripts the stub engines write, and which pfm reads back as
	// the proof a prompt was delivered.
	rollout    string
	transcript string
}

func TestRunJailPinsXDGConfigHome(t *testing.T) {
	foreign := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", foreign)
	jail := newRunJail(t)
	want := filepath.Join(jail.root, "home", ".config")
	if got := os.Getenv("XDG_CONFIG_HOME"); got != want {
		t.Fatalf("XDG_CONFIG_HOME=%q, want jailed config root %q", got, want)
	}
}

func newRunJail(t *testing.T) *runJail {
	t.Helper()
	previousTimings := runSpawnTimings
	runSpawnTimings = spawn.Timings{
		Poll:  10 * time.Millisecond,
		Boot:  time.Second,
		Step:  time.Second,
		Typed: 10 * time.Millisecond,
	}
	t.Cleanup(func() { runSpawnTimings = previousTimings })
	// /tmp, not t.TempDir(): a tmux socket path must stay inside the ~100
	// byte sun_path limit, which the test-name-derived TempDir blows past.
	root, err := os.MkdirTemp("/tmp", "ccfrun")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })

	jail := &runJail{
		root: root,
		// tmux-<uid> is tmux's own convention under TMUX_TMPDIR, and the fleet
		// scan reaches a server with `-L <socket>` while spawn creates it with
		// `-S <dir>/<socket>`. Naming the directory anything else makes those
		// two disagree, and a chat pfm just started becomes one it cannot
		// find.
		tmuxDir: filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid())),
		binDir:  filepath.Join(root, "bin"),
		rollout: filepath.Join(
			root, "codex", "sessions",
			"rollout-2026-08-12T00-00-00-019ff700-0000-7000-8000-000000000001.jsonl",
		),
		transcript: filepath.Join(
			root, "claude", "stub",
			"b1111111-1111-4111-8111-111111111111.jsonl",
		),
	}
	for _, directory := range []string{
		jail.tmuxDir,
		jail.binDir,
		filepath.Join(root, "home"),
		filepath.Join(root, "claude"),
		filepath.Join(root, "codex"),
		filepath.Join(root, "sid"),
		filepath.Join(root, "proc"),
		filepath.Join(root, "work"),
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(name, body string) {
		if err := os.WriteFile(
			filepath.Join(jail.binDir, name),
			[]byte(body),
			0o700,
		); err != nil {
			t.Fatal(err)
		}
	}
	write("codex", stubCodex)
	write("claude", stubClaude)
	if err := os.WriteFile(
		filepath.Join(root, "codex", "auth.json"),
		[]byte(`{"tokens":{"access_token":"fixture","account_id":"fixture"}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	configDir := filepath.Join(root, "home", ".config", "pfm")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(configDir, "config.json"),
		[]byte(`{"version":2,"ask":{"engine":"claude"}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	// The engine stubs go on PATH BEFORE anything drives the CLI, and every
	// self-identifying variable is cleared: a spawn made from inside this
	// chat must not inherit its tmux server, pane, or chat identity.
	t.Setenv("PATH", jail.binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "home", ".config"))
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("CODEX_THREAD_ID", "")
	t.Setenv("TMUX_TMPDIR", root)
	t.Setenv("PFM_HOME", filepath.Join(root, "home"))
	t.Setenv("PFM_DB", filepath.Join(root, "fleet.db"))
	t.Setenv("PFM_SID_DIR", filepath.Join(root, "sid"))
	t.Setenv("PFM_CLAUDE_ROOTS", filepath.Join(root, "claude"))
	t.Setenv("PFM_CODEX_ROOT", filepath.Join(root, "codex"))
	t.Setenv("PFM_TMUX_DIR", jail.tmuxDir)
	t.Setenv("PFM_PROC_ROOT", filepath.Join(root, "proc"))
	t.Setenv("CX_STUB_NAME", filepath.Join(root, "cx-name"))
	t.Setenv("CX_STUB_PROMPT", filepath.Join(root, "cx-prompt"))
	t.Setenv("CC_STUB_ARGV", filepath.Join(root, "cc-argv"))
	t.Setenv("CX_STUB_ROLLOUT", jail.rollout)
	t.Setenv("CC_STUB_TRANSCRIPT", jail.transcript)
	return jail
}

// killSockets takes down every server this test started, so a jail never
// leaves a tmux server behind holding the temp directory open.
func (jail *runJail) killSockets(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir(jail.tmuxDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		_ = exec.Command(
			"tmux",
			"-S", filepath.Join(jail.tmuxDir, entry.Name()),
			"kill-server",
		).Run()
	}
}

func (jail *runJail) read(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(jail.root, name))
	if err != nil {
		return ""
	}
	return string(content)
}

func (jail *runJail) onlyWindowName(t *testing.T) string {
	t.Helper()
	entries, err := os.ReadDir(jail.tmuxDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("jailed socket count=%d err=%v", len(entries), err)
	}
	output, err := exec.Command(
		"tmux", "-S", filepath.Join(jail.tmuxDir, entries[0].Name()),
		"display-message", "-p", "#{window_name}",
	).Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(output))
}

// await polls for engine-side state. `run` returns the moment the keystrokes
// are delivered — the engine writes its file a beat later — so a bare read
// races the pane and would flake.
func (jail *runJail) await(t *testing.T, name, want string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	got := ""
	for time.Now().Before(deadline) {
		got = jail.read(t, name)
		if strings.Contains(got, want) {
			return got
		}
		time.Sleep(50 * time.Millisecond)
	}
	return got
}

// TestChatNewSpawnsANamedCodexChat drives the CLI against a real tmux
// server and a stub engine: the session is created detached, the thread is
// renamed through the engine's own UI, and only then is the first prompt
// delivered.
func TestChatNewSpawnsANamedCodexChat(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newRunJail(t)
	defer jail.killSockets(t)
	t.Setenv("CX_STUB_ARGV", filepath.Join(jail.root, "cx-argv"))

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"chat", "new",
		"--engine", "codex",
		"--name", "_KILL codex worker",
		"--cwd", filepath.Join(jail.root, "work"),
		"read the incident report",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if got := jail.await(t, "cx-name", "_KILL"); got != "_KILL codex worker" {
		t.Fatalf("thread name = %q, want %q (stderr=%q)",
			got, "_KILL codex worker", stderr.String())
	}
	got := jail.await(t, "cx-prompt", "incident")
	if strings.TrimSpace(got) != "read the incident report" {
		t.Fatalf("delivered prompt = %q", got)
	}
	if argv := jail.read(t, "cx-argv"); !strings.Contains(argv, "--dangerously-bypass-approvals-and-sandbox") {
		t.Fatalf("absent-config Codex argv = %q, want current bypass flag", argv)
	}
	report := stdout.String()
	if !strings.Contains(report, "\tnamed\t") ||
		!strings.Contains(report, "killed by its _KILL name") {
		t.Fatalf("run report = %q", report)
	}
	if !strings.Contains(report, "attach: tmux -L cx-") {
		t.Fatalf("run report has no attach line: %q", report)
	}

	entries, err := os.ReadDir(jail.tmuxDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("jailed tmux sockets = %v (err=%v)", entries, err)
	}
	if !strings.HasPrefix(entries[0].Name(), "cx-") {
		t.Fatalf("codex chat born on socket %q", entries[0].Name())
	}
	if got := jail.onlyWindowName(t); got != "_KILL codex worker" {
		t.Fatalf("codex window=%q, want inline launch name", got)
	}
	state := fleetdb.OpenSharedState(context.Background(), paths.Values{
		FleetDB: filepath.Join(jail.root, "home", ".cc", "fleet.db"),
	})
	t.Cleanup(func() { _ = state.Close() })
	events, err := state.CommsSince(context.Background(), 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	// ReceiverSocket must be the full socket PATH ("-S" argument tmux
	// itself takes), not the bare file name: the inject recorder
	// (internal/inject/engine.go) already records result.SocketPath in
	// this same shape, and a spawn recorded any other way cannot resolve
	// against a live row's own (socket, pane) key
	// (compose.paneKey/rowsByPane) — the bug that let one dead chat render
	// as three ghost nodes. ReceiverPane is asserted empty deliberately:
	// spawn.Result carries no pane id (a fresh session's first pane is
	// conventionally "%0", but nothing here proves that invariant, so
	// chat_new_command.go does not invent one).
	wantSocket := filepath.Join(jail.tmuxDir, entries[0].Name())
	if len(events) != 1 || events[0].Kind != fleetdb.KindSpawn ||
		events[0].SenderSession != "" || events[0].Target != "_KILL codex worker" ||
		events[0].ReceiverSocket != wantSocket || events[0].ReceiverPane != "" ||
		events[0].Message != "read the incident report" {
		t.Fatalf("spawn comms = %#v, want ReceiverSocket %q and empty ReceiverPane", events, wantSocket)
	}
}

// TestRunReportsACodexBuildThatCannotBeRenamed is the version-drift drill: a
// Codex that does not offer /rename must leave a WORKING chat that says so —
// non-zero exit, UNNAMED in the report — and the abandoned command must never
// reach the model as a prompt.
func TestRunReportsACodexBuildThatCannotBeRenamed(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newRunJail(t)
	defer jail.killSockets(t)
	t.Setenv("CX_STUB_NO_RENAME", "1")

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"chat", "new",
		"--engine", "cx",
		"--name", "worker",
		"--cwd", filepath.Join(jail.root, "work"),
		"read the incident report",
	}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("run exit=%d, want 1 (stdout=%q)", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "\tUNNAMED\t") {
		t.Fatalf("run report = %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "did not offer /rename") {
		t.Fatalf("run warnings = %q", stderr.String())
	}
	if got := jail.read(t, "cx-name"); got != "" {
		t.Fatalf("a thread name landed anyway: %q", got)
	}
	prompts := jail.await(t, "cx-prompt", "incident")
	if strings.TrimSpace(prompts) != "read the incident report" {
		t.Fatalf("delivered prompts = %q — the work must still be delivered, "+
			"and the abandoned /rename must not be", prompts)
	}
}

// TestChatNewSpawnsAClaudeChatWithItsNameOnTheCommandLine proves the other
// route: Claude is named by its own flag, carries the autonomy flags, and is
// never typed into.
func TestChatNewSpawnsAClaudeChatWithItsNameOnTheCommandLine(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newRunJail(t)
	defer jail.killSockets(t)

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"chat", "new",
		"--name", "worker 7",
		"--cwd", filepath.Join(jail.root, "work"),
		"audit the firewall",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	argv := jail.await(t, "cc-argv", "--name")
	for _, want := range []string{
		"--name worker 7",
		"audit the firewall",
		"--allow-dangerously-skip-permissions",
		"--dangerously-skip-permissions",
	} {
		if !strings.Contains(argv, want) {
			t.Fatalf("claude argv = %q, want it to contain %q", argv, want)
		}
	}
	if jail.read(t, "cx-name") != "" {
		t.Fatal("the Claude route drove the Codex rename UI")
	}
	report := stdout.String()
	if !strings.Contains(report, "cc\tworker 7\t") ||
		!strings.Contains(report, "\tlisted\n") {
		t.Fatalf("run report = %q", report)
	}
	if got := jail.onlyWindowName(t); got != "worker 7" {
		t.Fatalf("claude window=%q, want inline launch name", got)
	}
}

// TestMachineConfigChangesTheActualLaunchCommands is the machine-config
// acceptance seam: it grades the argv recorded by the engine processes, not
// merely the decoded config struct. The default-path tests above separately
// pin today's bypass flags when no config file exists.
func TestMachineConfigChangesTheActualLaunchCommands(t *testing.T) {
	for _, test := range []struct {
		name      string
		engine    string
		argvFile  string
		forbidden []string
	}{
		{
			name:     "claude prompt permissions",
			engine:   "claude",
			argvFile: "cc-argv",
			forbidden: []string{
				"--allow-dangerously-skip-permissions",
				"--dangerously-skip-permissions",
			},
		},
		{
			name:      "codex workspace sandbox",
			engine:    "codex",
			argvFile:  "cx-argv",
			forbidden: []string{"--dangerously-bypass-approvals-and-sandbox"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := exec.LookPath("tmux"); err != nil {
				t.Skip("tmux is not installed")
			}
			jail := newRunJail(t)
			defer jail.killSockets(t)
			t.Setenv("CX_STUB_ARGV", filepath.Join(jail.root, "cx-argv"))

			configPath := filepath.Join(jail.root, "config.json")
			content, err := json.Marshal(map[string]any{
				"version": 1,
				"accounts": []map[string]any{
					{"id": 1, "configDir": filepath.Join(jail.root, "accounts", "1")},
					{"id": 2, "configDir": filepath.Join(jail.root, "accounts", "2")},
					{"id": 3, "configDir": filepath.Join(jail.root, "accounts", "3")},
				},
				"claude": map[string]any{"permissionMode": "prompt"},
				"codex":  map[string]any{"yolo": false},
				"mcp": map[string]any{
					"servers": map[string]any{
						"chat": map[string]any{"enabled": false},
					},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(configPath, content, 0o600); err != nil {
				t.Fatal(err)
			}

			var stdout, stderr bytes.Buffer
			code := run([]string{
				"--config", configPath,
				"chat", "new",
				"--engine", test.engine,
				"--name", "configured-worker",
				"--cwd", filepath.Join(jail.root, "work"),
				"inspect the fixture",
			}, &stdout, &stderr)
			if code != 0 {
				t.Fatalf("run exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			argv := jail.await(t, test.argvFile, "inspect")
			for _, value := range test.forbidden {
				if strings.Contains(argv, value) {
					t.Fatalf("configured %s argv still contains %q: %q", test.engine, value, argv)
				}
			}
		})
	}
}

type chatNewCallerVerbs struct {
	row compose.Row
}

func (verbs chatNewCallerVerbs) Last(context.Context, pfmchat.LastRequest) (pfmchat.LastResult, error) {
	return pfmchat.LastResult{}, errors.New("unexpected chat last")
}

func (verbs chatNewCallerVerbs) Status(context.Context, pfmchat.StatusRequest) (headless.Status, error) {
	return headless.Status{}, errors.New("unexpected chat status")
}

func (verbs chatNewCallerVerbs) List(context.Context, pfmchat.ListRequest) (pfmchat.ListResult, error) {
	return pfmchat.ListResult{Rows: []compose.Row{verbs.row}, Matched: 1}, nil
}

func (verbs chatNewCallerVerbs) Find(context.Context, pfmchat.FindRequest) ([]pfmchat.TranscriptMatch, error) {
	return nil, errors.New("unexpected chat find")
}

func (verbs chatNewCallerVerbs) Read(
	context.Context,
	string,
	int,
) (headless.Chat, []transcript.Entry, bool, error) {
	return headless.Chat{}, nil, false, errors.New("unexpected chat read")
}

func callJailedChatNew(
	t *testing.T,
	runtime commandRuntime,
	row compose.Row,
	input mcpserv.NewInput,
) {
	t.Helper()
	bridge := mcpRuntime(runtime, false)
	bridge.Chat = chatNewCallerVerbs{row: row}
	service, err := mcpserv.NewConfigured("test", io.Discard, bridge)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := service.Server().Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "pfm-test", Version: "test"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })
	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Meta: mcp.Meta{"pfmProxy": map[string]any{
			"v": mcpserv.ProxyWireVersion, "session": row.SessionName, "socketName": row.Socket,
			"socketPath": filepath.Join(runtime.Paths.TmuxDir, row.Socket), "pane": row.PaneID,
			"engine": "claude", "id": row.ID,
		}}, Name: "chat_new", Arguments: input,
	})
	if err != nil {
		t.Fatalf("chat_new: %v", err)
	}
	if result.IsError {
		content, marshalErr := json.Marshal(result.Content)
		if marshalErr != nil {
			t.Fatalf("chat_new returned tool error %#v (encode error: %v)", result.Content, marshalErr)
		}
		t.Fatalf("chat_new returned tool error: %s", content)
	}
}

func assertJailedSpawnLineage(t *testing.T, jail *runJail, parent, forbiddenParent, wantCWD string) {
	t.Helper()
	entries, err := os.ReadDir(jail.tmuxDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("jailed socket count=%d err=%v", len(entries), err)
	}
	socket := entries[0].Name()
	state := fleetdb.OpenSharedState(context.Background(), paths.Values{
		FleetDB: filepath.Join(jail.root, "home", ".cc", "fleet.db"),
	})
	t.Cleanup(func() { _ = state.Close() })
	children, found, err := state.Children(context.Background(), fleetdb.KindNew, parent)
	if err != nil || !found || len(children) != 1 || children[0] != socket {
		t.Fatalf("children[%q] = %q found=%v err=%v, want %q", parent, children, found, err, socket)
	}
	if forbiddenParent != "" {
		children, _, err = state.Children(context.Background(), fleetdb.KindNew, forbiddenParent)
		if err != nil || len(children) != 0 {
			t.Fatalf("children[%q] = %q err=%v, want none", forbiddenParent, children, err)
		}
	}
	events, err := state.CommsSince(context.Background(), 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].SenderSession != parent {
		t.Fatalf("spawn events = %#v, want request parent %q", events, parent)
	}
	transcriptText := jail.await(t, filepath.Join("claude", "stub", filepath.Base(jail.transcript)), "lineage")
	if !strings.Contains(transcriptText, `"cwd":"`+wantCWD+`"`) {
		t.Fatalf("spawn transcript = %q, want cwd %q", transcriptText, wantCWD)
	}
}

func TestMCPChatNewResolvesRelativeCWDAndUsesRequestParent(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newRunJail(t)
	defer jail.killSockets(t)
	callerDir := filepath.Join(jail.root, "work")
	childDir := filepath.Join(callerDir, "child")
	if err := os.MkdirAll(childDir, 0o700); err != nil {
		t.Fatal(err)
	}
	const requestParent = "request-caller"
	const daemonParent = "daemon-caller"
	t.Setenv("CLAUDE_CODE_SESSION_ID", daemonParent)
	runtime, err := pfmconfig.LoadRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	callJailedChatNew(t, runtime, compose.Row{
		Kind: compose.LiveClaude, ID: requestParent, CWD: callerDir,
		SessionName: "caller-seat", Socket: "caller-socket", PaneID: "%1",
	}, mcpserv.NewInput{
		Name: "relative child", Engine: "claude", CWD: "child", Prompt: "lineage prompt",
	})
	assertJailedSpawnLineage(t, jail, requestParent, daemonParent, childDir)
}

func TestDirectChatNewKeepsAmbientParent(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newRunJail(t)
	defer jail.killSockets(t)
	const parent = "ambient-caller"
	t.Setenv("CLAUDE_CODE_SESSION_ID", parent)
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"chat", "new", "--engine", "claude", "--name", "direct child",
		"--cwd", filepath.Join(jail.root, "work"), "lineage prompt",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("chat new exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	assertJailedSpawnLineage(t, jail, parent, "", filepath.Join(jail.root, "work"))
}

func TestParentChatIDUsesScopedCallerIncludingEmptyID(t *testing.T) {
	env := &paths.MapEnv{Values: map[string]string{
		"CLAUDE_CODE_SESSION_ID": "daemon-caller",
		"CODEX_THREAD_ID":        "daemon-codex",
	}}
	if got := parentChatID(context.Background(), env); got != "daemon-caller" {
		t.Fatalf("direct CLI parent = %q, want daemon environment parent", got)
	}
	ctx := pfmchat.WithResolvedSelf(context.Background(), headless.Chat{ID: "request-caller"})
	if got := parentChatID(ctx, env); got != "request-caller" {
		t.Fatalf("request parent = %q, want request caller", got)
	}
	ctx = pfmchat.WithResolvedSelf(context.Background(), headless.Chat{})
	if got := parentChatID(ctx, env); got != "" {
		t.Fatalf("empty scoped parent = %q, want no ambient fallback", got)
	}
}

func TestChatNewCancellationReachesSpawnAndAwait(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	t.Run("before spawn", func(t *testing.T) {
		jail := newRunJail(t)
		defer jail.killSockets(t)
		runtime, err := pfmconfig.LoadRuntime("")
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(pfmchat.WithResolvedSelf(
			context.Background(), headless.Chat{ID: "request-caller", CWD: filepath.Join(jail.root, "work")},
		))
		cancel()
		var stdout, stderr bytes.Buffer
		code := runChatWithRuntime([]string{
			"new", "--engine", "claude", "--name", "cancelled child",
			"--cwd", filepath.Join(jail.root, "work"),
		}, strings.NewReader(""), &stdout, &stderr, runtime, ctx)
		if code != 1 || !strings.Contains(stderr.String(), context.Canceled.Error()) {
			t.Fatalf("cancelled spawn exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
		if entries, readErr := os.ReadDir(jail.tmuxDir); readErr != nil || len(entries) != 0 {
			t.Fatalf("cancelled spawn sockets=%v err=%v", entries, readErr)
		}
	})

	t.Run("during await", func(t *testing.T) {
		jail := newRunJail(t)
		defer jail.killSockets(t)
		t.Setenv("CC_STUB_MUTE", "1")
		runtime, err := pfmconfig.LoadRuntime("")
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(pfmchat.WithResolvedSelf(
			context.Background(), headless.Chat{ID: "request-caller", CWD: filepath.Join(jail.root, "work")},
		))
		defer cancel()
		go func() {
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				content, readErr := os.ReadFile(jail.transcript)
				if readErr == nil && strings.Contains(string(content), "cancel await") {
					cancel()
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
		}()
		var stdout, stderr bytes.Buffer
		code := runChatWithRuntime([]string{
			"new", "--engine", "claude", "--name", "await child",
			"--cwd", filepath.Join(jail.root, "work"), "--await", "--timeout", "1", "cancel await",
		}, strings.NewReader(""), &stdout, &stderr, runtime, ctx)
		if code != 1 || !strings.Contains(stderr.String(), context.Canceled.Error()) ||
			strings.Contains(stderr.String(), "died at birth") {
			t.Fatalf("cancelled await exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
	})
}
