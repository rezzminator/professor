package doctor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func layoutDoctorOutput(t *testing.T, runtime config.Runtime) (string, int, int) {
	t.Helper()
	var output bytes.Buffer
	warnings, failures := printLayoutChecks(&output, runtime, paths.OSEnv{})
	return output.String(), warnings, failures
}

func doctorLayoutWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLayoutDoctorCleanTargetPrintsNoFinding(t *testing.T) {
	runtime := testjail.CleanHome(t)
	output, warnings, failures := layoutDoctorOutput(t, runtime)
	for _, prefix := range []string{"session-store:", "managed-cleanup:", "legacy:", "state: legacy", "layout:"} {
		if strings.Contains(output, prefix) {
			t.Errorf("clean target printed %s in %q", prefix, output)
		}
	}
	if warnings != 0 || failures != 0 {
		t.Fatalf("clean target warnings=%d failures=%d output=%q", warnings, failures, output)
	}
	if !strings.Contains(output, "state: state.db="+runtime.Paths.StateDB+" missing") ||
		!strings.Contains(output, "state: state.cacheDb="+runtime.Paths.CacheDB+" missing") {
		t.Fatalf("missing state-path hints in %q", output)
	}
	root := filepath.Join(runtime.Paths.Home, ".local", "state", "pfm", "migrations")
	if !strings.Contains(output, "install journals: 0 in "+root+", 0 bytes\n") {
		t.Fatalf("missing clean journal inventory in %q", output)
	}
}

func TestLayoutDoctorReportsInstallJournalInventory(t *testing.T) {
	runtime := testjail.CleanHome(t)
	root := filepath.Join(runtime.Paths.Home, ".local", "state", "pfm", "migrations")
	for id, body := range map[string]string{
		"20260101T000000Z": `[{"result":"applied"},{"result":"restored"}]`,
		"20260102T000000Z": `[{"result":"pending"}]`,
		"20260103T000000Z": `[{"result":"pending"}]`,
		"20260105T000000Z": `{`,
	} {
		doctorLayoutWrite(t, filepath.Join(root, id, "journal.json"), body)
	}
	if err := os.MkdirAll(filepath.Join(root, "20260104T000000Z"), 0o700); err != nil {
		t.Fatal(err)
	}
	doctorLayoutWrite(t, filepath.Join(root, "20260103T000000Z", "rolled-back"), "done")
	doctorLayoutWrite(t, filepath.Join(root, "notes", "note"), "ignored")
	journals, err := installer.InstallJournals(runtime.Paths.Home)
	if err != nil {
		t.Fatal(err)
	}
	var size uint64
	for _, journal := range journals {
		size += journal.Bytes
	}
	output, _, failures := layoutDoctorOutput(t, runtime)
	for _, line := range []string{
		fmt.Sprintf("install journals: 5 in %s, %d bytes", root, size),
		"install journal 20260102T000000Z: pending records from a crashed or failed install — run pfm install --rollback 20260102T000000Z",
		"install journal 20260105T000000Z: UNREADABLE error=unexpected end of JSON input",
	} {
		if !strings.Contains(output, line+"\n") {
			t.Errorf("missing %q in %q", line, output)
		}
	}
	if strings.Contains(output, "install journal 20260101T000000Z:") ||
		strings.Contains(output, "install journal 20260103T000000Z:") || failures < 2 {
		t.Fatalf("rolled-back journal reported or failures missing: failures=%d output=%q", failures, output)
	}
}

func TestLayoutDoctorReportsUnreadableInstallJournalRoot(t *testing.T) {
	runtime := testjail.CleanHome(t)
	root := filepath.Join(runtime.Paths.Home, ".local", "state", "pfm", "migrations")
	doctorLayoutWrite(t, root, "file")
	output, _, failures := layoutDoctorOutput(t, runtime)
	if !strings.Contains(output, "install journals: UNREADABLE "+root+" error=") || failures == 0 {
		t.Fatalf("unreadable root failures=%d output=%q", failures, output)
	}
}

func TestLayoutDoctorCleanAccountStaysGreenWithLiveChat(t *testing.T) {
	runtime := testjail.CleanHome(t)
	account := filepath.Join(runtime.Paths.Home, ".cc", "2")
	runtime.Config.Accounts = append(runtime.Config.Accounts, config.Account{ID: 2, ConfigDir: account})
	if err := os.MkdirAll(account, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, entry := range installer.SessionPaths {
		store := filepath.Join(runtime.Paths.Home, ".claude", entry)
		if err := os.MkdirAll(store, 0o700); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(account, entry)
		if _, err := os.Lstat(link); err == nil {
			if err := os.Remove(link); err != nil {
				t.Fatal(err)
			}
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if err := os.Symlink(store, link); err != nil {
			t.Fatal(err)
		}
	}
	doctorLayoutWrite(t, filepath.Join(account, "settings.json"), `{}`)
	doctorLayoutWrite(t, filepath.Join(account, ".claude.json"), `{}`)
	doctorLayoutWrite(t, filepath.Join(account, "sessions", "123.json"), `{}`)
	before, beforeWarnings, beforeFailures := layoutDoctorOutput(t, runtime)
	if err := os.MkdirAll(filepath.Join(runtime.Paths.ProcRoot, "123"), 0o700); err != nil {
		t.Fatal(err)
	}
	after, warnings, failures := layoutDoctorOutput(t, runtime)
	if after != before || warnings != beforeWarnings || failures != beforeFailures {
		t.Fatalf(
			"live chat changed doctor: before=%q (%d,%d), after=%q (%d,%d)",
			before,
			beforeWarnings,
			beforeFailures,
			after,
			warnings,
			failures,
		)
	}
	for _, line := range []string{"session-store:", "legacy:", "layout:"} {
		if strings.Contains(after, line) {
			t.Fatalf("healthy account printed %q in %q", line, after)
		}
	}
}

func TestLayoutDoctorSessionLinesAndFailures(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		change func(*testing.T, string)
		want   string
	}{
		{"missing", func(t *testing.T, path string) {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}, "missing — run pfm install"},
		{"real-dir", func(t *testing.T, path string) {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			doctorLayoutWrite(t, filepath.Join(path, "id.jsonl"), "line")
		}, "is a real dir (1 entries) — run pfm install"},
		{"wrong-link", func(t *testing.T, path string) {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("/foreign/projects", path); err != nil {
				t.Fatal(err)
			}
		}, "points at /foreign/projects, want"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			runtime := testjail.CleanHome(t)
			runtime.Config.Accounts = append(
				runtime.Config.Accounts,
				config.Account{ID: 2, ConfigDir: filepath.Join(runtime.Paths.Home, ".cc", "2")},
			)
			path := filepath.Join(runtime.Paths.Home, ".cc", "2", "projects")
			testCase.change(t, path)
			output, _, failures := layoutDoctorOutput(t, runtime)
			if failures < 1 || !strings.Contains(output, "session-store: "+path+" "+testCase.want) {
				t.Fatalf("failures=%d output=%q", failures, output)
			}
		})
	}
}

func TestLayoutDoctorManagedCleanupLinesAndTallies(t *testing.T) {
	for _, testCase := range []struct {
		name, body, want string
		required         bool
		warnings         int
	}{
		{"absent", "", "missing — transcripts older than 30 days are deleted by any Claude launch outside pfm", true, 1},
		{"wrong", `{"cleanupPeriodDays":30}`, "cleanupPeriodDays=30, want 36500", true, 1},
		{"right", `{"cleanupPeriodDays":36500}`, "", true, 0},
		{"off", "", "managed-cleanup: check off by config", false, 0},
		{"unreadable", "{bad", "managed-cleanup UNREADABLE", true, 0},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			runtime := testjail.CleanHome(t)
			path := filepath.Join(runtime.Paths.ManagedSettingsDir, "pfm.json")
			if testCase.body == "" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else {
				doctorLayoutWrite(t, path, testCase.body)
			}
			runtime.Config.Claude.RequireManagedCleanup = testCase.required
			output, warnings, failures := layoutDoctorOutput(t, runtime)
			if warnings != testCase.warnings || (testCase.want != "" && !strings.Contains(output, testCase.want)) {
				t.Fatalf("warnings=%d failures=%d output=%q", warnings, failures, output)
			}
			if testCase.name == "unreadable" && failures != 1 {
				t.Fatalf("unreadable failures=%d output=%q", failures, output)
			}
			if testCase.name == "right" && strings.Contains(output, "managed-cleanup:") {
				t.Fatalf("right value output=%q", output)
			}
		})
	}
}

func TestLayoutDoctorLegacyAccountAndUnreadableLines(t *testing.T) {
	runtime := testjail.CleanHome(t)
	runtime.Config.Accounts = append(
		runtime.Config.Accounts,
		config.Account{ID: 2, ConfigDir: filepath.Join(runtime.Paths.Home, ".cc", "2")},
	)
	home := runtime.Paths.Home
	settings := filepath.Join(home, ".cc", "2", "settings.json")
	doctorLayoutWrite(
		t,
		settings,
		`{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"`+home+`/.local/bin/pfm internal launcher-repair"}]}]},"statusLine":{"type":"command","command":"`+home+`/.local/bin/pfm statusline"},"subagentStatusLine":{"type":"command","command":"`+home+`/.local/bin/pfm-statusline --subagents"}}`,
	)
	registry := filepath.Join(home, ".cc", "2", ".claude.json")
	doctorLayoutWrite(t, registry, `{"mcpServers":{"chat":{}}}`)
	ledger := map[string]any{"registrations": map[string]any{registry: map[string]any{"chat": map[string]any{}}}}
	encoded, err := json.Marshal(ledger)
	if err != nil {
		t.Fatal(err)
	}
	doctorLayoutWrite(
		t,
		filepath.Join(home, ".local", "share", "pfm", "install", "mcp-ownership.json"),
		string(encoded),
	)
	clone, err := paths.ReadSourceRepoMarker(home)
	if err != nil {
		t.Fatal(err)
	}
	memory, err := os.ReadFile(filepath.Join(clone, "templates", "project", "scripts", "memory-wire.sh"))
	if err != nil {
		t.Fatal(err)
	}
	memory = bytes.Replace(memory, []byte("# memory-wire.sh —"), []byte("# cc-memory-wire.sh —"), 1)
	memory = bytes.Replace(memory, []byte("{MEMORY_VAULT_DIR}"), []byte("vault"), 1)
	helper := filepath.Join(home, ".cc", "2", "scripts", "cc-memory-wire.sh")
	doctorLayoutWrite(t, helper, string(memory))
	output, _, failures := layoutDoctorOutput(t, runtime)
	for _, want := range []string{"legacy: " + settings + " still carries pfm hooks", "legacy: " + settings + " still carries pfm statusLine", "legacy: " + settings + " still carries pfm subagentStatusLine", "legacy: " + registry + " still carries pfm mcpServers.chat", "legacy: " + helper + " names cc-memory-wire.sh"} {
		if !strings.Contains(output, want) {
			t.Errorf("missing %q from %q", want, output)
		}
	}
	if failures < 2 {
		t.Fatalf("failures=%d output=%q", failures, output)
	}
	doctorLayoutWrite(t, settings, "{bad")
	output, _, _ = layoutDoctorOutput(t, runtime)
	if !strings.Contains(output, "legacy: "+settings+" UNREADABLE error=") {
		t.Fatalf("malformed account file output=%q", output)
	}
	doctorLayoutWrite(
		t,
		filepath.Join(home, ".local", "share", "pfm", "install", "settings-hook-ownership.json"),
		"{bad",
	)
	output, _, _ = layoutDoctorOutput(t, runtime)
	if !strings.Contains(output, "legacy: ownership ledger UNREADABLE error=") {
		t.Fatalf("unreadable ledger output=%q", output)
	}
}

func TestLayoutDoctorStateAndOtherRows(t *testing.T) {
	runtime := testjail.CleanHome(t)
	home := runtime.Paths.Home
	legacy := filepath.Join(home, ".cc", "fleet.db")
	doctorLayoutWrite(t, legacy, "db")
	staged := filepath.Join(home, ".local", "share", "pfm", "install", "harness-prompts")
	if err := os.MkdirAll(staged, 0o700); err != nil {
		t.Fatal(err)
	}
	output, warnings, _ := layoutDoctorOutput(t, runtime)
	if warnings < 2 || !strings.Contains(output, "state: legacy "+legacy+" still present — run pfm install") ||
		!strings.Contains(output, "layout: staged-prompts remove "+staged) {
		t.Fatalf("warnings=%d output=%q", warnings, output)
	}
}

func TestLayoutDoctorHomeMCPLines(t *testing.T) {
	runtime := testjail.CleanHome(t)
	home := runtime.Paths.Home
	mcp := filepath.Join(home, ".mcp.json")
	ledger := filepath.Join(home, ".local", "share", "pfm", "install", "mcp-ownership.json")
	doctorLayoutWrite(t, mcp, fmt.Sprintf(
		`{"mcpServers":{"harvester":{"type":"http","url":"http://127.0.0.1:%d/mcp/harvester"},"operator":{"command":"own"}}}`,
		runtime.Config.MCP.HTTP.Port,
	))
	doctorLayoutWrite(t, ledger, `{"clients":["harvester"]}`)
	output, _, failures := layoutDoctorOutput(t, runtime)
	for _, want := range []string{
		"legacy: " + mcp + " still carries pfm mcpServers.harvester — run pfm install\n",
		"legacy: " + ledger + " still carries pfm clients — run pfm install\n",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("missing %q from %q", want, output)
		}
	}
	if failures != 2 {
		t.Fatalf("failures=%d output=%q", failures, output)
	}
	doctorLayoutWrite(t, mcp, "{bad")
	output, _, failures = layoutDoctorOutput(t, runtime)
	if !strings.Contains(output, "layout: home-mcp UNREADABLE "+mcp+" error=") || failures < 1 {
		t.Fatalf("malformed home .mcp.json output=%q failures=%d", output, failures)
	}
	doctorLayoutWrite(t, ledger, "{bad")
	output, _, _ = layoutDoctorOutput(t, runtime)
	if got := strings.Count(output, "legacy: ownership ledger UNREADABLE error="); got != 1 {
		t.Fatalf("unreadable ledger reported %d times: %q", got, output)
	}
}
