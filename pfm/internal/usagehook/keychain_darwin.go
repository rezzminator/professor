//go:build darwin

package usagehook

import (
	"context"
	"fmt"
)

// readKeychain uses the registry's absolute system command; pfm builds without cgo.
func readKeychain(ctx context.Context, service string) ([]byte, error) {
	binary, err := usageRunner.LookPath("security")
	if err != nil {
		return nil, fmt.Errorf("resolve security: %w", err)
	}
	return runKeychain(ctx, binary, service)
}
