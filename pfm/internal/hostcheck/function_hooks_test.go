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
	installModulePlugin(t, env.Accounts[0].ConfigDir, "buddy@professor", true)
	row := onlyRow(t, detect(t, "function-hook-modules", env))
	if row.Severity != Warn || row.Check != "function-hook-modules" || row.Path != path {
		t.Fatalf("row=%+v, want WARN function-hook-modules at %s", row, path)
	}
	assertContains(t, "problem", row.Problem,
		"cachedGrowthBookFeatures."+hookModulesFlag+" is false", "buddy@professor",
		"gateway", "CLAUDE_CODE_ENABLE_FUNCTION_HOOKS=1 does not override")
	assertContains(t, "fix", row.Fix, "no Claude chat running", hookModulesFlag+" to true in "+path, "new chat")
}

func TestFunctionHookModulesCachedTrueIsClean(t *testing.T) {
	env := fixtureEnv(t)
	writeFile(t, accountStateFile(env), `{"cachedGrowthBookFeatures":{"`+hookModulesFlag+`":true}}`)
	installModulePlugin(t, env.Accounts[0].ConfigDir, "buddy@professor", true)
	assertRows(t, detect(t, "function-hook-modules", env))
}

func TestFunctionHookModulesUnsetFlagWarnsOnlyWithModulePlugins(t *testing.T) {
	env := fixtureEnv(t)
	dir := env.Accounts[0].ConfigDir
	writeFile(t, accountStateFile(env), `{"cachedGrowthBookFeatures":{"other":true}}`)
	installModulePlugin(t, dir, "plain@plain", false)
	assertRows(t, detect(t, "function-hook-modules", env))
	installModulePlugin(t, dir, "auto-effort-control@professor", true)
	row := onlyRow(t, detect(t, "function-hook-modules", env))
	if row.Severity != Warn {
		t.Fatalf("row=%+v, want WARN", row)
	}
	assertContains(t, "problem", row.Problem, hookModulesFlag+" is unset", "auto-effort-control@professor")
}

func TestFunctionHookModulesMissingStateFileWarnsWithModulePlugins(t *testing.T) {
	env := fixtureEnv(t)
	installModulePlugin(t, env.Accounts[0].ConfigDir, "buddy@professor", true)
	row := onlyRow(t, detect(t, "function-hook-modules", env))
	if row.Severity != Warn || row.Path != accountStateFile(env) {
		t.Fatalf("row=%+v, want WARN at %s", row, accountStateFile(env))
	}
	assertContains(t, "problem", row.Problem, "does not exist", hookModulesFlag+" is unset", "buddy@professor")
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

// installDirectoryPlugin records plugin@market as installed from a snapshot
// without hook modules while market is a directory marketplace whose folder
// ./plugins/plugin declares them (or not): Claude Code runs the folder.
func installDirectoryPlugin(t *testing.T, dir, plugin, market string, folderModules, listed bool) (root string) {
	t.Helper()
	root = filepath.Join(t.TempDir(), market)
	snapshot := filepath.Join(dir, "plugins", "cache", market, plugin)
	writeFile(t, filepath.Join(snapshot, "hooks", "hooks.json"), `{"hooks":{}}`)
	if folderModules {
		writeFile(t, filepath.Join(root, "plugins", plugin, "hooks", "hooks.json"), `{"modules":["./plugin.ts"]}`)
	}
	if listed {
		writeFile(t, filepath.Join(root, ".claude-plugin", "marketplace.json"),
			`{"name":"`+market+`","plugins":[{"name":"`+plugin+`","source":"./plugins/`+plugin+`"}]}`)
	}
	id := plugin + "@" + market
	writeFile(t, filepath.Join(dir, "plugins", "installed_plugins.json"),
		`{"version":2,"plugins":{"`+id+`":[{"scope":"user","installPath":"`+snapshot+`"}]}}`)
	writeFile(t, filepath.Join(dir, "plugins", "known_marketplaces.json"),
		`{"`+market+`":{"source":{"source":"directory","path":"`+root+`"}}}`)
	return root
}

func TestFunctionHookModulesDirectoryMarketplaceReadsItsFolderNotTheSnapshot(t *testing.T) {
	env := fixtureEnv(t)
	writeFile(t, accountStateFile(env), `{}`)
	installDirectoryPlugin(t, env.Accounts[0].ConfigDir, "buddy", "professor", true, true)
	row := onlyRow(t, detect(t, "function-hook-modules", env))
	assertContains(t, "problem", row.Problem, hookModulesFlag+" is unset", "buddy@professor")
}

func TestFunctionHookModulesDirectoryMarketplaceFolderWithoutModulesIsClean(t *testing.T) {
	env := fixtureEnv(t)
	writeFile(t, accountStateFile(env), `{}`)
	dir := env.Accounts[0].ConfigDir
	installDirectoryPlugin(t, dir, "buddy", "professor", false, true)
	snapshot := filepath.Join(dir, "plugins", "cache", "professor", "buddy", "hooks", "hooks.json")
	writeFile(t, snapshot, `{"modules":["./old.ts"]}`)
	assertRows(t, detect(t, "function-hook-modules", env))
}

func TestFunctionHookModulesGithubMarketplaceStillReadsInstallPath(t *testing.T) {
	env := fixtureEnv(t)
	dir := env.Accounts[0].ConfigDir
	writeFile(t, accountStateFile(env), `{}`)
	installModulePlugin(t, dir, "buddy@professor", true)
	writeFile(t, filepath.Join(dir, "plugins", "known_marketplaces.json"),
		`{"professor":{"source":{"source":"github","repo":"owner/professor"}}}`)
	row := onlyRow(t, detect(t, "function-hook-modules", env))
	assertContains(t, "problem", row.Problem, hookModulesFlag+" is unset", "buddy@professor")
}

func TestFunctionHookModulesUnresolvableDirectoryFolderIsReportedNotClean(t *testing.T) {
	env := fixtureEnv(t)
	writeFile(t, accountStateFile(env), `{}`)
	root := installDirectoryPlugin(t, env.Accounts[0].ConfigDir, "buddy", "professor", true, false)
	manifest := filepath.Join(root, ".claude-plugin", "marketplace.json")
	row := onlyRow(t, detect(t, "function-hook-modules", env))
	if row.Severity != Warn || row.Path != manifest {
		t.Fatalf("row=%+v, want WARN at %s", row, manifest)
	}
	assertContains(t, "problem", row.Problem, "buddy@professor", "unknown")
}

func TestFunctionHookModulesUnlistedDirectoryPluginIsReportedNotClean(t *testing.T) {
	env := fixtureEnv(t)
	writeFile(t, accountStateFile(env), `{}`)
	root := installDirectoryPlugin(t, env.Accounts[0].ConfigDir, "buddy", "professor", true, false)
	writeFile(
		t,
		filepath.Join(root, ".claude-plugin", "marketplace.json"),
		`{"plugins":[{"name":"other","source":"./plugins/other"}]}`,
	)
	row := onlyRow(t, detect(t, "function-hook-modules", env))
	if row.Severity != Warn {
		t.Fatalf("row=%+v, want WARN", row)
	}
	assertContains(t, "problem", row.Problem, "lists no folder source for plugin buddy", "unknown")
}

func TestFunctionHookModulesUnparsableKnownMarketplacesIsReportedNotClean(t *testing.T) {
	env := fixtureEnv(t)
	dir := env.Accounts[0].ConfigDir
	writeFile(t, accountStateFile(env), `{}`)
	installModulePlugin(t, dir, "buddy@professor", true)
	known := filepath.Join(dir, "plugins", "known_marketplaces.json")
	writeFile(t, known, `{nope`)
	row := onlyRow(t, detect(t, "function-hook-modules", env))
	if row.Severity != Warn || row.Path != known {
		t.Fatalf("row=%+v, want WARN at %s", row, known)
	}
	assertContains(t, "problem", row.Problem, "cannot parse", "unknown")
}
