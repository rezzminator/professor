//go:build jail

// Subject: the real shell evaluating the quoted words and the launcher scripts of a plan.

package action

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/cmdparse"
	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestQuoteRoundTripsHostileWords(t *testing.T) {
	values := []string{
		"",
		"plain",
		"space dir",
		"single'quote",
		"$HOME $(touch nope) `touch nope2`; newline\nnext",
		"tail\n",
	}
	for _, value := range values {
		outputPath := filepath.Join(t.TempDir(), "word")
		script := "set -- " + cmdparse.Quote(value) +
			"; printf %s \"$1\" > " + cmdparse.Quote(outputPath)
		command := exec.Command("sh", "-c", script)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("sh round trip %q: %v: %s", value, err, output)
		}
		content, err := os.ReadFile(outputPath)
		if err != nil {
			t.Fatal(err)
		}
		if string(content) != value {
			t.Fatalf("cmdparse.Quote(%q) round trip = %q", value, content)
		}
	}
}

func TestAgentFailureNetFallsBackToSanitizedResume(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	pfmScript := filepath.Join(root, "pfm")
	claudeScript := filepath.Join(root, "claude")
	resultPath := filepath.Join(root, "result")
	writeActionScript(t, pfmScript, `#!/bin/sh
if [ "$1" = internal ] && [ "$2" = agent-open ]; then exit 1; fi
exit 2
`, 0o700)
	writeActionScript(t, claudeScript, `#!/bin/sh
{
  printf 'argv=%s\n' "$*"
  printf 'ttl=%s\n' "${CACHE_LIVE_CONTROL_MAIN_TTL-unset}"
  printf 'sid=%s\n' "${CLAUDE_CODE_SESSION_ID-unset}"
  printf 'code=%s\n' "${CLAUDECODE-unset}"
  printf 'cfg=%s\n' "${CLAUDE_CONFIG_DIR-unset}"
  printf 'enable=%s\n' "${ENABLE_PROMPT_CACHING_1H-unset}"
  printf 'force=%s\n' "${FORCE_PROMPT_CACHING_5M-unset}"
  printf 'base=%s\n' "${ANTHROPIC_BASE_URL-unset}"
  printf 'token=%s\n' "${ANTHROPIC_AUTH_TOKEN-unset}"
  printf 'gateway=%s\n' "${CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY-unset}"
} > "$ACTION_RESULT"
`, 0o700)
	id := "22222222-2222-4222-8222-222222222222"
	plan, err := synthesizeWithTestConfig(Request{
		Row: compose.Row{
			Kind:      compose.Agent,
			ID:        id,
			CWD:       "/work/agent",
			ConfigDir: pfmconfig.DefaultAccountDir(home, 2),
		},
		PrimaryAccount: 2,
		Cache1H:        true,
		Home:           home,
		FreshSocket:    "cc-1700000001-123-456",
	})
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("sh", "-c", plan.Run)
	command.Env = append(
		os.Environ(),
		"PATH="+root+":"+os.Getenv("PATH"),
		"ACTION_RESULT="+resultPath,
		"CLAUDE_CODE_SESSION_ID=poison",
		"CLAUDECODE=poison",
		"CLAUDE_CONFIG_DIR=/poison",
		"ENABLE_PROMPT_CACHING_1H=poison",
		"FORCE_PROMPT_CACHING_5M=poison",
		"ANTHROPIC_BASE_URL=http://127.0.0.1:9/proxy",
		"ANTHROPIC_AUTH_TOKEN=poison",
		"CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY=1",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("run agent failure net: %v: %s", err, output)
	}
	content, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatal(err)
	}
	line := string(content)
	if !strings.HasPrefix(line, "argv=--remote-control --resume "+id+" --settings ") ||
		!strings.Contains(line, "\nsid=unset\n") || !strings.Contains(line, "\ncode=unset\n") ||
		!strings.Contains(line, "\ncfg="+pfmconfig.DefaultAccountDir(home, 2)+"\n") ||
		!strings.Contains(line, "\nenable=unset\n") || !strings.Contains(line, "\nforce=unset\n") ||
		!strings.Contains(line, "\nbase=unset\n") || !strings.Contains(line, "\ntoken=unset\n") ||
		!strings.Contains(line, "\ngateway=unset\n") {
		t.Fatalf("fallback result = %q", content)
	}
	if !strings.Contains(line, "\nttl=1h\n") {
		t.Fatalf("fallback cache: want ttl=1h in the process environment, got %q", content)
	}
}

// writeActionScript writes an executable the plan runs, through testjail so a script run at once cannot race a fork.
func writeActionScript(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := testjail.WriteExecutable(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}
