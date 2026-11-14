package hostcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const betasVar = "CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS"

func accountSettings(env Env) string {
	return filepath.Join(env.Accounts[0].ConfigDir, "settings.json")
}

func TestShellClaudeEnvWarnsOnShellOnlyVariable(t *testing.T) {
	env := fixtureEnv(t)
	env.Environ = []string{"HOME=" + env.Home, betasVar + "=1"}
	writeFile(t, accountSettings(env), `{"env":{"CLAUDE_CODE_MAX_WEB_SEARCHES_PER_SESSION":"9"}}`)
	row := onlyRow(t, detect(t, "shell-claude-env", env))
	if row.Severity != Warn || row.Check != "shell-claude-env" || row.Path != accountSettings(env) {
		t.Fatalf("row=%+v, want WARN shell-claude-env at %s", row, accountSettings(env))
	}
	assertContains(t, "problem", row.Problem, betasVar, "login shell", "pfm mcp serve")
	assertContains(t, "fix", row.Fix, `"env"`, betasVar, accountSettings(env))
}

func TestShellClaudeEnvAnthropicVariableWithNoSettingsFileWarns(t *testing.T) {
	env := fixtureEnv(t)
	env.Environ = []string{"ANTHROPIC_BASE_URL=https://gateway.example"}
	row := onlyRow(t, detect(t, "shell-claude-env", env))
	if got, want := strings.ReplaceAll(
		row.Problem+" | "+row.Fix,
		env.Home,
		"{home}",
	), "ANTHROPIC_BASE_URL is exported in the login shell but absent from this settings env: chats pfm mcp serve spawns under launchd or systemd inherit no login-shell export, so they run without it while terminal chats have it | add \"ANTHROPIC_BASE_URL\" to the \"env\" block of {home}/.cc/1/settings.json with the value your shell exports, then start a new chat"; got != want {
		t.Fatalf("problem | fix = %q, want %q", got, want)
	}
}

func TestShellClaudeEnvVariableInSettingsEnvIsClean(t *testing.T) {
	env := fixtureEnv(t)
	env.Environ = []string{betasVar + "=1"}
	writeFile(t, accountSettings(env), `{"env":{"`+betasVar+`":"1"}}`)
	assertRows(t, detect(t, "shell-claude-env", env))
}

func TestShellClaudeEnvIgnoresLaunchSetEmptyAndForeignVariables(t *testing.T) {
	env := fixtureEnv(t)
	env.Environ = []string{
		"CLAUDE_CONFIG_DIR=" + env.Accounts[0].ConfigDir,
		"CLAUDE_CODE_AUTO_COMPACT_WINDOW=50000",
		"CLAUDE_CODE_ENABLE_FUNCTION_HOOKS=1",
		betasVar + "=",
		"PATH=/usr/bin",
		"NOT_CLAUDE_X=1",
	}
	assertRows(t, detect(t, "shell-claude-env", env))
}

func TestShellClaudeEnvInsideAChatReportsNotChecked(t *testing.T) {
	env := fixtureEnv(t)
	env.Environ = []string{"CLAUDECODE=1", betasVar + "=1"}
	row := onlyRow(t, detect(t, "shell-claude-env", env))
	if row.Severity != Warn {
		t.Fatalf("row=%+v, want WARN", row)
	}
	assertContains(t, "problem", row.Problem, "not checked", "CLAUDECODE")
	assertContains(t, "fix", row.Fix, "terminal")
}

func TestShellClaudeEnvSharedSettingsGetOneRow(t *testing.T) {
	env := fixtureEnv(t)
	second := filepath.Join(env.Home, ".cc", "2")
	makeDir(t, second)
	writeFile(t, accountSettings(env), `{}`)
	if err := os.Symlink(accountSettings(env), filepath.Join(second, "settings.json")); err != nil {
		t.Fatal(err)
	}
	env.Accounts = append(env.Accounts, env.Accounts[0])
	env.Accounts[1].ID, env.Accounts[1].ConfigDir = 2, second
	env.Environ = []string{betasVar + "=1"}
	onlyRow(t, detect(t, "shell-claude-env", env))
}

func TestShellClaudeEnvUnreadableOrUnparsableSettingsIsReportedNotClean(t *testing.T) {
	env := fixtureEnv(t)
	env.Environ = []string{betasVar + "=1"}
	makeDir(t, accountSettings(env))
	row := onlyRow(t, detect(t, "shell-claude-env", env))
	if row.Severity != Block || row.Path != accountSettings(env) {
		t.Fatalf("row=%+v, want BLOCK at %s", row, accountSettings(env))
	}
	other := fixtureEnv(t)
	other.Environ = env.Environ
	writeFile(t, accountSettings(other), `{"env":`)
	row = onlyRow(t, detect(t, "shell-claude-env", other))
	if row.Severity != Warn || row.Path != accountSettings(other) {
		t.Fatalf("row=%+v, want WARN at %s", row, accountSettings(other))
	}
	assertContains(t, "problem", row.Problem, "cannot parse")
}

func TestShellClaudeEnvKeepsConfigurationWarningsWithInheritedRuntime(t *testing.T) {
	env := fixtureEnv(t)
	env.Environ = []string{
		betasVar + "=1",
		"CLAUDE_CODE_BRIDGE_SESSION_ID=session",
		"CLAUDE_CODE_ENTRYPOINT=cli",
		"CLAUDE_CODE_EXECPATH=/opt/claude",
		"CLAUDE_CODE_MESSAGING_SOCKET=/tmp/session.sock",
		"CLAUDE_CODE_MESSAGING_TOKEN=secret",
		"CLAUDE_CODE_SESSION_ATTENDED=1",
		"CLAUDE_PID=123",
		"CLAUDE_EFFORT=high",
	}
	rows := detect(t, "shell-claude-env", env)
	if len(rows) != 1 ||
		rows[0].Problem != "CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS is exported in the login shell but absent from this settings env: chats pfm mcp serve spawns under launchd or systemd inherit no login-shell export, so they run without it while terminal chats have it" {
		t.Fatalf("rows=%+v, want only the configurable beta warning", rows)
	}
}
