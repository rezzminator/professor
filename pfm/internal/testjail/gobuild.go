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
	if binary, set := paths.PrebuiltPFMBinary(); set {
		info, err := os.Stat(binary)
		if err != nil {
			return "", fmt.Errorf("%s=%q: %w", paths.EnvTestPFMBinary, binary, err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			return "", fmt.Errorf("%s=%q: not an executable regular file", paths.EnvTestPFMBinary, binary)
		}
		return binary, nil
	}
	binary := filepath.Join(dir, "pfm")
	if err := GoBuild(moduleDir, binary, "./cmd/pfm"); err != nil {
		return "", err
	}
	return binary, nil
}

// MockEngineBinary returns a validated run-wide binary or builds one for this process.
func MockEngineBinary(moduleDir, dir string) (string, error) {
	if binary, set := paths.PrebuiltMockEngineBinary(); set {
		info, err := os.Stat(binary)
		if err != nil {
			return "", fmt.Errorf("%s=%q: %w", paths.EnvTestMockEngineBinary, binary, err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			return "", fmt.Errorf("%s=%q: not an executable regular file", paths.EnvTestMockEngineBinary, binary)
		}
		return binary, nil
	}
	binary := filepath.Join(dir, "mock-engine")
	if err := GoBuild(moduleDir, binary, "./cmd/mock-engine"); err != nil {
		return "", err
	}
	return binary, nil
}
