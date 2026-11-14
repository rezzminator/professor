package installer

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestHostSettingsProbe(t *testing.T) {
	home := t.TempDir()
	if ManagedRoot(home) != managedRootForHome(home) {
		t.Fatal("managed root differs")
	}
	path := filepath.Join(home, "physical-settings.json")
	raw, owned := accountSettingsFixture(home)
	writeFixture(t, path, string(raw))
	link := filepath.Join(home, "settings.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	ledger := settingsHookOwnershipPath(ManagedRoot(home))
	encoded, err := encodeSettingsHookOwnership(map[string]settingsHookCounts{physicalSettingsPath(link): owned})
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, ledger, string(encoded))
	names, err := PFMSettingsLeftovers(fixtureRetiredHooks, home, link)
	if err != nil ||
		!reflect.DeepEqual(
			names,
			[]string{
				fmt.Sprintf("hook %q", home+"/.local/bin/pfm internal launcher-repair"),
				`hook "pfm internal clear-hide"`,
				"statusLine",
				"subagentStatusLine",
			},
		) {
		t.Fatalf("names=%v err=%v", names, err)
	}
	// A ledger record does not claim a command running the operator's binary.
	writeFixture(
		t,
		path,
		`{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"`+home+`/private-ledger-hook"}]}]}}`,
	)
	names, err = PFMSettingsLeftovers(fixtureRetiredHooks, home, link)
	if err != nil || len(names) != 0 {
		t.Fatalf("ledger names=%v err=%v", names, err)
	}
	writeFixture(t, ledger, "{")
	_, err = PFMSettingsLeftovers(fixtureRetiredHooks, home, link)
	assertProbePath(t, err, ledger)
}

func assertProbePath(t *testing.T, err error, path string) {
	t.Helper()
	var pathErr *fs.PathError
	if !errors.As(err, &pathErr) || pathErr.Path != path {
		t.Fatalf("error=%v, want path %s", err, path)
	}
}

func TestHostProbesAbsenceAndUnreadable(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "missing")
	if _, err := PFMSettingsLeftovers(fixtureRetiredHooks, home, path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(home, "settings.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := PFMSettingsLeftovers(fixtureRetiredHooks, home, filepath.Join(home, "settings.json"))
	assertProbePath(t, err, filepath.Join(home, "settings.json"))
}
