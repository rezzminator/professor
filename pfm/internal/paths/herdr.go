package paths

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// HerdrShellWrapper is the executable Herdr runs for a new interactive pane.
func HerdrShellWrapper(home string) string {
	return filepath.Join(home, ".local", "share", "pfm", "install", "bin", "pfm-herdr-shell")
}

// HerdrConfig resolves the same config for shell wiring and theme sync. A
// debug config is ambiguous unless the operator identifies the server's file.
func HerdrConfig(home string, env Env) (string, error) {
	if path, set := env.Lookup("HERDR_CONFIG_PATH"); set {
		if path == "" {
			return "", errors.New("HERDR_CONFIG_PATH is empty")
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return path, fmt.Errorf("resolve Herdr config %s: %w", path, err)
		}
		return absolute, nil
	}
	directory, set := env.Lookup("XDG_CONFIG_HOME")
	if !set || directory == "" {
		directory = filepath.Join(home, ".config")
	}
	debug := filepath.Join(directory, "herdr-dev", "config.toml")
	if _, err := os.Stat(debug); err == nil {
		return debug, fmt.Errorf(
			"debug Herdr config exists at %s; set HERDR_CONFIG_PATH to identify the server's config",
			debug,
		)
	} else if !errors.Is(err, os.ErrNotExist) {
		return debug, fmt.Errorf("inspect debug Herdr config %s: %w", debug, err)
	}
	path := filepath.Join(directory, "herdr", "config.toml")
	absolute, err := filepath.Abs(path)
	if err != nil {
		return path, fmt.Errorf("resolve Herdr config %s: %w", path, err)
	}
	return absolute, nil
}
