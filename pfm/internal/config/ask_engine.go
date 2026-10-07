package config

import (
	"fmt"
	"path/filepath"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func validateAskEngine(result Config, home string) error {
	counts := result.Engines()
	switch result.Ask.Engine {
	case pfmengine.Claude:
		if counts[pfmengine.Claude] == 0 {
			return fmt.Errorf(
				"config %s: ask.engine %q has zero Claude accounts; add an accounts entry or choose codex",
				result.Path,
				pfmengine.MustLookup(result.Ask.Engine).LongName,
			)
		}
	case pfmengine.Codex:
		if counts[pfmengine.Codex] == 0 {
			return fmt.Errorf(
				"config %s: ask.engine %q has zero Codex accounts; authenticate the default Codex home, add codex.homes, or choose claude",
				result.Path,
				pfmengine.MustLookup(result.Ask.Engine).LongName,
			)
		}
	case pfmengine.OpenCode:
		if counts[pfmengine.OpenCode] == 0 {
			descriptor := pfmengine.MustLookup(pfmengine.OpenCode)
			return fmt.Errorf(
				"config %s: ask.engine %q has zero %s accounts; create %s or choose another engine",
				result.Path,
				descriptor.LongName,
				descriptor.Short,
				filepath.Join(descriptor.DefaultRoots(home)[0], "opencode.db"),
			)
		}
	}
	return nil
}
