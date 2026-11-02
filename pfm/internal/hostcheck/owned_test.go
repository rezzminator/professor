package hostcheck

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
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
		ledger := filepath.Join(installer.ManagedRoot(env.Home), "settings-hook-ownership.json")
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

func TestOwnershipLedgerUnreadable(t *testing.T) {
	for _, test := range []struct{ check, name string }{{"pfm-settings", "settings-hook-ownership.json"}} {
		t.Run(test.check, func(t *testing.T) {
			env := fixtureEnv(t)
			path := filepath.Join(installer.ManagedRoot(env.Home), test.name)
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

func TestThirdPartyMCP(t *testing.T) {
	env := fixtureEnv(t)
	path := filepath.Join(env.Accounts[0].ConfigDir, ".claude.json")
	writeFile(t, path, `{"mcpServers":{"foreign":{"command":"invented"}}}`)
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
	t.Run("unreadable-continues", func(t *testing.T) {
		env := fixtureEnv(t)
		path := filepath.Join(env.Accounts[0].ConfigDir, ".claude.json")
		makeDir(t, path)
		other := filepath.Join(env.Home, ".claude.json")
		writeFile(t, other, `{"mcpServers":{"foreign":{"command":"invented"}}}`)
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
	const reason = "active user integration"
	for _, test := range []struct {
		name, path, server, body, reason, problem string
		accepted                                  bool
		warnings                                  int
	}{
		{
			"exact", "", "agent-browser", `{"mcpServers":{"agent-browser":{}}}`, reason,
			"mcpServers.agent-browser retained by doctor.acceptedMCP: " + reason, true, 0,
		},
		{
			"exact without a reason", "", "agent-browser", `{"mcpServers":{"agent-browser":{}}}`, "",
			"mcpServers.agent-browser retained by doctor.acceptedMCP", true, 0,
		},
		{"other server", "", "other", `{"mcpServers":{"agent-browser":{}}}`, reason, "", false, 1},
		{"other path", "/other/.claude.json", "agent-browser", `{"mcpServers":{"agent-browser":{}}}`, reason, "", false, 1},
		{"broken json", "", "agent-browser", `{`, reason, "", false, 0},
		{
			"mixed registry", "", "agent-browser", `{"mcpServers":{"agent-browser":{},"other":{"command":"invented"}}}`, reason,
			"mcpServers.agent-browser retained by doctor.acceptedMCP: " + reason, true, 1,
		},
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
				{Path: acceptedPath, Server: test.server, Reason: test.reason},
			}
			rows := detect(t, "third-party-mcp", env)
			if test.accepted {
				if len(rows) != 1+test.warnings || rows[0].Severity != Accepted ||
					rows[0].Problem != test.problem ||
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
