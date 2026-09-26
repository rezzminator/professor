package reload

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestReloadBirthCarriesLaunchRecord(t *testing.T) {
	root := t.TempDir()
	values := paths.Values{StateDB: filepath.Join(root, "pfm.db"), ProcRoot: t.TempDir()}
	machine := pfmconfig.Config{
		Claude: pfmconfig.Claude{Cache1H: true},
		Accounts: []pfmconfig.Account{
			{ID: 1, ConfigDir: filepath.Join(root, "one")},
			{ID: 2, ConfigDir: filepath.Join(root, "two"), Claude: &pfmconfig.ClaudePrefs{Cache1H: false}},
		},
	}
	const session = "11111111-1111-4111-8111-111111111111"
	if err := fleetdb.RecordLaunch(context.Background(), values, fleetdb.Launch{
		SessionID: session, Engine: pfmengine.Claude, Account: 2, Cache1H: false,
	}, 100); err != nil {
		t.Fatal(err)
	}
	account, cache, err := BirthAccount(values, machine, "cc-seat", session, Pane{}, &bytes.Buffer{},
		&paths.MapEnv{Values: map[string]string{"CLAUDE_CONFIG_DIR": machine.Accounts[0].ConfigDir}})
	if err != nil || account != 2 || cache {
		t.Fatalf("birth = %d/%t, %v; want 2/5m from record", account, cache, err)
	}
}

func TestReloadBirthWithoutRecordUsesProcessAccountAndConfigCache(t *testing.T) {
	root := t.TempDir()
	values := paths.Values{StateDB: filepath.Join(root, "missing.db"), ProcRoot: t.TempDir()}
	machine := pfmconfig.Config{
		Claude: pfmconfig.Claude{Binary: "claude", Cache1H: true},
		Accounts: []pfmconfig.Account{
			{ID: 1, ConfigDir: filepath.Join(root, "one")},
			{ID: 2, ConfigDir: filepath.Join(root, "two"), Claude: &pfmconfig.ClaudePrefs{Cache1H: false}},
		},
	}
	processDir := filepath.Join(values.ProcRoot, "200")
	if err := os.MkdirAll(processDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"cmdline": "claude\x00",
		"environ": "CLAUDE_CONFIG_DIR=" + machine.Accounts[1].ConfigDir + "\x00ENABLE_PROMPT_CACHING_1H=1\x00",
		"stat":    "200 (claude) S 100 1 1 0 -1 0 0 0 0 0 0 0 0 0 0 0 20 0 100\n",
	} {
		if err := os.WriteFile(filepath.Join(processDir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	account, cache, err := BirthAccount(values, machine, "cc-seat", "unrecorded", Pane{PID: 100}, &bytes.Buffer{},
		&paths.MapEnv{Values: map[string]string{
			"CLAUDE_CONFIG_DIR": machine.Accounts[0].ConfigDir,
		}})
	if err != nil || account != 2 || cache {
		t.Fatalf("birth = %d/%t, %v; want 2/5m from config", account, cache, err)
	}
}

func TestReloadBirthUnreadableRecordStops(t *testing.T) {
	root := t.TempDir()
	values := paths.Values{StateDB: filepath.Join(root, "broken.db"), ProcRoot: t.TempDir()}
	if err := os.WriteFile(values.StateDB, []byte("not SQLite"), 0o600); err != nil {
		t.Fatal(err)
	}
	machine := pfmconfig.Config{Accounts: []pfmconfig.Account{{ID: 1, ConfigDir: filepath.Join(root, "one")}}}
	const session = "11111111-1111-4111-8111-111111111111"
	_, _, err := BirthAccount(values, machine, "cc-seat", session, Pane{}, &bytes.Buffer{}, &paths.MapEnv{})
	if err == nil || !strings.Contains(err.Error(), "read launch record for "+session) {
		t.Fatalf("corrupt record error = %v", err)
	}
}

func TestValidateReloadAccountUsesTheSeatEngineRoster(t *testing.T) {
	machine := pfmconfig.Config{
		Version:       pfmconfig.Version,
		CodexAccounts: []pfmconfig.CodexAccount{{ID: 3, Home: "/codex/3"}},
	}
	if _, err := ValidateAccount(machine, "cx", 3); err != nil {
		t.Fatalf("requested Codex account rejected: %v", err)
	}
	if _, err := ValidateAccount(machine, "cc", 1); err == nil ||
		!strings.Contains(err.Error(), "no Claude accounts configured") {
		t.Fatalf("empty Claude roster error = %v", err)
	}
	if _, err := ValidateAccount(machine, "cx", 4); err == nil ||
		!strings.Contains(err.Error(), "requested Codex account 4") {
		t.Fatalf("off-roster Codex error = %v", err)
	}
}

func TestReloadExplicitlyRejectsOpenCode(t *testing.T) {
	machine := pfmconfig.Config{OpenCodeAccounts: []pfmconfig.OpenCodeAccount{{ID: 1, Home: "/opencode"}}}
	if _, err := ValidateAccount(machine, pfmengine.OpenCode, 1); err == nil ||
		!strings.Contains(err.Error(), "OpenCode") {
		t.Fatalf("ValidateAccount(OpenCode) error=%v, want product-level refusal", err)
	}
	_, err := SessionTranscript(paths.Values{Roots: map[pfmengine.ID][]string{
		pfmengine.Claude: {t.TempDir()}, pfmengine.OpenCode: {t.TempDir()},
	}}, machine, pfmengine.OpenCode, "ses-fixture")
	if err == nil || !strings.Contains(err.Error(), "OpenCode") {
		t.Fatalf("SessionTranscript(OpenCode) error=%v, want product-level refusal", err)
	}
}
