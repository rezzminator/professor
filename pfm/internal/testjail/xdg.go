package testjail

import (
	"fmt"
	"os"
	"path/filepath"
)

// UnsetXDGConfigHome, set by a TestMain before Run, lets the installer resolve
// rumdl and VS Code config against each test's own Home. Child tools then read
// $HOME/.config, so HOME moves into the package jail with it (the Go
// directories are already pinned outside it); Git remains pinned to
// GIT_CONFIG_GLOBAL=/dev/null by Run.
var UnsetXDGConfigHome bool

func pinXDGConfigHome(home string) error {
	if UnsetXDGConfigHome {
		if err := os.Unsetenv("XDG_CONFIG_HOME"); err != nil {
			return fmt.Errorf("unset XDG_CONFIG_HOME: %w", err)
		}
		if err := os.Setenv("HOME", home); err != nil {
			return fmt.Errorf("set HOME to %s: %w", home, err)
		}
		return nil
	}
	if err := os.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config")); err != nil {
		return fmt.Errorf("set XDG_CONFIG_HOME under %s: %w", home, err)
	}
	return nil
}
