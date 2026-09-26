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

// A hook document that does not parse is an unchecked file, never a clean
// one: UnknownPFMHookCommands returns the decode error, and a clean document
// returns no names and no error.
func TestUnknownPFMHookCommandsNamesAnUnparsableDocument(t *testing.T) {
	t.Parallel()
	home := filepath.Join("neutral", "home")
	names, err := UnknownPFMHookCommands([]byte("{\"hooks\": "), home)
	if err == nil {
		t.Fatalf("UnknownPFMHookCommands(unparsable) = %v, nil; want the decode error", names)
	}
	names, err = UnknownPFMHookCommands([]byte("{\"hooks\": {}}"), home)
	if err != nil || len(names) != 0 {
		t.Fatalf("UnknownPFMHookCommands(clean) = %v, %v; want no names, no error", names, err)
	}
}
