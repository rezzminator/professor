package hookentry

import (
	"errors"
	"fmt"
	"io"
	"os"

	"hostops/pfm/internal/config"
	"hostops/pfm/internal/installer"
)

// ClaudeLaunch resolves the real Claude binary before entering the managed
// launcher, keeping binary discovery out of the installed launcher shim.
func ClaudeLaunch(args []string, stdout, stderr io.Writer, runtime config.Runtime) int {
	binary, err := installer.ResolveClaudeBinary(
		runtime.Paths.Home,
		runtime.Config.Claude.Binary,
		os.Getenv("PATH"),
	)
	if errors.Is(err, installer.ErrClaudeBinaryNotFound) {
		fmt.Fprintln(stderr, "pfm claude launcher: no real Claude binary found")
		return 127
	}
	if err != nil {
		fmt.Fprintf(stderr, "pfm claude launcher: resolve real Claude binary: %v\n", err)
		return 1
	}

	launchArgs := make([]string, 0, len(args)+3)
	launchArgs = append(launchArgs, "--real", binary, "--")
	launchArgs = append(launchArgs, args...)
	return Launch(launchArgs, stdout, stderr, runtime)
}
