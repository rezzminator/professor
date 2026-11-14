//go:build darwin

package usagehook

import (
	"context"
	"fmt"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// readKeychain uses the registry's absolute system command; pfm builds without cgo.
func readKeychain(ctx context.Context, service string) ([]byte, error) {
	binary, err := usageRunner.LookPath(deps.KeychainCommand)
	if err != nil {
		return nil, fmt.Errorf("resolve security: %w", err)
	}
	return runKeychain(ctx, binary, service)
}
