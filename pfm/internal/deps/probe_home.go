package deps

import (
	"fmt"
	"os"
	"strings"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// EngineProbeHome is one fresh, throwaway engine home a probe runs the engine
// in. A probe asks the binary a question (its version, its self-doctor); it
// never acts for an account, so it must never run in one: a logged-out engine
// started with an ambient env writes its first-run state into the shared
// store and $HOME, and the host check then blocks the machine.
type EngineProbeHome struct {
	// variable is the engine's home variable, read from the engine registry
	// (engine.Descriptor.HomeEnv), never spelled here.
	variable string
	// Dir is the 0700 directory under pfm's SID dir the variable points at.
	Dir string
}

// NewEngineProbeHome creates a fresh 0700 throwaway home for engine id under
// pfm's own SID dir. An error means the probe could not look: the caller runs
// nothing and reports the error, never ok and never absent.
func NewEngineProbeHome(id pfmengine.ID) (EngineProbeHome, error) {
	descriptor, err := pfmengine.Lookup(id)
	if err != nil {
		return EngineProbeHome{}, fmt.Errorf("could not look: no probe home for engine %q: %w", id, err)
	}
	if descriptor.HomeEnv == "" {
		return EngineProbeHome{}, fmt.Errorf("could not look: engine %s names no home variable to isolate", id)
	}
	root := paths.SIDDirFrom(paths.OSEnv{})
	if err := os.MkdirAll(root, 0o700); err != nil {
		return EngineProbeHome{}, fmt.Errorf("could not look: create the probe home base %s: %w", root, err)
	}
	dir, err := os.MkdirTemp(root, paths.SIDEngineProbeHomePrefix)
	if err != nil {
		return EngineProbeHome{}, fmt.Errorf(
			"could not look: create a throwaway %s under %s: %w", descriptor.HomeEnv, root, err)
	}
	return EngineProbeHome{variable: descriptor.HomeEnv, Dir: dir}, nil
}

// Env returns base with every entry of the engine's home variable dropped and
// the variable pointed at the throwaway home, so no inherited value wins.
func (home EngineProbeHome) Env(base []string) []string {
	environment := make([]string, 0, len(base)+1)
	for _, entry := range base {
		if name, _, _ := strings.Cut(entry, "="); name != home.variable {
			environment = append(environment, entry)
		}
	}
	return append(environment, home.variable+"="+home.Dir)
}

// Remove deletes the throwaway home and everything the probe wrote into it.
func (home EngineProbeHome) Remove() error {
	if err := os.RemoveAll(home.Dir); err != nil {
		return fmt.Errorf("remove the throwaway %s %s: %w", home.variable, home.Dir, err)
	}
	return nil
}
