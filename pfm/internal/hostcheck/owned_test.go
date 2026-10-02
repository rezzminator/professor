package hostcheck

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/config"
)

func settingsFixture(t *testing.T, env Env, path string) {
	t.Helper()
	raw, err := json.Marshal(
		map[string]any{
			"statusLine": map[string]any{"type": "command", "command": claudelaunch.StatusLineCommand(env.Home)},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, string(raw))
}

func TestPFMSettings(t *testing.T) {
	env := fixtureEnv(t)
	path := filepath.Join(env.Store, "settings.json")
	settingsFixture(t, env, path)
	for _, id := range []int{2, 1} {
		dir := config.DefaultAccountDir(env.Home, id)
		makeDir(t, dir)
		if err := os.Symlink(path, filepath.Join(dir, "settings.json")); err != nil {
			t.Fatal(err)
		}
	}
	env.Accounts = append(env.Accounts, config.Account{ID: 2, ConfigDir: config.DefaultAccountDir(env.Home, 2)})
	assertRows(
		t,
		detect(t, "pfm-settings", env),
		Row{
			Block,
			"pfm-settings",
			path,
			"carries pfm statusLine — they ride --settings at launch and would run twice",
			"remove statusLine from " + path + " (pfm's hook commands only; keep every other key)",
		},
	)
	t.Run("unreadable-continues", func(t *testing.T) {
		env := fixtureEnv(t)
		path := filepath.Join(env.Store, "settings.json")
		makeDir(t, path)
		other := filepath.Join(env.Accounts[0].ConfigDir, "settings.json")
		settingsFixture(t, env, other)
		rows := detect(t, "pfm-settings", env)
		assertUnreadable(t, rows[:1], "pfm-settings", path, syscall.EISDIR)
		assertRows(
			t,
			rows[1:],
			Row{
				Block,
				"pfm-settings",
				other,
				"carries pfm statusLine — they ride --settings at launch and would run twice",
				"remove statusLine from " + other + " (pfm's hook commands only; keep every other key)",
			},
		)
	})
	t.Run("malformed-file", func(t *testing.T) {
		env := fixtureEnv(t)
		path := filepath.Join(env.Store, "settings.json")
		writeFile(t, path, "{")
		rows := detect(t, "pfm-settings", env)
		if len(rows) != 1 || rows[0].Path != path || !strings.HasPrefix(rows[0].Problem, "UNREADABLE "+path+":") {
			t.Fatalf("rows=%v", rows)
		}
	})
}

func mcpFixture(t *testing.T, env Env, path string) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"mcpServers": map[string]any{
		"professor": map[string]any{
			"type":    "stdio",
			"command": filepath.Join(env.Home, ".local", "bin", "pfm"),
			"args":    []string{"mcp", "serve", "--stdio"},
		},
		"foreign": map[string]any{"command": "invented"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, string(raw))
}

func TestPFMMCP(t *testing.T) {
	env := fixtureEnv(t)
	account := filepath.Join(env.Accounts[0].ConfigDir, ".claude.json")
	ambient := filepath.Join(env.Home, ".claude.json")
	homeMCP := filepath.Join(env.Home, ".mcp.json")
	var want []Row
	for _, path := range []string{account, ambient, homeMCP} {
		mcpFixture(t, env, path)
		want = append(
			want,
			Row{
				Block,
				"pfm-mcp",
				path,
				"carries pfm mcpServers.professor",
				"remove mcpServers.professor from " + path + "; pfm's server rides --mcp-config at launch",
			},
		)
	}
	ledger := filepath.Join(env.ManagedRoot, "mcp-ownership.json")
	writeFile(t, ledger, `{"clients":["professor"]}`)
	want = append(
		want,
		Row{
			Block,
			"pfm-mcp",
			ledger,
			"mcp-ownership.json still records pfm clients",
			"remove \"clients\" from " + ledger,
		},
	)
	assertRows(t, detect(t, "pfm-mcp", env), want...)
	t.Run("unreadable-continues", func(t *testing.T) {
		env := fixtureEnv(t)
		path := filepath.Join(env.Accounts[0].ConfigDir, ".claude.json")
		makeDir(t, path)
		other := filepath.Join(env.Home, ".mcp.json")
		mcpFixture(t, env, other)
		rows := detect(t, "pfm-mcp", env)
		assertUnreadable(t, rows[:1], "pfm-mcp", path, syscall.EISDIR)
		assertRows(
			t,
			rows[1:],
			Row{
				Block,
				"pfm-mcp",
				other,
				"carries pfm mcpServers.professor",
				"remove mcpServers.professor from " + other + "; pfm's server rides --mcp-config at launch",
			},
		)
	})
	t.Run("physical-registry-once", func(t *testing.T) {
		env := fixtureEnv(t)
		homePath := filepath.Join(env.Home, ".mcp.json")
		writeFile(t, homePath, `{"mcpServers":{"client":{"command":"invented"},"registered":{"command":"invented"}}}`)
		path := filepath.Join(env.Accounts[0].ConfigDir, ".claude.json")
		makeDir(t, filepath.Dir(path))
		if err := os.Symlink(homePath, path); err != nil {
			t.Fatal(err)
		}
		ledger, err := json.Marshal(
			map[string]any{
				"clients":       []string{"client"},
				"registrations": map[string]any{path: map[string]any{"registered": map[string]any{}}},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		ledgerPath := filepath.Join(env.ManagedRoot, "mcp-ownership.json")
		writeFile(t, ledgerPath, string(ledger))
		assertRows(
			t,
			detect(t, "pfm-mcp", env),
			Row{
				Block,
				"pfm-mcp",
				path,
				"carries pfm mcpServers.client,registered",
				"remove mcpServers.client,registered from " + path + "; pfm's server rides --mcp-config at launch",
			},
			Row{
				Block,
				"pfm-mcp",
				ledgerPath,
				"mcp-ownership.json still records pfm clients",
				"remove \"clients\" from " + ledgerPath,
			},
		)
	})
}

func TestOwnershipLedgerUnreadable(t *testing.T) {
	for _, test := range []struct{ check, name string }{{"pfm-settings", "settings-hook-ownership.json"}, {"pfm-mcp", "mcp-ownership.json"}} {
		t.Run(test.check, func(t *testing.T) {
			env := fixtureEnv(t)
			path := filepath.Join(env.ManagedRoot, test.name)
			writeFile(t, path, "{")
			rows := detect(t, test.check, env)
			if len(rows) != 1 || rows[0].Severity != Block || rows[0].Path != path ||
				!strings.HasPrefix(rows[0].Problem, "UNREADABLE "+path+":") ||
				rows[0].Fix != "make "+path+" readable to you, then rerun" {
				t.Fatalf("rows=%+v", rows)
			}
		})
	}
}

func historicalHelper(t *testing.T, newName string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..", "..")
	content, err := os.ReadFile(filepath.Join(root, "templates", "project", "scripts", newName))
	if err != nil {
		t.Fatal(err)
	}
	content = bytes.Replace(content, []byte("# "+newName+" —"), []byte("# cc-"+newName+" —"), 1)
	content = bytes.Replace(
		content,
		[]byte("SessionStart hook (memory-wire.sh)"),
		[]byte("SessionStart hook (cc-memory-wire.sh)"),
		1,
	)
	return string(bytes.Replace(content, []byte("{MEMORY_VAULT_DIR}"), []byte("invented-vault"), 1))
}

func TestMemoryHelpers(t *testing.T) {
	for _, name := range []string{"memory-wire.sh", "memory-consolidate.sh"} {
		t.Run(name, func(t *testing.T) {
			env := fixtureEnv(t)
			path := filepath.Join(env.Accounts[0].ConfigDir, "scripts", "cc-"+name)
			content := historicalHelper(t, name)
			writeFile(t, path, content)
			target := filepath.Join(env.Accounts[0].ConfigDir, "scripts", name)
			assertRows(
				t,
				detect(t, "memory-helpers", env),
				Row{
					Block,
					"memory-helpers",
					path,
					"pfm's memory helper under its retired name",
					"mv " + path + " " + target + ", then replace " + path + " with " + target + " in every hook command of " + filepath.Join(
						env.Accounts[0].ConfigDir,
						"settings.json",
					),
				},
			)
		})
		t.Run(name+"-customized", func(t *testing.T) {
			env := fixtureEnv(t)
			path := filepath.Join(env.Accounts[0].ConfigDir, "scripts", "cc-"+name)
			writeFile(t, path, historicalHelper(t, name)+"# edited\n")
			assertRows(t, detect(t, "memory-helpers", env))
		})
	}
	t.Run("unreadable-continues", func(t *testing.T) {
		env := fixtureEnv(t)
		writeFile(t, filepath.Join(env.Store, "scripts"), "file")
		path := filepath.Join(env.Accounts[0].ConfigDir, "scripts", "cc-memory-wire.sh")
		writeFile(t, path, historicalHelper(t, "memory-wire.sh"))
		rows := detect(t, "memory-helpers", env)
		if len(rows) != 3 {
			t.Fatalf("rows=%v", rows)
		}
		for i, name := range []string{"cc-memory-wire.sh", "cc-memory-consolidate.sh"} {
			assertUnreadable(
				t,
				rows[i:i+1],
				"memory-helpers",
				filepath.Join(env.Store, "scripts", name),
				syscall.ENOTDIR,
			)
		}
		if rows[2].Path != path || rows[2].Severity != Block {
			t.Fatalf("later=%v", rows[2])
		}
	})
}

func TestThirdPartyMCP(t *testing.T) {
	env := fixtureEnv(t)
	path := filepath.Join(env.Accounts[0].ConfigDir, ".claude.json")
	mcpFixture(t, env, path)
	assertRows(
		t,
		detect(t, "third-party-mcp", env),
		Row{
			Warn,
			"third-party-mcp",
			path,
			"mcpServers.foreign is declared outside pfm",
			"move it to mcp.thirdParty.foreign in " + env.ConfigPath + ", then remove mcpServers.foreign from " + path,
		},
	)
	// The ledger, as well as shape matching, excludes pfm's entries.
	ledger, err := json.Marshal(
		map[string]any{"registrations": map[string]any{path: map[string]any{"foreign": map[string]any{}}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(env.ManagedRoot, "mcp-ownership.json"), string(ledger))
	assertRows(t, detect(t, "third-party-mcp", env))
	t.Run("unreadable-continues", func(t *testing.T) {
		env := fixtureEnv(t)
		path := filepath.Join(env.Accounts[0].ConfigDir, ".claude.json")
		makeDir(t, path)
		other := filepath.Join(env.Home, ".claude.json")
		mcpFixture(t, env, other)
		rows := detect(t, "third-party-mcp", env)
		assertUnreadable(t, rows[:1], "third-party-mcp", path, syscall.EISDIR)
		assertRows(
			t,
			rows[1:],
			Row{
				Warn,
				"third-party-mcp",
				other,
				"mcpServers.foreign is declared outside pfm",
				"move it to mcp.thirdParty.foreign in " + env.ConfigPath + ", then remove mcpServers.foreign from " + other,
			},
		)
	})
	t.Run("malformed", func(t *testing.T) {
		env := fixtureEnv(t)
		path := filepath.Join(env.Accounts[0].ConfigDir, ".claude.json")
		writeFile(t, path, "{")
		assertUnreadable(t, detect(t, "third-party-mcp", env), "third-party-mcp", path, jsonDecodeError("{"))
	})
}
