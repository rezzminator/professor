package installer

import (
	"io"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "hostops/pfm/internal/config"
)

func TestApplyRemovesOwnedHooksAndLedgerForDroppedSettingsSeat(t *testing.T) {
	home := t.TempDir()
	seatA := filepath.Join(home, ".claude")
	seatB := filepath.Join(home, ".cc", "2")
	writeFixture(t, filepath.Join(seatA, "settings.json"), `{
  "hooks":{"SessionStart":[{"matcher":"","hooks":[{"type":"command","command":"operator-keep"}]}]}
}`)
	writeFixture(t, filepath.Join(seatB, "settings.json"), `{"hooks":{}}`)
	managed := managedRootForHome(home)
	apply := func(configDirs ...string) {
		t.Helper()
		installer := engine{
			options: Options{
				Mode:       ModeApply,
				Home:       home,
				ConfigDirs: configDirs,
				Stdout:     io.Discard,
			},
			apply:       true,
			managedRoot: managed,
			stamp:       "fixture",
		}
		if err := installer.wireSettings(); err != nil {
			t.Fatal(err)
		}
	}
	apply(seatA, seatB)
	apply(seatB)

	dropped := readFixture(t, filepath.Join(seatA, "settings.json"))
	var document map[string]any
	if err := unmarshalKeepingNumbers([]byte(dropped), &document); err != nil {
		t.Fatal(err)
	}
	pfmBinary := filepath.Join(home, ".local", "bin", "pfm")
	for key, count := range countSettingsHookCommands(document) {
		if count > 0 && (key.Command == pfmBinary || strings.HasPrefix(key.Command, pfmBinary+" ")) {
			t.Fatalf("dropped seat retained pfm hook command %q:\n%s", key.Command, dropped)
		}
	}
	if !strings.Contains(dropped, "operator-keep") {
		t.Fatalf("dropped seat lost foreign hook:\n%s", dropped)
	}
	ownership, _, err := readSettingsHookOwnership(settingsHookOwnershipPath(managed))
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := ownership[physicalSettingsPath(filepath.Join(seatA, "settings.json"))]; exists {
		t.Fatalf("ownership ledger retained dropped seat: %#v", ownership)
	}
	machine := pfmconfig.Config{Accounts: []pfmconfig.Account{{ID: 2, ConfigDir: seatB}}}
	for _, result := range ProbeExpectedHooks(home, machine) {
		if result.State == stateDrift {
			t.Fatalf("ownership probe retained drift after seat removal: %#v", result)
		}
	}
}
