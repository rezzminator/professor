package installer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
		if err != nil || !strings.Contains(string(content), professorName) {
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
				registration := e.mcpOpenCodeRegistration()
				ownership.OpenCodeRegistrations = map[string]map[string]any{
					e.options.OpenCodeConfigPath: {professorName: registration},
				}
				raw, err := json.Marshal(map[string]any{"mcp": map[string]any{professorName: registration}})
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
