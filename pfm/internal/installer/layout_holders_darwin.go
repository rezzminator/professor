//go:build darwin

package installer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

const lsofHolderTimeout = 10 * time.Second

func dbHolderPIDs(procRoot, db string) ([]string, error) {
	if info, err := os.Stat(procRoot); err == nil && info.IsDir() && filepath.Clean(procRoot) != "/proc" {
		return procFDHolders(procRoot, db)
	}
	return lsofHolderPIDs(db)
}

func lsofHolderPIDs(db string) ([]string, error) {
	targets, err := lsofHolderTargets(db)
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return nil, nil
	}
	path, err := deps.Resolve("lsof")
	if err != nil {
		return nil, fmt.Errorf("database holder probe: lsof unavailable: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), lsofHolderTimeout)
	defer cancel()
	argv := append([]string{path, "-t", "--"}, targets...)
	result, err := obs.Runner(deps.RealRunner{}).Run(ctx, argv, deps.RunOptions{})
	if err != nil {
		return nil, fmt.Errorf("database holder probe: lsof: %w", err)
	}
	return parseLsofHolders(string(result.Stdout), string(result.Stderr), result.ExitCode)
}
