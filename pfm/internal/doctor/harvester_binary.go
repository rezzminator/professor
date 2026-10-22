package doctor

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
)

func printHarvesterDoctor(
	ctx context.Context,
	stdout io.Writer,
	runtime config.Runtime,
	skipConversion bool,
	runner deps.Runner,
) int {
	if !config.MCPServerEnabled(runtime.Config, config.MCPServerHarvester) {
		return 0
	}
	binary := filepath.Join(runtime.Paths.Home, ".local", "bin", "harvester")
	fmt.Fprintln(stdout, "doctor: harvester")
	info, err := os.Stat(binary)
	if err != nil {
		fmt.Fprintf(stdout, "doctor: harvester binary=%s unavailable error=%v\n", binary, err)
		return 1
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		fmt.Fprintf(stdout, "doctor: harvester binary=%s not executable\n", binary)
		return 1
	}
	argv := []string{binary, "doctor"}
	if skipConversion {
		argv = append(argv, "--skip-conversion")
	}
	result, err := runner.Run(ctx, argv, deps.RunOptions{})
	fmt.Fprint(stdout, string(result.Stdout))
	fmt.Fprint(stdout, string(result.Stderr))
	if err != nil {
		fmt.Fprintf(stdout, "doctor: harvester binary=%s failed error=%v\n", binary, err)
		return 1
	}
	if result.ExitCode != 0 {
		fmt.Fprintf(stdout, "doctor: harvester binary=%s failed exit=%d\n", binary, result.ExitCode)
		return 1
	}
	return 0
}
