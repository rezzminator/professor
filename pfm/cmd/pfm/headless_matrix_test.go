package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/action"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/testjail"
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
		{"call without a message", []string{"chat", "call", "x"}, 2},
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
	// Its own fleet: caller placement reads the tmux sockets under TMUX_TMPDIR,
	// and a sibling test's probe server dying there must not answer for it.
	testjail.Fleet(t)
	var stdout, stderr bytes.Buffer
	code := run([]string{"chat", "new", "--name", "x", "--engine", "bogus"}, &stdout, &stderr)
	if want := "pfm chat new: unknown engine \"bogus\" (want cc/claude, cx/codex, ox/opencode)\n"; code != 2 ||
		stderr.String() != want {
		t.Fatalf("chat new bogus exit=%d stdout=%q stderr = %q, want %q", code, stdout.String(), stderr.String(), want)
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
	home := t.TempDir()
	if err := os.MkdirAll(pfmconfig.DefaultAccountDir(home, 1), 0o700); err != nil {
		t.Fatal(err)
	}
	machine := pfmconfig.Defaults(home, []string{filepath.Join(pfmconfig.DefaultAccountDir(home, 1), "projects")})
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
	golden(t, "headless_runs.txt", scrub(claude.Run+"\n"+codex.Run+"\n", home))
}
