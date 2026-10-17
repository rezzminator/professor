package installer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

func TestMCPWiresCodexAndOpenCodeWithoutClaudeRegistry(t *testing.T) {
	home := t.TempDir()
	registry := filepath.Join(home, ".claude.json")
	original := `{"mcpServers":{"other":{"command":"operator"}}}`
	writeFixture(t, registry, original)
	codex := filepath.Join(home, ".codex")
	opencode := OpenCodeConfigPath(home)
	e := engine{
		options: Options{
			Home: home, CodexHomes: []string{codex}, OpenCodeConfigPath: opencode,
			MCPEnabled: map[string]bool{"chat": true, "harvester": true}, MCPPort: 8377, Stdout: io.Discard,
		},
		apply: true, managedRoot: managedRootForHome(home), stamp: "fixture",
	}
	if err := e.wireMCP(); err != nil {
		t.Fatal(err)
	}
	if got := readFixture(t, registry); got != original {
		t.Fatalf("Claude registry changed: %s", got)
	}
	for _, path := range []string{filepath.Join(codex, "config.toml"), opencode} {
		content, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(content), "chat-professor") ||
			!strings.Contains(string(content), "harvester-professor") {
			t.Fatalf("%s lacks the professor MCP server: %s err=%v", path, content, err)
		}
	}
}

func TestWireMCPDropsClaudeOwnership(t *testing.T) {
	for _, test := range []struct {
		name       string
		mode       Mode
		apply      bool
		enabled    bool
		opencode   bool
		unreadable bool
	}{
		{"uninstall", ModeUninstall, true, false, false, false},
		{"disabled-install", ModeApply, true, false, false, false},
		{"install-keeps-opencode", ModeApply, true, true, true, false},
		{"dry-run", ModeApply, false, true, true, false},
		{"unreadable-ledger", ModeUninstall, true, false, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			var stdout bytes.Buffer
			e := engine{
				options: Options{Home: home, Mode: test.mode, MCPPort: 8377, Stdout: &stdout},
				apply:   test.apply, managedRoot: managedRootForHome(home), stamp: "fixture",
			}
			if test.enabled {
				e.options.MCPEnabled = map[string]bool{"chat": true}
			}
			registry := filepath.Join(home, ".claude.json")
			originalRegistry := `{"mcpServers":{"owned":{"command":"invented"}}}`
			writeFixture(t, registry, originalRegistry)
			ownership := mcpOwnership{
				Registrations: map[string]map[string]any{registry: {"owned": map[string]any{"command": "invented"}}},
				Pending: map[string]map[string]any{
					registry: {"pending": map[string]any{"command": "invented-pending"}},
				},
			}
			if test.opencode {
				e.options.OpenCodeConfigPath = OpenCodeConfigPath(home)
				registration := e.mcpOpenCodeRegistration(chatRegistrationName)
				ownership.OpenCodeRegistrations = map[string]map[string]any{
					e.options.OpenCodeConfigPath: {chatRegistrationName: registration},
				}
				raw, err := json.Marshal(map[string]any{"mcp": map[string]any{chatRegistrationName: registration}})
				if err != nil {
					t.Fatal(err)
				}
				writeFixture(t, e.options.OpenCodeConfigPath, string(raw))
			}
			raw, err := json.Marshal(ownership)
			if err != nil {
				t.Fatal(err)
			}
			ledger := e.mcpOwnershipPath()
			writeFixture(t, ledger, string(raw))
			if test.unreadable {
				if err := os.Rename(ledger, ledger+".fixture"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(ledger, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			err = e.wireMCP()
			if test.unreadable {
				assertProbePath(t, err, ledger)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := readFixture(t, registry); got != originalRegistry {
				t.Fatalf("Claude registry=%s want=%s", got, originalRegistry)
			}
			if !test.apply {
				if got := readFixture(t, ledger); got != string(raw) {
					t.Fatalf("dry-run ledger=%s want=%s", got, raw)
				}
				return
			}
			if !test.opencode {
				if _, err := os.Stat(ledger); !os.IsNotExist(err) {
					t.Fatalf("retired ledger stat=%v want=not-exist", err)
				}
				want := fmt.Sprintf("  change  remove %s\n", ledger)
				if got := stdout.String(); got != want {
					t.Fatalf("output=%q want=%q", got, want)
				}
				return
			}
			got, err := e.loadMCPOwnership()
			if err != nil || len(got.Registrations) != 0 || len(got.Pending) != 0 ||
				!sameJSONValue(got.OpenCodeRegistrations, ownership.OpenCodeRegistrations) {
				t.Fatalf("ledger=%+v err=%v want OpenCode registrations=%v", got, err, ownership.OpenCodeRegistrations)
			}
			var document map[string]any
			if err := json.Unmarshal([]byte(readFixture(t, ledger)), &document); err != nil {
				t.Fatal(err)
			}
			if _, present := document["registrations"]; present {
				t.Fatal("saved ledger carries Claude registrations")
			}
			if _, present := document["pending"]; present {
				t.Fatal("saved ledger carries Claude pending registrations")
			}
			want := fmt.Sprintf("  change  write %s\n", ledger)
			if !strings.Contains(stdout.String(), want) {
				t.Fatalf("output=%q lacks=%q", stdout.String(), want)
			}
		})
	}
}

func TestClaudeUserRegistriesIncludeTheAmbientConfigDirTheLauncherPassesThrough(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	accountDir := pfmconfig.DefaultAccountDir(home, 1)
	accounts := []pfmconfig.Account{{ID: 1, ConfigDir: accountDir}}
	for _, ambient := range []string{accountDir, filepath.Join(home, "ambient")} {
		registries := ClaudeUserRegistries(home, accounts, ambient)
		count := 1
		if ambient != accountDir {
			count = 2
		}
		if len(registries) != count {
			t.Fatalf("registries=%#v, want %d", registries, count)
		}
		wantPath := filepath.Join(accountDir, ".claude.json")
		wantReason := "account 1 (CLAUDE_CONFIG_DIR=" + accountDir + " when pfm spawns it)"
		if registries[0].Path != wantPath || registries[0].Reason != wantReason || registries[0].Account != 1 {
			t.Fatalf("account registry=%#v, want path=%s reason=%q account=1", registries[0], wantPath, wantReason)
		}
		if ambient != accountDir {
			wantReason = "ambient CLAUDE_CONFIG_DIR=" + ambient + " (the claude launcher passes it through — internal_launch.go)"
			if registries[1].Path != filepath.Join(ambient, ".claude.json") || registries[1].Reason != wantReason ||
				registries[1].Account != 0 {
				t.Fatalf("ambient registry=%#v, want dir=%s reason=%q account=0", registries[1], ambient, wantReason)
			}
		}
	}
}

func TestMCPFamilyRegistrationsPreserveForeignAndCutOverLegacy(t *testing.T) {
	for _, chat := range []bool{false, true} {
		for _, harvester := range []bool{false, true} {
			for _, foreign := range []bool{false, true} {
				t.Run(fmt.Sprintf("chat=%t/harvester=%t/foreign=%t", chat, harvester, foreign), func(t *testing.T) {
					home := t.TempDir()
					codex := filepath.Join(home, ".codex")
					openCode := OpenCodeConfigPath(home)
					bin := filepath.Join(home, ".local", "bin", "pfm")
					command := bin
					if foreign {
						command = "operator"
					}
					writeFixture(
						t,
						filepath.Join(codex, "config.toml"),
						fmt.Sprintf(
							"[mcp_servers.professor]\ncommand = %q\nargs = [\"mcp\", \"serve\", \"--stdio\"]\n",
							command,
						),
					)
					writeFixture(
						t,
						openCode,
						fmt.Sprintf(
							`{"mcp":{"professor":{"type":"local","command":[%q,"mcp","serve","--stdio"],"enabled":true}}}`,
							command,
						),
					)
					e := engine{
						options: Options{
							Home:               home,
							CodexHomes:         []string{codex},
							OpenCodeConfigPath: openCode,
							MCPEnabled:         map[string]bool{"chat": chat, "harvester": harvester},
							MCPPort:            8377,
							Stdout:             io.Discard,
						},
						apply:       true,
						managedRoot: managedRootForHome(home),
						stamp:       "fixture",
					}
					if err := e.wireMCP(); err != nil {
						t.Fatal(err)
					}
					var cx struct {
						Servers map[string]struct {
							Command string
							Args    []string
						} `toml:"mcp_servers"`
					}
					if _, err := toml.DecodeFile(filepath.Join(codex, "config.toml"), &cx); err != nil {
						t.Fatal(err)
					}
					oc, err := decodeJSONCObject([]byte(readFixture(t, openCode)))
					if err != nil {
						t.Fatal(err)
					}
					servers, _ := oc["mcp"].(map[string]any)
					expected := 0
					if foreign {
						expected++
					}
					for family, on := range map[string]bool{"chat": chat, "harvester": harvester} {
						if !on {
							continue
						}
						expected++
						name := family + "-professor"
						want := []string{"mcp", "serve", "--stdio", "--family", family}
						if cx.Servers[name].Command != bin || !reflect.DeepEqual(cx.Servers[name].Args, want) {
							t.Fatalf("Codex %s = %+v", name, cx.Servers[name])
						}
						registration, ok := servers[name].(map[string]any)
						if !ok || !sameStrings(registration["command"].([]any), append([]string{bin}, want...)) {
							t.Fatalf("OpenCode %s = %+v", name, registration)
						}
					}
					if len(cx.Servers) != expected || len(servers) != expected {
						t.Fatalf("installed servers Codex=%+v OpenCode=%+v, want %d", cx.Servers, servers, expected)
					}
					if foreign &&
						(cx.Servers["professor"].Command != "operator" || servers["professor"].(map[string]any)["command"].([]any)[0] != "operator") {
						t.Fatal("foreign professor changed")
					}
				})
			}
		}
	}
}

func TestClaudeMCPCutoverHonorsNativeLock(t *testing.T) {
	for _, locked := range []string{"registry", "alias", "file", "none", "parent-alias", "parent-none"} {
		t.Run(locked, func(t *testing.T) {
			t.Setenv("CLAUDE_CONFIG_DIR", "")
			home := t.TempDir()
			path := filepath.Join(home, ".claude.json")
			alias := filepath.Join(home, "account", ".claude.json")
			original := fmt.Sprintf(
				`{"mcpServers":{"professor":{"type":"stdio","command":%q,"args":["mcp","serve","--stdio"]}}}`,
				filepath.Join(home, ".local", "bin", "pfm"),
			)
			writeFixture(t, path, original)
			if err := os.MkdirAll(filepath.Dir(alias), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(path, alias); err != nil {
				t.Fatal(err)
			}
			lock := path + ".lock"
			if locked == "alias" || locked == "parent-alias" {
				lock = alias + ".lock"
			}
			if strings.HasPrefix(locked, "parent-") {
				parentAlias := filepath.Join(home, "account-link")
				if err := os.Symlink(filepath.Dir(alias), parentAlias); err != nil {
					t.Fatal(err)
				}
				alias = filepath.Join(parentAlias, filepath.Base(alias))
			}
			unlocked := strings.HasSuffix(locked, "none")
			if locked == "file" {
				writeFixture(t, lock, "operator")
			} else if !unlocked {
				if err := os.Mkdir(lock, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			e := engine{options: Options{
				Home: home, Stdout: io.Discard,
				ClaudeAccounts: []pfmconfig.Account{{ID: 1, ConfigDir: filepath.Dir(alias)}},
				MCPEnabled:     map[string]bool{"chat": true},
			}, apply: true, stamp: "fixture"}
			err := e.cutOverClaudeMCP()
			if unlocked {
				if err != nil {
					t.Fatal(err)
				}
				if got := readFixture(t, path); !strings.Contains(got, "chat-professor") {
					t.Fatalf("unlocked cutover not published: %s", got)
				}
				for _, p := range []string{path, alias} {
					if _, err := os.Lstat(p + ".lock"); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("lock not released for %s: %v", p, err)
					}
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), lock) {
				t.Fatalf("native config lock was ignored: error=%v; want lock %s", err, lock)
			}
			if got := readFixture(t, path); got != original {
				t.Fatalf("locked registry overwritten: %s", got)
			}
			if _, err := os.Lstat(lock); err != nil {
				t.Fatalf("native lock removed: %v", err)
			}
			for _, candidate := range []string{path + ".lock", filepath.Join(home, "account", ".claude.json.lock")} {
				if candidate == lock {
					continue
				}
				if _, err := os.Lstat(candidate); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("partial acquisition leaked lock %s: %v", candidate, err)
				}
			}
			backups, err := filepath.Glob(path + ".pre-professor-*")
			if err != nil || len(backups) != 0 {
				t.Fatalf("locked registry backed up: %v, %v", backups, err)
			}
		})
	}
}

func TestClaudeMCPFamilyCutover(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		for _, enabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("foreign=%t/enabled=%t", foreign, enabled), func(t *testing.T) {
				home := t.TempDir()
				account := filepath.Join(home, "account")
				ambient := filepath.Join(home, "ambient")
				missing := filepath.Join(home, "missing")
				command := filepath.Join(home, ".local", "bin", "pfm")
				if foreign {
					command = "operator"
				}
				original := fmt.Sprintf(
					`{"theme":"keep","mcpServers":{"professor":{"type":"stdio","command":%q,"args":["mcp","serve","--stdio"]},"other":{"command":"operator"}}}`,
					command,
				)
				for _, dir := range []string{account, ambient} {
					writeFixture(t, filepath.Join(dir, ".claude.json"), original)
				}
				t.Setenv("CLAUDE_CONFIG_DIR", ambient)
				var stdout bytes.Buffer
				e := engine{
					options: Options{
						Home:           home,
						ClaudeAccounts: []pfmconfig.Account{{ID: 1, ConfigDir: account}, {ID: 2, ConfigDir: missing}},
						CodexHomes:     []string{},
						MCPEnabled:     map[string]bool{"chat": enabled, "harvester": enabled},
						MCPPort:        8377,
						Stdout:         &stdout,
					},
					apply:       true,
					managedRoot: managedRootForHome(home),
					stamp:       "fixture",
				}
				for range 2 {
					if err := e.wireMCP(); err != nil {
						t.Fatal(err)
					}
				}
				for _, dir := range []string{account, ambient} {
					raw := readFixture(t, filepath.Join(dir, ".claude.json"))
					var document map[string]any
					if err := json.Unmarshal([]byte(raw), &document); err != nil {
						t.Fatal(err)
					}
					servers := document["mcpServers"].(map[string]any)
					if foreign {
						if raw != original {
							t.Fatalf("foreign registry changed: %s", raw)
						}
						continue
					}
					count := 1
					if enabled {
						count = 3
					}
					if len(servers) != count || document["theme"] != "keep" {
						t.Fatalf("account registry=%s want %d servers", raw, count)
					}
					for _, family := range []string{"chat", "harvester"} {
						if enabled {
							shape, ok := servers[family+"-professor"].(map[string]any)
							if !ok ||
								!isExactStdioShape(
									shape,
									command,
									[]string{"mcp", "serve", "--stdio", "--family", family},
								) {
								t.Fatalf("family %s=%v", family, shape)
							}
						}
					}
				}
				if _, err := os.Stat(filepath.Join(missing, ".claude.json")); !os.IsNotExist(err) {
					t.Fatalf("missing registry created: %v", err)
				}
				if foreign && !strings.Contains(stdout.String(), "preserve foreign Claude MCP client professor") {
					t.Fatalf("foreign registration not reported: %s", stdout.String())
				}
			})
		}
	}
}
