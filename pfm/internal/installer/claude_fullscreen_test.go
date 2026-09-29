package installer

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

// canaryRegistry is a .claude.json as Claude writes it (two-space indent),
// carrying the boot canary's three keys among unrelated ones, the removable
// pair both in the middle and at the end so both splice paths run.
const canaryRegistry = `{
  "numStartups": 12,
  "fullscreenAutoDisabled": {
    "version": "2.1.284",
    "at": 1790000000000,
    "strikes": 2
  },
  "fullscreenBootPending": {
    "4242": 1790000000000
  },
  "tipsHistory": {"a<b": 1},
  "fullscreenBootStrikes": {
    "count": 2,
    "version": "2.1.284"
  }
}
`

const canaryCleared = `{
  "numStartups": 12,
  "fullscreenBootPending": {
    "4242": 1790000000000
  },
  "tipsHistory": {"a<b": 1}
}
`

func fullscreenEngine(t *testing.T, apply bool, accounts ...pfmconfig.Account) (*engine, *bytes.Buffer) {
	t.Helper()
	var output bytes.Buffer
	return &engine{
		options: Options{Home: t.TempDir(), Stdout: &output, ClaudeAccounts: accounts},
		apply:   apply, stamp: "test",
	}, &output
}

func fullscreenAccount(t *testing.T, settings, registry string) pfmconfig.Account {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "cc")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if settings != "" {
		if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(settings), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if registry != "" {
		if err := os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(registry), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	return pfmconfig.Account{ID: 3, ConfigDir: dir}
}

func readRegistry(t *testing.T, account pfmconfig.Account) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(account.ConfigDir, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestClearFullscreenAutoDisableRemovesOnlyTheVerdictKeys(t *testing.T) {
	account := fullscreenAccount(t, `{"tui":"fullscreen"}`, canaryRegistry)
	installer, output := fullscreenEngine(t, true, account)
	if err := installer.clearFullscreenAutoDisable(); err != nil {
		t.Fatal(err)
	}
	if got := readRegistry(t, account); got != canaryCleared {
		t.Fatalf("registry after clear:\n%s\nwant:\n%s", got, canaryCleared)
	}
	info, err := os.Stat(filepath.Join(account.ConfigDir, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want 0640 preserved", info.Mode().Perm())
	}
	if !strings.Contains(output.String(), "change  clear Claude's fullscreen auto-disable") {
		t.Errorf("install output does not report the clear:\n%s", output)
	}
}

func TestClearFullscreenAutoDisableDryRunReportsWithoutWriting(t *testing.T) {
	account := fullscreenAccount(t, `{"tui":"fullscreen"}`, canaryRegistry)
	installer, output := fullscreenEngine(t, false, account)
	if err := installer.clearFullscreenAutoDisable(); err != nil {
		t.Fatal(err)
	}
	if got := readRegistry(t, account); got != canaryRegistry {
		t.Fatalf("dry run wrote the registry:\n%s", got)
	}
	if !strings.Contains(output.String(), "change  clear Claude's fullscreen auto-disable") {
		t.Errorf("dry run does not report the planned clear:\n%s", output)
	}
}

func TestClearFullscreenAutoDisableLeavesOtherRegistriesAlone(t *testing.T) {
	for _, test := range []struct {
		name, settings, registry string
	}{
		{name: "no verdict keys", settings: `{"tui":"fullscreen"}`, registry: canaryCleared},
		{name: "tui default", settings: `{"tui":"default"}`, registry: canaryRegistry},
		{name: "no settings", registry: canaryRegistry},
	} {
		t.Run(test.name, func(t *testing.T) {
			account := fullscreenAccount(t, test.settings, test.registry)
			path := filepath.Join(account.ConfigDir, ".claude.json")
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			installer, output := fullscreenEngine(t, true, account)
			if err := installer.clearFullscreenAutoDisable(); err != nil {
				t.Fatal(err)
			}
			after, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if readRegistry(t, account) != test.registry || !os.SameFile(before, after) {
				t.Fatal("registry was rewritten")
			}
			if strings.Contains(output.String(), "change") {
				t.Errorf("output reports a change:\n%s", output)
			}
		})
	}
}

func TestClearFullscreenAutoDisableWritesThroughASymlink(t *testing.T) {
	account := fullscreenAccount(t, `{"tui":"fullscreen"}`, "")
	physical := filepath.Join(t.TempDir(), "shared.claude.json")
	if err := os.WriteFile(physical, []byte(canaryRegistry), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(account.ConfigDir, ".claude.json")
	if err := os.Symlink(physical, link); err != nil {
		t.Fatal(err)
	}
	installer, _ := fullscreenEngine(t, true, account)
	if err := installer.clearFullscreenAutoDisable(); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the symlink was replaced: %v %v", info, err)
	}
	raw, err := os.ReadFile(physical)
	if err != nil || string(raw) != canaryCleared {
		t.Fatalf("physical file = %q, %v", raw, err)
	}
}

func TestClearFullscreenAutoDisableReportsAnUnreadableRegistry(t *testing.T) {
	account := fullscreenAccount(t, `{"tui":"fullscreen"}`, "")
	if err := os.MkdirAll(filepath.Join(account.ConfigDir, ".claude.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	installer, output := fullscreenEngine(t, true, account)
	if err := installer.clearFullscreenAutoDisable(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "skip") || !strings.Contains(output.String(), "state unknown") {
		t.Fatalf("unreadable registry not reported:\n%s", output)
	}
}
