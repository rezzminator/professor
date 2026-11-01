package reload

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/codexmeta"
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
	home := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(home, "auth.json"),
		[]byte(`{"tokens":{"access_token":"fixture","account_id":"fixture"}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	machine := pfmconfig.Config{
		Version:       pfmconfig.Version,
		CodexAccounts: []pfmconfig.CodexAccount{{ID: 3, Home: home}},
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

// A Codex chat's rollout is found plain or compressed, as Codex leaves a
// finished one, the plain twin winning beside its compressed copy; a reload
// that missed it would start the chat fresh instead of resuming it.
func TestSessionTranscriptFindsACompressedCodexRollout(t *testing.T) {
	home := t.TempDir()
	day := filepath.Join(home, "sessions", "2026", "09", "23")
	if err := os.MkdirAll(day, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(name string) string {
		path := filepath.Join(day, name)
		if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	compress := func(path string) string {
		compressed, err := codexmeta.CompressRollout(path)
		if err != nil {
			t.Fatal(err)
		}
		return compressed
	}
	plain := write("rollout-2026-09-23T12-07-24-plain-thread.jsonl")
	finished := compress(write("rollout-2026-09-23T12-07-24-finished-thread.jsonl"))
	compress(write("rollout-2026-09-23T12-07-24-twin-thread.jsonl"))
	twin := write("rollout-2026-09-23T12-07-24-twin-thread.jsonl")
	machine := pfmconfig.Config{CodexAccounts: []pfmconfig.CodexAccount{{ID: 1, Home: home}}}
	for _, row := range []struct{ id, want string }{
		{"plain-thread", plain}, {"finished-thread", finished}, {"twin-thread", twin}, {"absent-thread", ""},
	} {
		got, err := SessionTranscript(paths.Values{}, machine, pfmengine.Codex, row.id)
		if err != nil || got != row.want {
			t.Errorf("SessionTranscript(codex, %s) = %q, %v; want %q", row.id, got, err, row.want)
		}
	}
}

// TestReloadBirthDetachedShellIgnoresTheLoginDefault: with no seat process in
// the pane, a reload reads the caller's own birth config; the login default
// there names no seat, so it resolves as if nothing were exported.
func TestReloadBirthDetachedShellIgnoresTheLoginDefault(t *testing.T) {
	root := t.TempDir()
	values := paths.Values{StateDB: filepath.Join(root, "missing.db"), ProcRoot: t.TempDir()}
	machine := pfmconfig.Config{
		Claude: pfmconfig.Claude{Binary: "claude"},
		Accounts: []pfmconfig.Account{
			{ID: 1, ConfigDir: filepath.Join(root, "one")},
			{ID: 2, ConfigDir: filepath.Join(root, "two")},
		},
	}
	two := machine.Accounts[1].ConfigDir
	for _, test := range []struct {
		name, value, sentinel string
		want                  int
	}{
		{"neither set", "", "", 1},
		{"login default", two, two, 1},
		{"explicit", two, "", 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			account, _, err := BirthAccount(values, machine, "cc-seat", "unrecorded", Pane{PID: 100}, &bytes.Buffer{},
				&paths.MapEnv{Values: map[string]string{
					"CLAUDE_CONFIG_DIR": test.value, claudelaunch.ConfigDirDefaultEnv: test.sentinel,
				}})
			if err != nil || account != test.want {
				t.Fatalf("birth = %d, %v; want %d", account, err, test.want)
			}
		})
	}
}

func TestValidateCodexReloadRequiresLogin(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codex-3")
	machine := pfmconfig.Config{CodexAccounts: []pfmconfig.CodexAccount{{ID: 3, Home: home}}}
	_, err := ValidateAccount(machine, "cx", 3)
	want := "Codex account 3: " + filepath.Join(home, "auth.json") + " "
	if !errors.Is(err, pfmconfig.ErrCodexLoggedOut) || !strings.HasPrefix(err.Error(), want) {
		t.Fatalf("ValidateAccount error=%v; want %q", err, want)
	}
}
