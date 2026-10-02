package installer

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// InstallConfig loads the target config or seeds a first install from the clone's example.
// A preview loads the seeded values without writing the target.
func InstallConfig(
	runtime pfmconfig.Runtime,
	_ paths.Env,
	installClone string,
	apply bool,
) (config pfmconfig.Config, seeded string, err error) {
	values, target := runtime.Paths, runtime.Config.Path
	if target == "" {
		return runtime.Config, "", nil
	}
	load := func() (pfmconfig.Config, error) {
		loaded, loadErr := pfmconfig.Load(
			target,
			values.Home,
			values.Roots[pfmengine.Claude],
			values.FirstRoot(pfmengine.Codex),
		)
		if loadErr != nil {
			return pfmconfig.Config{}, fmt.Errorf("load install config %s: %w", target, loadErr)
		}
		return loaded, nil
	}
	if _, statErr := os.Stat(target); statErr == nil {
		config, err = load()
		return config, "", err
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return config, "", fmt.Errorf("inspect install config %s: %w", target, statErr)
	}
	if runtime.ConfigExplicit {
		return runtime.Config, "", nil
	}
	clone, markerErr := paths.ReadSourceRepoMarker(values.Home)
	if markerErr != nil && !errors.Is(markerErr, paths.ErrNoSourceRepoMarker) {
		return config, "", fmt.Errorf("read source repo marker: %w", markerErr)
	}
	if errors.Is(markerErr, paths.ErrNoSourceRepoMarker) && installClone != "" {
		absolute, absErr := filepath.Abs(installClone)
		if absErr != nil {
			return config, "", fmt.Errorf("resolve install clone %q: %w", installClone, absErr)
		}
		clone = paths.PhysicalPath(absolute)
	}
	if clone == "" {
		return runtime.Config, "", nil
	}
	example := filepath.Join(clone, "example.pfm.config.json")
	content, readErr := os.ReadFile(example)
	if errors.Is(readErr, fs.ErrNotExist) {
		return runtime.Config, "", nil
	}
	if readErr != nil {
		return config, "", fmt.Errorf("read install example %s: %w", example, readErr)
	}
	if !apply {
		config, err = pfmconfig.LoadSeed(
			example,
			target,
			values.Home,
			values.Roots[pfmengine.Claude],
			values.FirstRoot(pfmengine.Codex),
		)
		return config, "", err
	}
	if err := atomicfile.Write(target, content, 0o600); err != nil {
		return config, "", fmt.Errorf("write install config %s: %w", target, err)
	}
	config, err = load()
	return config, example, err
}
