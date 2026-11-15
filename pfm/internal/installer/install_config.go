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

// InstallConfig loads the target config or plans a first install from the clone's example.
// It returns the seed path without writing the target.
func InstallConfig(
	runtime pfmconfig.Runtime,
	_ paths.Env,
	installClone string,
) (config pfmconfig.Config, seeded string, err error) {
	values, target := runtime.Paths, runtime.Config.Path
	if target == "" {
		// A first install has no clone marker yet, so the runtime knows no config path; the
		// clone it installs from names the one the marker will point at.
		clone, err := installCloneDir(values.Home, installClone)
		if err != nil || clone == "" {
			return runtime.Config, "", err
		}
		target = filepath.Join(clone, pfmconfig.FileName)
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
	clone, err := installCloneDir(values.Home, installClone)
	if err != nil {
		return config, "", err
	}
	if clone == "" {
		return runtime.Config, "", nil
	}
	example := filepath.Join(clone, "example.pfm.config.json")
	_, readErr := os.ReadFile(example)
	if errors.Is(readErr, fs.ErrNotExist) {
		return runtime.Config, "", nil
	}
	if readErr != nil {
		return config, "", fmt.Errorf("read install example %s: %w", example, readErr)
	}
	config, err = pfmconfig.LoadSeed(
		example,
		target,
		values.Home,
		values.Roots[pfmengine.Claude],
		values.FirstRoot(pfmengine.Codex),
	)
	if err != nil {
		return config, example, err
	}
	config.SeedContent, err = seedWithoutDefaultNewKeys(config)
	return config, example, err
}

// installCloneDir is the clone a config seeds from: the source-repo marker's, else, before the
// first install writes the marker, the clone the install runs from; empty when neither is known.
func installCloneDir(home, installClone string) (string, error) {
	clone, markerErr := paths.ReadSourceRepoMarker(home)
	if markerErr != nil && !errors.Is(markerErr, paths.ErrNoSourceRepoMarker) {
		return "", fmt.Errorf("read source repo marker: %w", markerErr)
	}
	if !errors.Is(markerErr, paths.ErrNoSourceRepoMarker) || installClone == "" {
		return clone, nil
	}
	absolute, err := filepath.Abs(installClone)
	if err != nil {
		return "", fmt.Errorf("resolve install clone %q: %w", installClone, err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve install clone %q: %w", installClone, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("resolve install clone %q: %w", installClone, errors.New("not a directory"))
	}
	return paths.PhysicalPath(absolute), nil
}

func (installer *engine) seedConfig() error {
	if installer.options.ConfigSeed == "" {
		return nil
	}
	example, target := installer.options.ConfigSeed, installer.options.MCPConfigPath
	return installer.change("seed "+target+" from "+example, func() error {
		content := installer.options.ConfigSeedContent
		if content == nil {
			return fmt.Errorf("seed install config %s: validated example bytes are missing", target)
		}
		if err := atomicfile.Create(target, content, 0o600); err != nil {
			return fmt.Errorf("write install config %s: %w", target, err)
		}
		return nil
	})
}
