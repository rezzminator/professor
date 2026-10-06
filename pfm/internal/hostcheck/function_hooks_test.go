package hostcheck

import (
	"path/filepath"
	"strings"
	"testing"
)

const hookModulesFlag = "tengu_plugin_hooks_modules"

// installModulePlugin records plugin id as installed in dir's shared plugin
// record, its hooks.json declaring modules when modules is true.
func installModulePlugin(t *testing.T, dir, id string, modules bool) {
	t.Helper()
	installPath := filepath.Join(dir, "plugins", "cache", id)
	hooks := `{"hooks":{}}`
	if modules {
		hooks = `{"modules":["./plugin.ts"]}`
	}
	writeFile(t, filepath.Join(installPath, "hooks", "hooks.json"), hooks)
	writeFile(t, filepath.Join(dir, "plugins", "installed_plugins.json"),
		`{"version":2,"plugins":{"`+id+`":[{"scope":"user","installPath":"`+installPath+`"}]}}`)
}

func accountStateFile(env Env) string {
	return filepath.Join(env.Accounts[0].ConfigDir, ".claude.json")
}

func onlyRow(t *testing.T, rows []Row) Row {
	t.Helper()
	if len(rows) != 1 {
		t.Fatalf("rows=%+v, want exactly one", rows)
	}
	return rows[0]
}

func assertContains(t *testing.T, field, got string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Fatalf("%s=%q, want it to contain %q", field, got, want)
		}
	}
}

func TestFunctionHookModulesCachedFalseWarnsWithCauseAndFix(t *testing.T) {
	env := fixtureEnv(t)
	path := accountStateFile(env)
	writeFile(t, path, `{"cachedGrowthBookFeatures":{"`+hookModulesFlag+`":false}}`)
	installModulePlugin(t, env.Accounts[0].ConfigDir, "buddy@buddy", true)
	row := onlyRow(t, detect(t, "function-hook-modules", env))
	if row.Severity != Warn || row.Check != "function-hook-modules" || row.Path != path {
		t.Fatalf("row=%+v, want WARN function-hook-modules at %s", row, path)
	}
	assertContains(t, "problem", row.Problem,
		"cachedGrowthBookFeatures."+hookModulesFlag+" is false", "buddy@buddy",
		"gateway", "CLAUDE_CODE_ENABLE_FUNCTION_HOOKS=1 does not override")
	assertContains(t, "fix", row.Fix, "no Claude chat running", hookModulesFlag+" to true in "+path, "new chat")
}

func TestFunctionHookModulesCachedTrueIsClean(t *testing.T) {
	env := fixtureEnv(t)
	writeFile(t, accountStateFile(env), `{"cachedGrowthBookFeatures":{"`+hookModulesFlag+`":true}}`)
	installModulePlugin(t, env.Accounts[0].ConfigDir, "buddy@buddy", true)
	assertRows(t, detect(t, "function-hook-modules", env))
}

func TestFunctionHookModulesUnsetFlagWarnsOnlyWithModulePlugins(t *testing.T) {
	env := fixtureEnv(t)
	dir := env.Accounts[0].ConfigDir
	writeFile(t, accountStateFile(env), `{"cachedGrowthBookFeatures":{"other":true}}`)
	installModulePlugin(t, dir, "plain@plain", false)
	assertRows(t, detect(t, "function-hook-modules", env))
	installModulePlugin(t, dir, "agent-effort@agent-effort", true)
	row := onlyRow(t, detect(t, "function-hook-modules", env))
	if row.Severity != Warn {
		t.Fatalf("row=%+v, want WARN", row)
	}
	assertContains(t, "problem", row.Problem, hookModulesFlag+" is unset", "agent-effort@agent-effort")
}

func TestFunctionHookModulesMissingStateFileWarnsWithModulePlugins(t *testing.T) {
	env := fixtureEnv(t)
	installModulePlugin(t, env.Accounts[0].ConfigDir, "buddy@buddy", true)
	row := onlyRow(t, detect(t, "function-hook-modules", env))
	if row.Severity != Warn || row.Path != accountStateFile(env) {
		t.Fatalf("row=%+v, want WARN at %s", row, accountStateFile(env))
	}
	assertContains(t, "problem", row.Problem, "does not exist", hookModulesFlag+" is unset", "buddy@buddy")
}

func TestFunctionHookModulesUnreadableStateFileIsReportedNotClean(t *testing.T) {
	env := fixtureEnv(t)
	makeDir(t, accountStateFile(env)) // a directory: the read fails, also for root
	row := onlyRow(t, detect(t, "function-hook-modules", env))
	if row.Severity != Block || row.Path != accountStateFile(env) {
		t.Fatalf("row=%+v, want BLOCK at %s", row, accountStateFile(env))
	}
	assertContains(t, "problem", row.Problem, "UNREADABLE")
}

func TestFunctionHookModulesUnparsableStateFileIsReportedNotClean(t *testing.T) {
	env := fixtureEnv(t)
	writeFile(t, accountStateFile(env), `{"cachedGrowthBookFeatures":`)
	row := onlyRow(t, detect(t, "function-hook-modules", env))
	if row.Severity != Warn || row.Path != accountStateFile(env) {
		t.Fatalf("row=%+v, want WARN at %s", row, accountStateFile(env))
	}
	assertContains(t, "problem", row.Problem, "cannot parse", "unknown")
}

func TestFunctionHookModulesUnreadablePluginRecordIsReportedNotClean(t *testing.T) {
	env := fixtureEnv(t)
	writeFile(t, accountStateFile(env), `{}`)
	record := filepath.Join(env.Accounts[0].ConfigDir, "plugins", "installed_plugins.json")
	makeDir(t, record)
	row := onlyRow(t, detect(t, "function-hook-modules", env))
	if row.Severity != Block || row.Path != record {
		t.Fatalf("row=%+v, want BLOCK at %s", row, record)
	}
}
