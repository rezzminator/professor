package update

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/installer"
)

// TestUpdateRollbackResidueNamesTheStrandedHookCommands extends
// updateHookRollbackFixture's race guard (issue #24 finding 2): when the
// concurrent edit that forces residue itself carries a hook of pfm's own
// shape naming a subcommand this binary does not implement, the residue
// message names the stranded entry instead of only saying "reconcile it by
// hand" — the operator's repair instruction must be concrete.
func TestUpdateRollbackResidueNamesTheStrandedHookCommands(t *testing.T) {
	installer.SetImplementedSubcommands(nil, nil)
	var stranded []byte
	settings, _, stderr := updateHookRollbackFixture(t, func(settings string) {
		home := filepath.Dir(filepath.Dir(filepath.Dir(settings)))
		command := filepath.Join(home, ".local", "bin", "pfm") + " internal exit-intercept-vnext"
		stranded = []byte(
			"{\n  \"hooks\": {\"UserPromptSubmit\": [{\"hooks\": [{\"command\": \"" + command + "\"}]}]}\n}\n",
		)
		if err := os.WriteFile(settings, stranded, 0o600); err != nil {
			t.Fatal(err)
		}
	})
	if got, err := os.ReadFile(settings); err != nil || !bytes.Equal(got, stranded) {
		t.Fatalf("settings after rollback = %q, %v; want the concurrent edit kept %q", got, err, stranded)
	}
	if !strings.Contains(stderr, "it still carries") || !strings.Contains(stderr, "exit-intercept-vnext") {
		t.Fatalf("rollback residue did not name the stranded hook command: %q", stderr)
	}
}

// A hook file that changed after the update's install and no longer parses is
// named as unchecked, carrying its parse error — never read as a clean file.
func TestUpdateRollbackResidueNamesAnUnparsableHookFileAsUnchecked(t *testing.T) {
	broken := []byte("{\"hooks\": ")
	settings, _, stderr := updateHookRollbackFixture(t, func(settings string) {
		if err := os.WriteFile(settings, broken, 0o600); err != nil {
			t.Fatal(err)
		}
	})
	if got, err := os.ReadFile(settings); err != nil || !bytes.Equal(got, broken) {
		t.Fatalf("settings after rollback = %q, %v; want the concurrent edit kept %q", got, err, broken)
	}
	physical, err := filepath.EvalSymlinks(settings)
	if err != nil {
		t.Fatal(err)
	}
	want := "hook file " + physical + " changed after the update's install wrote it; left as is — it does not parse ("
	if !strings.Contains(stderr, want) ||
		!strings.Contains(stderr, "), so pfm could not check it for stranded pfm hooks; reconcile it by hand") {
		t.Fatalf("rollback residue did not name the unparsable hook file as unchecked: %q", stderr)
	}
}

// A hook file removed after the update's install is named as removed — an
// absent file has nothing to parse, so it is never reported as unparsable.
func TestUpdateRollbackResidueNamesARemovedHookFile(t *testing.T) {
	var physical string
	_, _, stderr := updateHookRollbackFixture(t, func(settings string) {
		resolved, err := filepath.EvalSymlinks(settings)
		if err != nil {
			t.Fatal(err)
		}
		physical = resolved
		if err := os.Remove(physical); err != nil {
			t.Fatal(err)
		}
	})
	if _, err := os.Lstat(physical); !os.IsNotExist(err) {
		t.Fatalf("rollback recreated the removed hook file %s: %v", physical, err)
	}
	want := "hook file " + physical + " was removed after the update's install wrote it; reconcile it by hand"
	if !strings.Contains(stderr, want) || strings.Contains(stderr, "does not parse") {
		t.Fatalf("rollback residue did not name the removed hook file as removed: %q", stderr)
	}
}
