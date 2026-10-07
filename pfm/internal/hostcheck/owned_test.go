package hostcheck

import (
	"bytes"
	"encoding/json"
	"fmt"
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
	t.Run("status-line", func(t *testing.T) {
		assertRows(
			t,
			detect(t, "pfm-settings", env),
			Row{
				Block,
				"pfm-settings",
				path,
				"carries pfm's statusLine — pfm supplies its own hooks and status lines through --settings at launch, so these entries are leftovers of an earlier install",
				"remove statusLine from " + path + " (each hook entry running that command and each named key; keep every other entry)",
			},
		)
	})
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
				"carries pfm's statusLine — pfm supplies its own hooks and status lines through --settings at launch, so these entries are leftovers of an earlier install",
				"remove statusLine from " + other + " (each hook entry running that command and each named key; keep every other entry)",
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
	t.Run("operator-ledger-only", func(t *testing.T) {
		env := fixtureEnv(t)
		path := filepath.Join(env.Store, "settings.json")
		command := env.Home + "/private-ledger-hook"
		writeFile(t, path, fmt.Sprintf(`{"hooks":{"SessionStart":[{"hooks":[{"command":%q}]}]}}`, command))
		ledger := filepath.Join(env.ManagedRoot, "settings-hook-ownership.json")
		writeFile(
			t,
			ledger,
			fmt.Sprintf(
				`{"version":1,"hooks":[{"path":%q,"event":"SessionStart","matcher":"","command":%q,"count":1}]}`,
				path,
				command,
			),
		)
		assertRows(t, detect(t, "pfm-settings", env))
	})
	t.Run("hook-edit", func(t *testing.T) {
		env := fixtureEnv(t)
		path := filepath.Join(env.Store, "settings.json")
		command := env.Home + "/.local/bin/pfm internal launcher-repair"
		writeFile(
			t,
			path,
			fmt.Sprintf(
				`{"hooks":{"SessionStart":[{"hooks":[{"command":%q},{"command":"pfm internal clear-hide"},{"command":"operator-hook"}]}]},"statusLine":{"command":"pfm statusline"},"subagentStatusLine":{"command":%q}}`,
				command,
				claudelaunch.SubagentStatusLineCommand(env.Home),
			),
		)
		items := fmt.Sprintf(`hook %q, hook "pfm internal clear-hide", statusLine, subagentStatusLine`, command)
		assertRows(t, detect(t, "pfm-settings", env), Row{
			Block,
			"pfm-settings",
			path,
			"carries pfm's " + items + " — pfm supplies its own hooks and status lines through --settings at launch, so these entries are leftovers of an earlier install",
			"remove " + items + " from " + path + " (each hook entry running that command and each named key; keep every other entry)",
		})
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
				"clients": []string{"client"},
				"registrations": map[string]any{
					path: map[string]any{"registered": map[string]any{"command": "invented"}},
				},
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
				"carries pfm mcpServers.registered",
				"remove mcpServers.registered from " + path + "; pfm's server rides --mcp-config at launch",
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
	t.Run("operator-professor", func(t *testing.T) {
		env := fixtureEnv(t)
		path := filepath.Join(env.Accounts[0].ConfigDir, ".claude.json")
		writeFile(t, path, `{"mcpServers":{"professor":{"command":"/opt/operator/mcp"}}}`)
		writeFile(
			t,
			filepath.Join(env.ManagedRoot, "mcp-ownership.json"),
			fmt.Sprintf(`{"registrations":{%q:{"professor":{"command":"old"}}}}`, path),
		)
		assertRows(t, detect(t, "pfm-mcp", env))
		assertRows(t, detect(t, "third-party-mcp", env), Row{
			Warn,
			"third-party-mcp",
			path,
			"mcpServers.professor is declared outside pfm",
			"move it to mcp.thirdParty.professor in " + env.ConfigPath + ", then remove mcpServers.professor from " + path,
		})
	})
	t.Run("clients-only", func(t *testing.T) {
		env := fixtureEnv(t)
		writeFile(t, filepath.Join(env.Home, ".mcp.json"), `{"mcpServers":{"client":{"command":"invented"}}}`)
		ledger := filepath.Join(env.ManagedRoot, "mcp-ownership.json")
		writeFile(t, ledger, `{"clients":["client"]}`)
		assertRows(t, detect(t, "pfm-mcp", env), Row{
			Block, "pfm-mcp", ledger,
			"mcp-ownership.json still records pfm clients",
			"remove \"clients\" from " + ledger,
		})
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
		map[string]any{
			"registrations": map[string]any{path: map[string]any{"foreign": map[string]any{"command": "invented"}}},
		},
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

func TestThirdPartyMCPAcceptedDeclarationIsExactAndReadable(t *testing.T) {
	for _, test := range []struct {
		name, path, server, body string
		accepted                 bool
		warnings                 int
	}{
		{"exact", "", "agent-browser", `{"mcpServers":{"agent-browser":{}}}`, true, 0},
		{"other server", "", "other", `{"mcpServers":{"agent-browser":{}}}`, false, 1},
		{"other path", "/other/.claude.json", "agent-browser", `{"mcpServers":{"agent-browser":{}}}`, false, 1},
		{"broken json", "", "agent-browser", `{`, false, 0},
		{"mixed registry", "", "agent-browser", `{"mcpServers":{"agent-browser":{},"other":{"command":"invented"}}}`, true, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			env := fixtureEnv(t)
			path := filepath.Join(env.Accounts[0].ConfigDir, ".claude.json")
			writeFile(t, path, test.body)
			acceptedPath := test.path
			if acceptedPath == "" {
				acceptedPath = path
			}
			env.AcceptedMCP = []config.AcceptedMCP{
				{Path: acceptedPath, Server: test.server, Reason: "active user integration"},
			}
			rows := detect(t, "third-party-mcp", env)
			if test.accepted {
				if len(rows) != 1+test.warnings || rows[0].Severity != Accepted ||
					!strings.Contains(rows[0].Problem, "active user integration") ||
					Count(rows, Warn) != test.warnings {
					t.Fatalf("accepted rows=%+v", rows)
				}
				if plan := NewPlan(1, rows); len(plan.Steps) != test.warnings || len(plan.Failed) != 0 {
					t.Fatalf("accepted declaration offered as a fix: %+v", plan)
				}
				if strings.Contains(rows[0].Render("host-check: "), "fix:") {
					t.Fatalf("accepted declaration prints a remedy: %+v", rows[0])
				}
			} else if len(rows) != 1 || Count(rows, Warn) != test.warnings ||
				(test.warnings == 0 && rows[0].Severity != Block) {
				t.Fatalf("unaccepted or unreadable rows=%+v", rows)
			}
		})
	}
}
