package update

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hostops/pfm/internal/installer"
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
