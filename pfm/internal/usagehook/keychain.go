package usagehook

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"hostops/pfm/internal/deps"
)

// Only errSecItemNotFound means absence; cancellation and ACL failures do not.
const keychainNotFoundStatus = 44

// runKeychain bounds both the process and inherited output pipes so a locked
// keychain cannot stall a prompt hook or keep a cancelled sampler running.
func runKeychain(ctx context.Context, binary, service string) ([]byte, error) {
	return runKeychainWithRunner(ctx, binary, service, deps.RealRunner{})
}

func runKeychainWithRunner(ctx context.Context, binary, service string, runner deps.Runner) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, deps.ProbeTimeout)
	defer cancel()
	result, err := runner.Run(ctx, []string{binary, "find-generic-password", "-s", service, "-w"}, deps.RunOptions{
		WaitDelay: time.Second,
	})
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("security find-generic-password: %w", ctx.Err())
		}
		detail := strings.Join(strings.Fields(string(result.Stderr)), " ")
		return nil, fmt.Errorf("security find-generic-password: %w: %s", err, detail)
	}
	if result.ExitCode == keychainNotFoundStatus {
		return nil, os.ErrNotExist
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf(
			"security find-generic-password exited %d: %s",
			result.ExitCode,
			strings.TrimSpace(string(result.Stderr)),
		)
	}
	return bytes.TrimSpace(result.Stdout), nil
}
