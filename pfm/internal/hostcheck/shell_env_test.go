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
	assertContains(t, "problem", row.Problem, "ANTHROPIC_BASE_URL")
	assertContains(t, "fix", row.Fix+row.Problem, "ANTHROPIC_BASE_URL")
	if want := "https://gateway.example"; strings.Contains(row.Problem+row.Fix, want) {
		t.Fatalf("row=%+v prints the shell's value %q", row, want)
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
