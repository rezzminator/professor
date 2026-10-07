package installer

import (
	"path/filepath"
	"testing"
)

func TestUnknownPFMHookCommandNeverReportsUnknownWithAnUnsetRegistry(t *testing.T) {
	implementedSubcommands = subcommandRegistry{}
	binary := filepath.Join(t.TempDir(), ".local", "bin", "pfm")
	if name, unknown := unknownPFMHookCommand(
		binary+" internal future-hook",
		binary,
	); unknown {
		t.Fatalf("unset command registry judged %q unknown", name)
	}
}
