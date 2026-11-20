package installer

import (
	"errors"
	"fmt"
	"os/exec"
	"reflect"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/themesync"
)

// pfm's keymap block (herdr_keymap.go renders it) owns the whole [keys]
// table, so a config holds it or a hand-written [keys] table, never both (TOML
// refuses a second [keys]).
const (
	herdrKeysOpen  = "# >>> pfm herdr keys >>>\n"
	herdrKeysClose = "# <<< pfm herdr keys <<<\n"
)

// wireHerdrKeys owns pfm's keymap block in the Herdr config, rendered from
// herdr.keys over pfm's keymap. A hand-written [keys] table that already says
// exactly what the block says is adopted into it; any other stays the user's,
// its missing mappings named.
func (installer *engine) wireHerdrKeys(uninstall bool) (result error) {
	if !uninstall {
		if absent, err := installer.herdrAbsent(); err != nil || absent {
			if absent {
				installer.skip("herdr keys: herdr absent")
			}
			return err
		}
	}
	home := installer.options.Home
	if installer.apply {
		release, err := themesync.LockState(home)
		if err != nil {
			return fmt.Errorf("lock Herdr keys config: %w", err)
		}
		defer func() { result = errors.Join(result, release()) }()
	}
	status, err := InspectHerdrKeys(home, installer.env(), installer.options.HerdrKeys)
	switch {
	case err != nil && (!uninstall || status.Path == ""):
		return err
	case err != nil:
		// Uninstall finds pfm's block by its markers alone, so a config
		// TOML refuses still loses the block and the rest of the teardown runs.
		installer.say("  note  herdr keys: %v; removing pfm's block by its markers", err)
	}
	raw, existed, err := readCodexConfig(status.Path)
	if err != nil {
		return fmt.Errorf("read Herdr keys config: %w", err)
	}
	original := string(raw)
	write := func(verb, updated string) error {
		return installer.change(verb+" "+status.Path, func() error {
			return installer.writeHerdrConfig(status.Path, raw, []byte(updated), existed)
		})
	}
	start, end, block := herdrKeysBlockRange(original)
	switch {
	case uninstall && block:
		return write("unwire herdr keys in", original[:start]+original[end:])
	case uninstall:
		return nil
	case status.Owned():
		installer.ok("herdr keys: wired " + status.Path)
		return nil
	case block && !status.HandWritten:
		return write("update herdr keys in", original[:start]+status.Want+original[end:])
	case status.HandWritten:
		return installer.adoptHerdrKeys(status, original, write)
	}
	updated := original + themesync.HerdrAppendSeparator(original) + status.Want
	if err := herdrKeysOnlyAdded(original, updated); err != nil {
		return fmt.Errorf("add pfm's keys block to %s: %w", status.Path, err)
	}
	return write("wire herdr keys in", updated)
}

// adoptHerdrKeys moves a hand-written keys table into pfm's block when the
// config reads the same afterwards, and otherwise leaves it alone.
func (installer *engine) adoptHerdrKeys(
	status HerdrKeysStatus,
	original string,
	write func(verb, updated string) error,
) error {
	start, end, found, err := themesync.HerdrTableSpan(original, "keys")
	if err == nil && found && !status.Block {
		candidate := original[:start] + status.Want + original[end:]
		before, beforeErr := decodeHerdrConfig(original)
		after, afterErr := decodeHerdrConfig(candidate)
		if beforeErr == nil && afterErr == nil && reflect.DeepEqual(before, after) {
			return write("adopt hand-written [keys] into pfm's block in", candidate)
		}
	}
	if len(status.Missing) == 0 {
		installer.ok("herdr keys: hand-written [keys] in " + status.Path + " binds every pfm mapping; left alone")
		return nil
	}
	installer.skip("herdr keys: hand-written [keys] in " + status.Path + " left alone; it lacks " +
		strings.Join(status.Missing, ", "))
	return nil
}

// herdrKeysOnlyAdded proves appending the block changed nothing but the keys
// table: no other table's keys fell under the new header.
func herdrKeysOnlyAdded(original, updated string) error {
	before, err := decodeHerdrConfig(original)
	if err != nil {
		return err
	}
	after, err := decodeHerdrConfig(updated)
	if err != nil {
		return err
	}
	delete(after, "keys")
	if !reflect.DeepEqual(before, after) {
		return errors.New("the appended [keys] table would capture existing settings")
	}
	return nil
}

// herdrAbsent reports whether Herdr is not installed: the default herdr
// binary not on PATH. A configured binary that does not resolve, or a lookup
// that failed otherwise, is an error, never absence.
func (installer *engine) herdrAbsent() (bool, error) {
	binary := installer.env().Get(herdr.EnvBin)
	if binary == "" {
		binary = "herdr"
	}
	_, err := installer.processRunner().LookPath(binary)
	if errors.Is(err, exec.ErrNotFound) && binary == "herdr" {
		return true, nil
	} else if err != nil {
		return false, fmt.Errorf("resolve Herdr binary %s: %w", binary, err)
	}
	return false, nil
}
