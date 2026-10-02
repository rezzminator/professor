package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleet"
	"github.com/rezzminator/professor/pfm/internal/kill"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/reload"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func reloadWorkerAccountFixture(t *testing.T) (string, string, string) {
	t.Helper()
	root := jailTest(t)
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	promptScript := filepath.Join(t.TempDir(), "prompt.py")
	if err := testjail.WriteExecutable(promptScript, []byte(reloadPromptFixture), 0o700); err != nil {
		t.Fatal(err)
	}
	captured := filepath.Join(t.TempDir(), "account.txt")
	fixtureClaude := filepath.Join(t.TempDir(), "claude-fixture.sh")
	script := "#!/bin/sh\nprintf 'ACCOUNT:%s\\n' \"${CLAUDE_CONFIG_DIR:-}\" > '" + captured + "'\nexec bash -c 'exec -a claude-fixture.sh python3 " + promptScript + "'\n"
	if err := testjail.WriteExecutable(fixtureClaude, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := writeConfigFixture(t, root, `{
  "version": 1,
  "accounts": [
    {"id": 1, "configDir": "`+filepath.Join(root, "account-1")+`", "claude": {"binary": "`+fixtureClaude+`"}},
    {"id": 2, "configDir": "`+filepath.Join(root, "account-2")+`", "claude": {"binary": "`+fixtureClaude+`"}}
  ]
}`)
	socket := probeReloadSocket(t, "account")
	server := exec.Command(
		"tmux",
		"-S",
		socket,
		"-f",
		"/dev/null",
		"new-session",
		"-d",
		"-s",
		"probe",
		"python3",
		promptScript,
	)
	server.Env = append(server.Environ(), "TMUX=")
	if output, err := server.CombinedOutput(); err != nil {
		t.Fatalf("start probe socket: %v: %s", err, output)
	}
	cleanupProbeReloadSocket(t, socket)
	paneOutput, err := exec.Command("tmux", "-S", socket, "list-panes", "-F", "#{pane_id}").Output()
	if err != nil {
		t.Fatalf("read probe pane: %v", err)
	}
	pane := strings.TrimSpace(string(paneOutput))
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(resolved.SIDDir, 0o700); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(t.TempDir(), "44444444-4444-4444-8444-444444444444.jsonl")
	if err := os.WriteFile(transcript, []byte(`{"cwd":"`+t.TempDir()+`"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	crumb := filepath.Join(resolved.SIDDir, filepath.Base(socket)+"."+pane)
	if err := os.WriteFile(crumb, []byte(transcript+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PFM_RELOAD_DELAY_MS", "0")
	t.Setenv("PFM_RELOAD_POLL_MS", "20")
	t.Setenv("PFM_RELOAD_EXIT_TRIES", "50")
	t.Setenv("PFM_RELOAD_IDLE_TRIES", "500")
	t.Setenv("PFM_RELOAD_THEN_TRIES", "500")
	return configPath, socket, captured
}

func reloadWorkerCapturedAccount(t *testing.T, captured string) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		data, err := os.ReadFile(captured)
		if err == nil && len(data) > 0 {
			return strings.TrimSpace(string(data))
		}
		if time.Now().After(deadline) {
			t.Fatalf("reborn claude did not record its account: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestChatReloadWorkerFailureReachesThePaneAndLog(t *testing.T) {
	configPath, socket, _ := reloadWorkerAccountFixture(t)
	runtime, err := pfmconfig.LoadRuntime(configPath)
	if err != nil {
		t.Fatal(err)
	}
	paneOutput, err := exec.Command("tmux", "-S", socket, "list-panes", "-F", "#{pane_id}").Output()
	if err != nil {
		t.Fatal(err)
	}
	pane := strings.TrimSpace(string(paneOutput))
	lock, err := os.OpenFile(
		reload.LockPath(runtime.Paths.SIDDir, filepath.Base(socket), pane),
		os.O_CREATE|os.O_WRONLY,
		0o600,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := lock.Close(); err != nil {
			t.Errorf("close lock: %v", err)
		}
	}()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PFM_RELOAD_POLL_MS", "0")
	t.Setenv("PFM_RELOAD_IDLE_TRIES", "3")
	oldDisplay := displayReloadWorkerFailure
	t.Cleanup(func() { displayReloadWorkerFailure = oldDisplay })
	var displayed string
	displayReloadWorkerFailure = func(_ context.Context, gotSocket, gotPane, message string) error {
		if gotSocket != socket || gotPane != pane {
			t.Errorf("announced on %q %q, want %q %q", gotSocket, gotPane, socket, pane)
		}
		displayed = message
		return nil
	}
	var stdout, stderr bytes.Buffer
	code := runChatReloadWorkerWithRuntime(
		[]string{"--sock", socket, "--pane", pane, "--then", "S"},
		&stdout, &stderr, runtime, nil,
	)
	if code != 1 || !strings.Contains(displayed, "another reload of this pane is already in flight") {
		t.Fatalf("rc=%d pane=%q log=%q", code, displayed, stderr.String())
	}
	if !strings.Contains(stderr.String(), displayed) {
		t.Fatalf("pane message %q was absent from worker log %q", displayed, stderr.String())
	}
	displayed = ""
	stdout.Reset()
	stderr.Reset()
	code = runChatReloadWorkerWithRuntime(
		[]string{"--sock", socket, "--pane", pane, "--account", "99"},
		&stdout, &stderr, runtime, nil,
	)
	if code != 2 || !strings.Contains(displayed, "account 99") || !strings.Contains(stderr.String(), displayed) {
		t.Fatalf("account error rc=%d pane=%q log=%q", code, displayed, stderr.String())
	}
}

func TestChatReloadWorkerConfigFailureReachesPaneAndLog(t *testing.T) {
	jailTest(t)
	malformedConfig := filepath.Join(t.TempDir(), "malformed-config.json")
	if err := os.WriteFile(malformedConfig, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(t.TempDir(), "tmux.sock")
	oldDisplay := displayReloadWorkerFailure
	t.Cleanup(func() { displayReloadWorkerFailure = oldDisplay })

	for _, tc := range []struct {
		name        string
		args        []string
		displayErr  error
		wantDisplay bool
	}{
		{name: "pane", args: []string{"--sock", socket, "--pane", "%7", "--then", "S"}, wantDisplay: true},
		{name: "missing socket", args: []string{"--pane", "%7"}},
		{name: "missing pane", args: []string{"--sock", socket}},
		{name: "dead pane", args: []string{"--sock", socket, "--pane", "%7"}, displayErr: errors.New("pane gone"), wantDisplay: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var displayed string
			var displayCalls int
			displayReloadWorkerFailure = func(_ context.Context, gotSocket, gotPane, message string) error {
				displayCalls++
				if gotSocket != socket || gotPane != "%7" {
					t.Errorf("display target = %q %q, want %q %%7", gotSocket, gotPane, socket)
				}
				displayed = message
				return tc.displayErr
			}
			var stdout, stderr bytes.Buffer
			args := append([]string{"--config", malformedConfig, internalCommand, reloadRunCommand}, tc.args...)
			if code := run(args, &stdout, &stderr); code != 1 {
				t.Fatalf("exit = %d, want 1; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			line := strings.SplitN(stderr.String(), "\n", 2)[0]
			if !strings.HasPrefix(line, "pfm: config: ") {
				t.Fatalf("config failure absent from worker log: %q", stderr.String())
			}
			if tc.wantDisplay {
				if displayCalls != 1 || displayed != line {
					t.Fatalf("display calls=%d message=%q, want config line %q", displayCalls, displayed, line)
				}
			} else if displayCalls != 0 || !strings.Contains(stderr.String(), "cannot announce worker failure: socket or pane missing") {
				t.Fatalf("missing target: display calls=%d log=%q", displayCalls, stderr.String())
			}
			wantDisplayFailure := "display worker failure on " + socket + " %7: pane gone"
			if tc.displayErr != nil && !strings.Contains(stderr.String(), wantDisplayFailure) {
				t.Fatalf("display failure absent from worker log: %q", stderr.String())
			}
		})
	}
}

func TestChatReloadWorkerNeverReadsAThenPayloadAsASeat(t *testing.T) {
	configPath, socket, captured := reloadWorkerAccountFixture(t)
	runtime, err := pfmconfig.LoadRuntime(configPath)
	if err != nil {
		t.Fatal(err)
	}
	// --then verifies the reborn process; the fixture pane lives in this fence.
	runtime.Paths.ProcRoot = "/proc"
	var stdout, stderr bytes.Buffer
	code := runChatReloadWorkerWithRuntime(
		[]string{"--sock", socket, "--then", "--account 4", "--cache", "5m"},
		&stdout, &stderr, runtime, nil,
	)
	if code != 0 {
		t.Fatalf("reload rc=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if got := reloadWorkerCapturedAccount(t, captured); !strings.HasSuffix(got, "/account-1") {
		t.Fatalf("reborn claude %q, want account 1", got)
	}
	output, err := exec.Command("tmux", "-S", socket, "capture-pane", "-p").Output()
	if err != nil {
		t.Fatalf("capture reborn pane: %v", err)
	}
	if !strings.Contains(string(output), "--account 4") {
		t.Fatalf("reborn pane never received --then payload: %q", output)
	}
}

func TestChatReloadWorkerTakesTheAccountAfterNewAndHide(t *testing.T) {
	configPath, socket, captured := reloadWorkerAccountFixture(t)
	runtime, err := pfmconfig.LoadRuntime(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := runChatReloadWorkerWithRuntime(
		[]string{"--sock", socket, "--new", "--hide", "--account", "2"},
		&stdout, &stderr, runtime, nil,
	)
	if code != 0 {
		t.Fatalf("reload rc=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if got := reloadWorkerCapturedAccount(t, captured); !strings.HasSuffix(got, "/account-2") {
		t.Fatalf("reborn claude %q, want account 2", got)
	}
}

func TestChatReloadWorkerContinuesAccountAfterNew(t *testing.T) {
	configPath, socket, captured := reloadWorkerAccountFixture(t)
	runtime, err := pfmconfig.LoadRuntime(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runChatReloadWorkerWithRuntime(
		[]string{"--sock", socket, "--new", "--account", "2"}, &stdout, &stderr, runtime, nil,
	); code != 0 {
		t.Fatalf("first reload rc=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if got := reloadWorkerCapturedAccount(t, captured); !strings.HasSuffix(got, "/account-2") {
		t.Fatalf("first reborn account=%q", got)
	}
	if err := os.Remove(captured); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := runChatReloadWorkerWithRuntime(
		[]string{"--sock", socket, "--cache", "5m"}, &stdout, &stderr, runtime, nil,
	); code != 0 {
		t.Fatalf("queued reload rc=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if got := reloadWorkerCapturedAccount(t, captured); !strings.HasSuffix(got, "/account-2") {
		t.Fatalf("continued account=%q stderr=%q", got, stderr.String())
	}
}

func TestChatReloadWorkerContinuesBoundCodexConversationAfterNew(t *testing.T) {
	root := jailTest(t)
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	const (
		oldID = "11111111-1111-4111-8111-111111111111"
		newID = "22222222-2222-4222-8222-222222222222"
		then  = "BOUND-CODEX-THEN"
	)
	promptScript := filepath.Join(t.TempDir(), "prompt.py")
	if err := testjail.WriteExecutable(
		promptScript,
		[]byte(strings.ReplaceAll(reloadPromptFixture, "❯", "›")),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	argvFile := filepath.Join(t.TempDir(), "codex-argv.txt")
	fixtureCodex := filepath.Join(t.TempDir(), "codex-fixture.sh")
	script := "#!/bin/sh\n{ printf 'CALL\\n'; for a in \"$@\"; do printf 'ARG:%s\\n' \"$a\"; done; } >> '" + argvFile + "'\nexec bash -c 'exec -a codex-fixture.sh python3 " + promptScript + "'\n"
	if err := testjail.WriteExecutable(fixtureCodex, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	codexHome := filepath.Join(root, "codex")
	configPath := writeConfigFixture(
		t,
		root,
		`{"version":1,"codex":{"binary":"`+fixtureCodex+`","homes":[{"id":1,"home":"`+codexHome+`"}]}}`,
	)
	runtime, err := pfmconfig.LoadRuntime(configPath)
	if err != nil {
		t.Fatal(err)
	}
	runtime.Paths.ProcRoot = "/proc"
	socket := strings.Replace(probeReloadSocket(t, "bound-new"), "cc-probe-", "cx-probe-", 1)
	server := exec.Command(
		"tmux",
		"-S",
		socket,
		"-f",
		"/dev/null",
		"new-session",
		"-d",
		"-s",
		"probe",
		"python3",
		promptScript,
	)
	server.Env = append(server.Environ(), "TMUX=")
	if output, err := server.CombinedOutput(); err != nil {
		t.Fatalf("start Codex probe socket: %v: %s", err, output)
	}
	cleanupProbeReloadSocket(t, socket)
	paneOutput, err := exec.Command("tmux", "-S", socket, "list-panes", "-F", "#{pane_id}").Output()
	if err != nil {
		t.Fatal(err)
	}
	pane := strings.TrimSpace(string(paneOutput))
	for _, id := range []string{oldID, newID} {
		rollout := filepath.Join(codexHome, "sessions", "rollout-2026-08-24T00-00-00-"+id+".jsonl")
		if err := os.MkdirAll(filepath.Dir(rollout), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(rollout, []byte(`{"cwd":"`+root+`"}`+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	bind := func(id string) {
		database, err := store.Open()
		if err != nil {
			t.Fatal(err)
		}
		manager, err := kill.New(database, fleet.KillDependencies(runtime))
		if err != nil {
			_ = database.Close()
			t.Fatal(err)
		}
		if _, _, err := manager.AdvanceCodexPane(context.Background(), filepath.Base(socket), pane, id); err != nil {
			_ = database.Close()
			t.Fatal(err)
		}
		if err := database.Close(); err != nil {
			t.Fatal(err)
		}
	}
	bind(oldID)
	t.Setenv("PFM_RELOAD_DELAY_MS", "0")
	t.Setenv("PFM_RELOAD_POLL_MS", "20")
	t.Setenv("PFM_RELOAD_EXIT_TRIES", "50")
	t.Setenv("PFM_RELOAD_IDLE_TRIES", "500")
	t.Setenv("PFM_RELOAD_THEN_TRIES", "500")
	var stdout, stderr bytes.Buffer
	if code := runChatReloadWorkerWithRuntime(
		[]string{"--sock", socket, "--new", "--hide"},
		&stdout,
		&stderr,
		runtime,
		nil,
	); code != 0 {
		t.Fatalf("new reload rc=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	lockPath := reload.LockPath(runtime.Paths.SIDDir, filepath.Base(socket), pane)
	readHandoff := func() (string, string) {
		content, err := os.ReadFile(lockPath)
		if err != nil {
			t.Fatal(err)
		}
		var record struct {
			SessionID  string `json:"session_id"`
			LeftBehind string `json:"left_behind"`
		}
		if err := json.Unmarshal(content, &record); err != nil {
			t.Fatal(err)
		}
		return record.SessionID, record.LeftBehind
	}
	if sessionID, leftBehind := readHandoff(); sessionID != "" || leftBehind != oldID {
		t.Fatalf("new handoff session=%q left behind=%q", sessionID, leftBehind)
	}
	bind(newID)
	stdout.Reset()
	stderr.Reset()
	if code := runChatReloadWorkerWithRuntime(
		[]string{"--sock", socket, "--then", then},
		&stdout,
		&stderr,
		runtime,
		nil,
	); code != 0 {
		t.Fatalf("bound reload rc=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "continuing from session "+newID+" on account 1") {
		t.Fatalf("continued conversation absent from stderr: %q", stderr.String())
	}
	if sessionID, leftBehind := readHandoff(); sessionID != newID || leftBehind != newID {
		t.Fatalf("continued handoff session=%q left behind=%q", sessionID, leftBehind)
	}
	argv, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(argv), "ARG:resume\nARG:"+newID+"\n") {
		t.Fatalf("reborn Codex did not resume bound conversation: %q", argv)
	}
	output, err := exec.Command("tmux", "-S", socket, "capture-pane", "-p").Output()
	if err != nil || !strings.Contains(string(output), then) {
		t.Fatalf("reborn pane steer=%q err=%v", output, err)
	}
}

func TestChatReloadWorkerHidesTheSessionItContinuedFrom(t *testing.T) {
	configPath, socket, _ := reloadWorkerAccountFixture(t)
	runtime, err := pfmconfig.LoadRuntime(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runChatReloadWorkerWithRuntime(
		[]string{"--sock", socket, "--new", "--account", "2"}, &stdout, &stderr, runtime, nil,
	); code != 0 {
		t.Fatalf("first reload rc=%d stderr=%q", code, stderr.String())
	}
	paneOutput, err := exec.Command("tmux", "-S", socket, "list-panes", "-F", "#{pane_id}").Output()
	if err != nil {
		t.Fatal(err)
	}
	record, err := os.ReadFile(
		reload.LockPath(runtime.Paths.SIDDir, filepath.Base(socket), strings.TrimSpace(string(paneOutput))),
	)
	if err != nil {
		t.Fatal(err)
	}
	var handoff struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(record, &handoff); err != nil || handoff.SessionID == "" {
		t.Fatalf("record=%q error=%v", record, err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := runChatReloadWorkerWithRuntime(
		[]string{"--sock", socket, "--new", "--hide"}, &stdout, &stderr, runtime, nil,
	); code != 0 || !strings.Contains(stdout.String(), "hid the conversation left behind ("+handoff.SessionID+")") {
		t.Fatalf("second reload rc=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestChatReloadWorkerRefusesTheAccountTwice(t *testing.T) {
	root := jailTest(t)
	configPath := writeConfigFixture(
		t,
		root,
		`{"version":1,"accounts":[{"id":1,"configDir":"`+filepath.Join(root, "account-1")+`"}]}`,
	)
	runtime, err := pfmconfig.LoadRuntime(configPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"2", "--account", "3"},
		{"--account", "2", "--account", "3"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := runChatReloadWorkerWithRuntime(args, &stdout, &stderr, runtime, nil); code != 2 {
				t.Fatalf("args=%q rc=%d, want 2; stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), "account specified twice") {
				t.Fatalf("args=%q stderr=%q, want duplicate account refusal", args, stderr.String())
			}
		})
	}
}
