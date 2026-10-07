package hostcheck

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestLegacyConfig(t *testing.T) {
	for _, present := range []bool{false, true} {
		t.Run(fmt.Sprint(present), func(t *testing.T) {
			env := fixtureEnv(t)
			if present {
				writeFile(t, env.ConfigPath, "{}")
			}
			var want []Row
			for _, name := range []string{config.FileName, "config.json"} {
				path := filepath.Join(env.LegacyConfigDir, name)
				writeFile(t, path, "{}")
				fix := "mv " + path + " " + env.ConfigPath
				if present {
					fix = "diff " + path + " " + env.ConfigPath + " && rm " + path
				}
				want = append(want, Row{Block, "legacy-config", path, "legacy pfm config outside the clone", fix})
			}
			assertRows(t, detect(t, "legacy-config", env), want...)
			env.ConfigExplicit = true
			env.CloneConfigPath = env.ConfigPath
			env.ConfigPath = want[0].Path
			assertRows(t, detect(t, "legacy-config", env), Row{
				Block, "legacy-config", env.ConfigPath, "explicit --config inside the legacy config dir", want[0].Fix,
			})
			env.ConfigPath = filepath.Join(env.Home, "elsewhere.json")
			assertRows(t, detect(t, "legacy-config", env))
		})
	}
	t.Run("physical-config", func(t *testing.T) {
		env := fixtureEnv(t)
		writeFile(t, env.ConfigPath, "{}")
		makeDir(t, env.LegacyConfigDir)
		if err := os.Symlink(env.ConfigPath, filepath.Join(env.LegacyConfigDir, config.FileName)); err != nil {
			t.Fatal(err)
		}
		assertRows(t, detect(t, "legacy-config", env))
	})
	t.Run("unreadable-and-readable", func(t *testing.T) {
		env := fixtureEnv(t)
		path := filepath.Join(env.LegacyConfigDir, config.FileName)
		makeDir(t, path)
		other := filepath.Join(env.LegacyConfigDir, "config.json")
		writeFile(t, other, "{}")
		rows := detect(t, "legacy-config", env)
		assertUnreadable(t, rows[:1], "legacy-config", path, syscall.EISDIR)
		assertRows(
			t,
			rows[1:],
			Row{
				Block,
				"legacy-config",
				other,
				"legacy pfm config outside the clone",
				"mv " + other + " " + env.ConfigPath,
			},
		)
	})
	for _, kind := range []string{"unknown clone", "unreadable explicit", "nested", "physical alias"} {
		t.Run(kind, func(t *testing.T) {
			env := fixtureEnv(t)
			env.ConfigExplicit = true
			env.CloneConfigPath = env.ConfigPath
			env.ConfigPath = filepath.Join(env.LegacyConfigDir, config.FileName)
			if kind == "nested" {
				env.ConfigPath = filepath.Join(env.LegacyConfigDir, "nested", config.FileName)
			}
			if kind == "unreadable explicit" {
				makeDir(t, env.ConfigPath)
				assertUnreadable(t, detect(t, "legacy-config", env), "legacy-config", env.ConfigPath, syscall.EISDIR)
				return
			}
			writeFile(t, env.ConfigPath, "{}")
			writeFile(t, filepath.Join(env.LegacyConfigDir, config.LegacyFileName), "{}")
			if kind == "physical alias" {
				alias := filepath.Join(env.Home, "alias")
				symlink(t, env.LegacyConfigDir, alias)
				env.ConfigPath = filepath.Join(alias, config.FileName)
			}
			fix := "mv " + env.ConfigPath + " " + env.CloneConfigPath
			if kind == "unknown clone" {
				env.CloneConfigPath = ""
				fix = "move " + env.ConfigPath + " into the clone as pfm.config.json, then run pfm install from the clone"
			}
			assertRows(t, detect(t, "legacy-config", env), Row{
				Block, "legacy-config", env.ConfigPath, "explicit --config inside the legacy config dir", fix,
			})
		})
	}
}

func TestLegacyHarvesterConfig(t *testing.T) {
	for _, present := range []bool{false, true} {
		t.Run(fmt.Sprint(present), func(t *testing.T) {
			env := fixtureEnv(t)
			path := filepath.Join(env.LegacyConfigDir, "harvester.config.json")
			target := filepath.Join(filepath.Dir(env.ConfigPath), "harvester.config.json")
			writeFile(t, path, "{}")
			fix := "mv " + path + " " + target
			if present {
				writeFile(t, target, "{}")
				fix = "diff " + path + " " + target + " && rm " + path
			}
			assertRows(
				t,
				detect(t, "legacy-harvester-config", env),
				Row{Block, "legacy-harvester-config", path, "legacy harvester config outside the clone", fix},
			)
		})
	}
	env := fixtureEnv(t)
	path := filepath.Join(env.LegacyConfigDir, "harvester.config.json")
	makeDir(t, path)
	assertUnreadable(t, detect(t, "legacy-harvester-config", env), "legacy-harvester-config", path, syscall.EISDIR)
	// The legacy location may already be the clone's location.
	env.ConfigPath = filepath.Join(env.LegacyConfigDir, config.FileName)
	assertRows(t, detect(t, "legacy-harvester-config", env))
}

func TestPreSplitConfig(t *testing.T) {
	for _, enabled := range []string{"true", "false", "null"} {
		t.Run(enabled, func(t *testing.T) {
			env := fixtureEnv(t)
			writeFile(
				t,
				env.ConfigPath,
				`{"mcp":{"servers":{"harvester":{"enabled":`+enabled+`}},"http":{"port":8377}}}`,
			)
			sibling := filepath.Join(filepath.Dir(env.ConfigPath), "config.json")
			writeFile(t, sibling, "{}")
			assertRows(
				t,
				detect(t, "pre-split-config", env),
				Row{
					Block,
					"pre-split-config",
					sibling,
					"a pre-split config.json beside the config",
					"rm " + sibling + " once its content is in " + env.ConfigPath,
				},
				Row{
					Block,
					"pre-split-config",
					env.ConfigPath,
					"mcp.servers.harvester belongs in harvester.config.json",
					"move mcp.servers.harvester.enabled (" + enabled + ") to \"enabled\" in " + filepath.Join(
						filepath.Dir(env.ConfigPath),
						"harvester.config.json",
					) + ", then delete mcp.servers.harvester from " + env.ConfigPath,
				},
				Row{
					Block,
					"pre-split-config",
					env.ConfigPath,
					"mcp.http.port is the pre-split default 8377",
					"set mcp.http.port to 18377 in " + env.ConfigPath + "; pfm install re-wires every client",
				},
			)
		})
	}
	t.Run("old-name", func(t *testing.T) {
		env := fixtureEnv(t)
		env.ConfigPath = filepath.Join(filepath.Dir(env.ConfigPath), "config.json")
		writeFile(t, env.ConfigPath, "{}")
		assertRows(
			t,
			detect(t, "pre-split-config", env),
			Row{
				Block,
				"pre-split-config",
				env.ConfigPath,
				"the config still has its pre-split name",
				"mv " + env.ConfigPath + " " + filepath.Join(filepath.Dir(env.ConfigPath), config.FileName),
			},
		)
	})
	t.Run("unreadable", func(t *testing.T) {
		env := fixtureEnv(t)
		makeDir(t, env.ConfigPath)
		sibling := filepath.Join(filepath.Dir(env.ConfigPath), "config.json")
		makeDir(t, sibling)
		rows := detect(t, "pre-split-config", env)
		assertUnreadable(t, rows[:1], "pre-split-config", env.ConfigPath, syscall.EISDIR)
		assertUnreadable(t, rows[1:], "pre-split-config", sibling, syscall.EISDIR)
	})
	t.Run("malformed", func(t *testing.T) {
		env := fixtureEnv(t)
		writeFile(t, env.ConfigPath, "{")
		assertUnreadable(
			t,
			detect(t, "pre-split-config", env),
			"pre-split-config",
			env.ConfigPath,
			jsonDecodeError("{"),
		)
	})
	t.Run("malformed-sibling", func(t *testing.T) {
		env := fixtureEnv(t)
		writeFile(t, env.ConfigPath, "{}")
		path := filepath.Join(filepath.Dir(env.ConfigPath), "config.json")
		writeFile(t, path, "{")
		assertUnreadable(t, detect(t, "pre-split-config", env), "pre-split-config", path, jsonDecodeError("{"))
	})
}

func jsonDecodeError(raw string) error { var value any; return json.Unmarshal([]byte(raw), &value) }

func TestLegacyDatabases(t *testing.T) {
	for _, kind := range []string{"state", "cache"} {
		for _, present := range []bool{false, true} {
			t.Run(kind+fmt.Sprint(present), func(t *testing.T) {
				env := fixtureEnv(t)
				legacy, target := paths.LegacyStateDB(env.Home), env.StateDB
				if kind == "cache" {
					legacy, target = paths.LegacyCacheDB(env.Home), env.CacheDB
				}
				for _, suffix := range []string{"", "-wal", "-shm"} {
					writeFile(t, legacy+suffix, "data")
				}
				fix := "close every chat, stop pfm's services, then: mv " + legacy + " " + target + " && mv " + legacy + "-wal " + target + "-wal && mv " + legacy + "-shm " + target + "-shm"
				if present {
					writeFile(t, target, "data")
					fix = "both exist — keep " + target + ": rm " + legacy + " " + legacy + "-wal " + legacy + "-shm"
				}
				assertRows(
					t,
					detect(t, "legacy-"+kind+"-db", env),
					Row{
						Block,
						"legacy-" + kind + "-db",
						legacy,
						"legacy " + kind + " database at the pre-layout path",
						fix,
					},
				)
				if kind == "state" {
					env.StateDB = legacy
				} else {
					env.CacheDB = legacy
				}
				assertRows(t, detect(t, "legacy-"+kind+"-db", env))
			})
		}
		t.Run(kind+"-unreadable", func(t *testing.T) {
			env := fixtureEnv(t)
			legacy := paths.LegacyStateDB(env.Home)
			if kind == "cache" {
				legacy = paths.LegacyCacheDB(env.Home)
			}
			writeFile(t, filepath.Dir(legacy), "file")
			rows := detect(t, "legacy-"+kind+"-db", env)
			if len(rows) != 3 {
				t.Fatalf("rows=%v", rows)
			}
			for i, suffix := range []string{"", "-wal", "-shm"} {
				assertUnreadable(t, rows[i:i+1], "legacy-"+kind+"-db", legacy+suffix, syscall.ENOTDIR)
			}
		})
	}
}

func TestLegacyHarvesterCache(t *testing.T) {
	for _, present := range []bool{false, true} {
		t.Run(fmt.Sprint(present), func(t *testing.T) {
			env := fixtureEnv(t)
			path, target := paths.LegacyHarvesterCacheDir(env.Home), paths.HarvesterCacheDir(env.Home)
			makeDir(t, path)
			fix := "mv " + path + " " + target
			if present {
				makeDir(t, target)
				fix = "rm -r " + path
			}
			assertRows(
				t,
				detect(t, "legacy-harvester-cache", env),
				Row{Warn, "legacy-harvester-cache", path, "pre-rename harvester cache dir", fix},
			)
			env.HarvesterCacheDir = path
			assertRows(t, detect(t, "legacy-harvester-cache", env))
		})
	}
	env := fixtureEnv(t)
	path := paths.LegacyHarvesterCacheDir(env.Home)
	writeFile(t, path, "file")
	assertUnreadable(t, detect(t, "legacy-harvester-cache", env), "legacy-harvester-cache", path, syscall.ENOTDIR)
}

func TestStagedShimAndPrompts(t *testing.T) {
	t.Run("shim", func(t *testing.T) {
		env := fixtureEnv(t)
		path := filepath.Join(env.Home, ".zshrc")
		line := `source "$HOME/.local/share/pfm/install/shim/pfm.zsh"`
		writeFile(
			t,
			path,
			"# "+line+"\n"+line+"\n"+`source "$HOME/clone/pfm/internal/installer/assets/shim/pfm.zsh"`+"\n",
		)
		assertRows(
			t,
			detect(t, "staged-shim", env),
			Row{
				Block,
				"staged-shim",
				path,
				"sources pfm's retired staged shim",
				"delete the line \"" + line + "\" from " + path + "; pfm install writes the clone's source line",
			},
		)
	})
	t.Run("prompts", func(t *testing.T) {
		env := fixtureEnv(t)
		prompts := filepath.Join(env.ManagedRoot, "harness-prompts")
		makeDir(t, prompts)
		assertRows(
			t,
			detect(t, "staged-prompts", env),
			Row{
				Warn,
				"staged-prompts",
				prompts,
				"retired staged prompt dir",
				"rm -r " + prompts + " once no chat started before the move is open",
			},
		)
	})
	t.Run("shim-unreadable", func(t *testing.T) {
		env := fixtureEnv(t)
		path := filepath.Join(env.Home, ".zshrc")
		makeDir(t, path)
		assertUnreadable(t, detect(t, "staged-shim", env), "staged-shim", path, syscall.EISDIR)
	})
	t.Run("prompts-unreadable", func(t *testing.T) {
		env := fixtureEnv(t)
		path := filepath.Join(env.ManagedRoot, "harness-prompts")
		writeFile(t, path, "file")
		assertUnreadable(t, detect(t, "staged-prompts", env), "staged-prompts", path, syscall.ENOTDIR)
	})
}

func TestSharedDB(t *testing.T) {
	for _, content := range []string{"", "data"} {
		t.Run(content, func(t *testing.T) {
			env := fixtureEnv(t)
			path := filepath.Join(env.Home, ".local", "state", "pfm", "shared.db")
			writeFile(t, path, content)
			problem := "retired empty shared.db"
			if content != "" {
				problem = "retired shared.db holds 4 bytes"
			}
			assertRows(t, detect(t, "shared-db", env), Row{Warn, "shared-db", path, problem, "rm " + path})
		})
	}
	env := fixtureEnv(t)
	path := filepath.Join(env.Home, ".local", "state", "pfm", "shared.db")
	makeDir(t, path)
	assertUnreadable(t, detect(t, "shared-db", env), "shared-db", path, syscall.EISDIR)
}

func TestStrayDir(t *testing.T) {
	env := fixtureEnv(t)
	root := filepath.Dir(config.DefaultAccountDir(env.Home, 1))
	empty, filled, file := filepath.Join(root, ".git"), filepath.Join(root, ".codex"), filepath.Join(root, ".agents")
	makeDir(t, empty)
	writeFile(t, filepath.Join(filled, "keep"), "data")
	writeFile(t, file, "data")
	assertRows(t, detect(t, "stray-dir", env),
		Row{Warn, "stray-dir", empty, "empty stray dir", "rmdir " + empty},
		Row{Warn, "stray-dir", filled, "stray dir holds 1 entries", "inspect, then rm -r " + filled},
		Row{Warn, "stray-dir", file, "not a directory", "inspect, then rm -r " + file})
	t.Run("unreadable", func(t *testing.T) {
		env := fixtureEnv(t)
		root := filepath.Dir(config.DefaultAccountDir(env.Home, 1))
		writeFile(t, root, "file")
		rows := detect(t, "stray-dir", env)
		if len(rows) != 3 {
			t.Fatalf("rows=%v", rows)
		}
		for i, name := range []string{".git", ".codex", ".agents"} {
			assertUnreadable(t, rows[i:i+1], "stray-dir", filepath.Join(root, name), syscall.ENOTDIR)
		}
	})
}
