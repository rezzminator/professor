package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hostops/pfm/internal/action"
	pfmconfig "hostops/pfm/internal/config"
	"hostops/pfm/internal/paths"
)

func TestHeadlessCompatibilityAliasExposesPublicHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"headless", "help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "headless exec") ||
		!strings.Contains(stderr.String(), "--output-format text|json|native") ||
		strings.Contains(stderr.String(), "deprecated") {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestChatArgumentMatrix(t *testing.T) {
	for _, testCase := range []struct {
		name string
		args []string
		want int
	}{
		{"no verb", []string{"chat"}, 2},
		{"unknown verb", []string{"chat", "frobnicate"}, 2},
		{"help", []string{"chat", "help"}, 0},
		{"new without a name", []string{"chat", "new"}, 2},
		{"new with an unknown engine", []string{"chat", "new", "--name", "x", "--engine", "gpt"}, 2},
		{"status without a target", []string{"chat", "status"}, 2},
		{"read with tail 0", []string{"chat", "read", "x", "--tail", "0"}, 2},
		{"stream with a bad regexp", []string{"chat", "stream", "x", "--filter", "("}, 2},
		{"inject without a message", []string{"chat", "inject", "x"}, 2},
		{"watch with a bad poll", []string{"chat", "watch", "x", "--poll", "0"}, 2},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(testCase.args, &stdout, &stderr); code != testCase.want {
				t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)",
					code, testCase.want, stdout.String(), stderr.String())
			}
		})
	}
}

func TestCLIEngineEdgeRejectsUnknownWithAcceptedSet(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"chat", "new", "--name", "x", "--engine", "bogus"}, &stdout, &stderr)
	if code != 2 ||
		!strings.Contains(stderr.String(), `unknown engine "bogus" (want cc/claude, cx/codex, ox/opencode)`) {
		t.Fatalf("chat new bogus exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

// TestRunPromptSourcesAreExclusive pins --prompt-file's contract: it is the
// transport for briefs too big to inline, and taking both would deliver a
// truncated one while looking successful.
func TestRunPromptSourcesAreExclusive(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "brief.md")
	if err := os.WriteFile(path, []byte("audit the firewall\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	prompt, err := runPrompt(path, nil)
	if err != nil || prompt != "audit the firewall" {
		t.Fatalf("file prompt = %q err = %v", prompt, err)
	}
	prompt, err = runPrompt("", []string{"do", "the", "thing"})
	if err != nil || prompt != "do the thing" {
		t.Fatalf("inline prompt = %q err = %v", prompt, err)
	}
	if _, err := runPrompt(path, []string{"also", "this"}); err == nil {
		t.Fatal("a file AND an inline prompt were accepted")
	}
	if _, err := runPrompt(filepath.Join(directory, "missing.md"), nil); err == nil {
		t.Fatal("a missing prompt file was accepted")
	}
	empty := filepath.Join(directory, "empty.md")
	if err := os.WriteFile(empty, []byte("   \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runPrompt(empty, nil); err == nil {
		t.Fatal("an empty prompt file was accepted")
	}
}

// TestModelAndEffortReachBothEngines proves item 4 of the order: a seat is
// born with its tier, on the engine's own spelling.
func TestModelAndEffortReachBothEngines(t *testing.T) {
	home := "/home/tester"
	machine := pfmconfig.Defaults(home, []string{pfmconfig.DefaultAccountProjectDir(home, 1)})
	machine.CodexAccounts = []pfmconfig.CodexAccount{{ID: 1, Home: home + "/.codex"}}
	claude, err := action.HeadlessRun(action.HeadlessRequest{
		Engine:         "cc",
		Name:           "seat",
		CWD:            "/work/alpha",
		Home:           home,
		PrimaryAccount: 1,
		Config:         machine,
		Model:          "claude-opus-5",
		Effort:         "XHIGH",
	})
	if err != nil {
		t.Fatalf("claude plan error = %v", err)
	}
	if !strings.Contains(claude.Run, "'--model' 'claude-opus-5'") ||
		!strings.Contains(claude.Run, "'--effort' 'xhigh'") {
		t.Fatalf("claude run = %s", claude.Run)
	}

	codex, err := action.HeadlessRun(action.HeadlessRequest{
		Engine:         "cx",
		Name:           "seat",
		CWD:            "/work/alpha",
		Home:           home,
		PrimaryAccount: 1,
		Config:         machine,
		Model:          "gpt-5.6-sol",
		Effort:         "high",
	})
	if err != nil {
		t.Fatalf("codex plan error = %v", err)
	}
	if !strings.Contains(codex.Run, "'--model' 'gpt-5.6-sol'") ||
		!strings.Contains(codex.Run, `'-c' 'model_reasoning_effort="high"'`) {
		t.Fatalf("codex run = %s", codex.Run)
	}
}

// TestUnknownChatIsRc4WithAMachineShape is STM's hard rule at the CLI edge:
// never empty output with rc 0.
func TestUnknownChatIsRc4WithAMachineShape(t *testing.T) {
	jail := newRunJail(t)
	defer jail.killSockets(t)

	for _, testCase := range []struct {
		name string
		args []string
	}{
		{"status", []string{"chat", "status", "ghost"}},
		{"last", []string{"chat", "last", "ghost"}},
		{"read", []string{"chat", "read", "ghost"}},
		{"stream", []string{"chat", "stream", "ghost"}},
		{"inject", []string{"chat", "inject", "ghost", "hello"}},
		{"watch", []string{"chat", "watch", "ghost"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(testCase.args, &stdout, &stderr)
			if code != codeUnknownChat {
				t.Fatalf("exit = %d, want %d (stderr=%q)", code, codeUnknownChat, stderr.String())
			}
			if !strings.Contains(stderr.String(), "no chat named") {
				t.Fatalf("stderr = %q", stderr.String())
			}
		})
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"chat", "status", "ghost", "--json"}, &stdout, &stderr); code != codeUnknownChat {
		t.Fatalf("json exit = %d", code)
	}
	var status map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		t.Fatalf("status --json is not JSON: %q", stdout.String())
	}
	if status["state"] != "not-found" {
		t.Fatalf("status = %v", status)
	}
}

// TestScanFailureIsRc2NeverUnknownChat is the other half of that rule: when
// the fleet scan cannot look (its index database cannot open), a read verb
// exits 2 with the failure — never rc 4 and a "not-found" row that would tell
// the caller the chat does not exist.
func TestScanFailureIsRc2NeverUnknownChat(t *testing.T) {
	jail := newRunJail(t)
	defer jail.killSockets(t)
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvDB, filepath.Join(blocker, "index.db"))
	for _, args := range [][]string{{"chat", "status", "ghost", "--json"}, {"chat", "last", "ghost"}} {
		var stdout, stderr bytes.Buffer
		code := run(args, &stdout, &stderr)
		if code != 2 || strings.Contains(stderr.String(), "no chat named") ||
			strings.Contains(stdout.String(), "not-found") {
			t.Fatalf(
				"%v with a broken index = %d stdout=%q stderr=%q; want rc 2 naming the failure",
				args,
				code,
				stdout.String(),
				stderr.String(),
			)
		}
	}
}
