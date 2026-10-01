package testjail

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

var testProcessHome = paths.OSEnv{}.Get("HOME")

// GoBuild uses the same explicit build settings for standalone and shared test binaries.
func GoBuild(moduleDir, output, target string, flags ...string) error {
	args := append([]string{"build", "-trimpath", "-buildvcs=false"}, flags...)
	args = append(args, "-o", output, target)
	goBinary, err := deps.Resolve("go")
	if err != nil {
		return fmt.Errorf("resolve go for test build: %w", err)
	}
	result, err := (deps.RealRunner{}).Run(context.Background(), append([]string{goBinary}, args...), deps.RunOptions{
		Dir: moduleDir,
		Env: append(os.Environ(),
			"CGO_ENABLED=0",
			"GOFLAGS=",
			"GOTOOLCHAIN=local",
			"GOTELEMETRY=off",
			"HOME="+testProcessHome,
		),
	})
	if err != nil {
		return fmt.Errorf("go build %s: %w", target, err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("go build %s: exit status %d: %s%s", target, result.ExitCode, result.Stderr, result.Stdout)
	}
	return nil
}

// PFMBinary returns a validated run-wide binary or builds one for this process.
func PFMBinary(moduleDir, dir string) (string, error) {
	binary, set := paths.PrebuiltPFMBinary()
	return prebuiltOrBuild(moduleDir, dir, paths.EnvTestPFMBinary, binary, set, "pfm")
}

// MockEngineBinary returns a validated run-wide binary or builds one for this process.
func MockEngineBinary(moduleDir, dir string) (string, error) {
	binary, set := paths.PrebuiltMockEngineBinary()
	return prebuiltOrBuild(moduleDir, dir, paths.EnvTestMockEngineBinary, binary, set, "mock-engine")
}

// prebuiltOrBuild validates the binary env names, or builds ./cmd/{command} into dir.
func prebuiltOrBuild(moduleDir, dir, env, binary string, set bool, command string) (string, error) {
	if set {
		info, err := os.Stat(binary)
		if err != nil {
			return "", fmt.Errorf("%s=%q: %w", env, binary, err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			return "", fmt.Errorf("%s=%q: not an executable regular file", env, binary)
		}
		return binary, nil
	}
	built := filepath.Join(dir, command)
	if err := GoBuild(moduleDir, built, "./cmd/"+command); err != nil {
		return "", err
	}
	return built, nil
}
