package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	pfmconfig "hostops/pfm/internal/config"
	"hostops/pfm/internal/headless"
	"hostops/pfm/internal/obs"
	"hostops/pfm/internal/paths"
)

// TestChatKillAndUnkillRecordChatStateTransitions: the CLI kill/unkill verbs
// (cmd/pfm/chat_command.go's runResolvedChatKill and runChatUnkill) walk the
// state door (spec § Middleware, `state`) on their real success path —
// comp=state, kind=chat — never the resolved chat's id or socket. Pattern:
// TestKillCLIVouchesEngineForUnindexedButVisibleRows in
// kill_cli_engine_jail_test.go, whose real tmux + jailed /proc fixture this
// reuses.
func TestChatKillAndUnkillRecordChatStateTransitions(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newKillCLIJail(t)

	socket := "probe-killobs-" + strconv.Itoa(os.Getpid())
	session := exec.Command(
		"tmux", "-L", socket, "-f", "/dev/null",
		"new-session", "-d", "-s", "bg", "sleep", "120",
	)
	if output, err := session.CombinedOutput(); err != nil {
		t.Fatalf("start background pane: %v: %s", err, output)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", socket, "kill-server").Run()
	})
	paneOutput, err := exec.Command(
		"tmux", "-L", socket, "list-panes", "-F", "#{pane_pid}",
	).Output()
	if err != nil {
		t.Fatal(err)
	}
	panePID, err := strconv.Atoi(strings.TrimSpace(string(paneOutput)))
	if err != nil {
		t.Fatalf("parse pane pid %q: %v", paneOutput, err)
	}

	const agentID = "e6666666-6666-4666-8666-666666666666"
	writeFakeProcess(t, jail.procRoot, fakeProcessSpec{
		pid:       90201,
		parentPID: panePID,
		comm:      "claude",
		cmdline:   []string{"/opt/claude", "--session-id", agentID},
		environ: map[string]string{
			"CLAUDE_CONFIG_DIR": filepath.Join(jail.home, ".cc", "2"),
		},
	})

	// run()'s own openActivityLog re-installs the process logger on every
	// call, so this drives the verbs directly — exactly what run() dispatches
	// to for "chat kill"/"chat unkill" — leaving obs.Test's recorder in place.
	runtime, err := pfmconfig.LoadRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	_, recorder := obs.Test(t)
	var killOut, killErr strings.Builder
	if code := runChatKill([]string{agentID}, &killOut, &killErr, paths.OSEnv{}, runtime); code != 0 {
		t.Fatalf("chat kill code=%d stdout=%q stderr=%q", code, killOut.String(), killErr.String())
	}
	var unkillOut, unkillErr strings.Builder
	if code := runChatUnkill([]string{agentID}, &unkillOut, &unkillErr, runtime); code != 0 {
		t.Fatalf("chat unkill code=%d stdout=%q stderr=%q", code, unkillOut.String(), unkillErr.String())
	}

	var nexts []string
	for _, record := range recorder.Records() {
		if record.Message != "state.transition" {
			continue
		}
		if kind, _ := record.Field("kind"); kind != "chat" {
			continue
		}
		next, _ := record.Field("next")
		nexts = append(nexts, next.(string))
	}
	if len(nexts) != 2 || nexts[0] != "killed" || nexts[1] != "live" {
		t.Fatalf("chat state path = %v, want [killed live]: %s", nexts, recorder.Raw())
	}
}

// TestChatKillOfAnUnknownTargetRecordsAFailedChatStateTransition: a target
// that never resolves (runChatKill's renderNoSuchChat branch, reached before
// the old success-only recording point) still walks the chat state door — a
// failed live->killed transition, its err naming only the verb and exit code,
// never the target string. The smallest fixture: an empty jailed fleet, no
// tmux required, since the target itself is never found.
func TestChatKillOfAnUnknownTargetRecordsAFailedChatStateTransition(t *testing.T) {
	newKillCLIJail(t)
	runtime, err := pfmconfig.LoadRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	_, recorder := obs.Test(t)
	var stdout, stderr strings.Builder
	code := runChatKill([]string{"no-such-chat-at-all"}, &stdout, &stderr, paths.OSEnv{}, runtime)
	if code != codeUnknownChat {
		t.Fatalf("chat kill of an unknown target code=%d, want codeUnknownChat: stdout=%q stderr=%q",
			code, stdout.String(), stderr.String())
	}
	var found bool
	for _, record := range recorder.Records() {
		if record.Message != "state.transition" {
			continue
		}
		if kind, _ := record.Field("kind"); kind != "chat" {
			continue
		}
		next, _ := record.Field("next")
		if next != "killed" {
			continue
		}
		if record.Level != "ERROR" {
			t.Fatalf("chat kill failure record level = %q, want ERROR: %s", record.Level, recorder.Raw())
		}
		if errField, hasErr := record.Field(obs.FieldErr); !hasErr || errField != "chat kill exited 4" {
			t.Fatalf("chat kill failure err = %v (found=%v), want %q", errField, hasErr, "chat kill exited 4")
		}
		found = true
	}
	if !found {
		t.Fatalf("chat kill of an unknown target wrote no failed chat->killed transition: %s", recorder.Raw())
	}
}

// TestApplyChatNameRecordsAChatStateTransition: the "name" verb
// (cmd/pfm/chat_command.go's applyChatName) walks the state door on a
// delivered rename — comp=state, kind=chat, never the delivered name text
// itself. Pattern: TestChatNameConvergesTheWindowInlineOnAProbeSocket in
// chat_name_jail_test.go, whose real tmux probe fixture this reuses.
func TestApplyChatNameRecordsAChatStateTransition(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	socket := filepath.Join(t.TempDir(), "probe-nameobs.sock")
	session := "probe-nameobs"
	start := exec.Command(
		"tmux", "-S", socket, "-f", "/dev/null",
		"new-session", "-d", "-s", session, "-n", "before", "sleep", "120",
	)
	if output, err := start.CombinedOutput(); err != nil {
		t.Fatalf("start probe server: %v: %s", err, output)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-S", socket, "kill-server").Run()
	})

	deliver := func(context.Context, headless.Chat, string) (int, string, error) {
		return 0, "", nil
	}
	var stderr bytes.Buffer
	_, recorder := obs.Test(t)
	code := applyChatName(context.Background(), headless.Chat{
		ID:      "probe-obs-id",
		Name:    "before",
		Socket:  socket,
		Session: session,
		Live:    true,
	}, "after", deliver, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("applyChatName() code=%d stderr=%q", code, stderr.String())
	}
	var found bool
	for _, record := range recorder.Records() {
		if record.Message != "state.transition" {
			continue
		}
		if kind, _ := record.Field("kind"); kind != "chat" {
			continue
		}
		if next, _ := record.Field("next"); next == "renamed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("applyChatName() wrote no chat->renamed transition: %s", recorder.Raw())
	}
}
