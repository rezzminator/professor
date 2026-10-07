package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// AmbientClaudeConfigDir returns the CLAUDE_CONFIG_DIR the invoking shell has
// exported, cleaned, or "" when unset or blank. The launcher shim
// (cmd/pfm/internal_launch.go) and the Claude user-registry resolver
// (installer.ClaudeUserRegistries) both read this SAME env var through this
// one helper, by the same rule, so the two doors cannot drift out of sync.
func AmbientClaudeConfigDir() string {
	return AmbientClaudeConfigDirFrom(paths.OSEnv{})
}

// AmbientClaudeConfigDirFrom applies the same rule over an injected environment.
func AmbientClaudeConfigDirFrom(env paths.Env) string {
	value := strings.TrimSpace(env.Get("CLAUDE_CONFIG_DIR"))
	if value == "" {
		return ""
	}
	return filepath.Clean(value)
}

// ResolvePath resolves the machine file from an override or the clone marker.
func ResolvePath(home string) (string, error) { return ResolvePathFrom(paths.OSEnv{}, home) }

// ResolvePathFrom applies the same rule over an injected environment.
func ResolvePathFrom(env paths.Env, home string) (string, error) {
	if override := strings.TrimSpace(env.Get(paths.EnvConfig)); override != "" {
		if !filepath.IsAbs(override) {
			return "", fmt.Errorf("%s must be an absolute path: %q", paths.EnvConfig, override)
		}
		return filepath.Clean(override), nil
	}
	repo, err := paths.ReadSourceRepoMarker(home)
	if err != nil {
		return "", err
	}
	return filepath.Join(repo, FileName), nil
}

func configOverridden(env paths.Env) bool {
	return strings.TrimSpace(env.Get(paths.EnvConfig)) != ""
}

type sourceRepoMarkerError struct{ cause error }

func (err *sourceRepoMarkerError) Error() string { return NoConfigPathError(err.cause).Error() }
func (err *sourceRepoMarkerError) Unwrap() error { return err.cause }

func configMarkerError(markerErr error) error {
	if errors.Is(markerErr, paths.ErrNoSourceRepoMarker) {
		return nil
	}
	return &sourceRepoMarkerError{cause: markerErr}
}

// NoConfigPathError gives writers the remedy for a missing clone marker.
func NoConfigPathError(markerErr error) error {
	if markerErr == nil || errors.Is(markerErr, paths.ErrNoSourceRepoMarker) {
		return fmt.Errorf(
			"no config path: no source repo recorded — run pfm install from the clone, or set %s",
			paths.EnvConfig,
		)
	}
	return fmt.Errorf("no config path: %w — run pfm install from the clone, or set %s", markerErr, paths.EnvConfig)
}

// RefuseAmbientConfigHome prevents a test from reading this checkout through
// a source-repo marker unless the test pins PFM_CONFIG to its own file.
func RefuseAmbientConfigHome(home string) error {
	return RefuseAmbientConfigHomeFrom(paths.OSEnv{}, home)
}

// RefuseAmbientConfigHomeFrom applies the test guard to an injected environment.
func RefuseAmbientConfigHomeFrom(env paths.Env, home string) error {
	if !testing.Testing() || env.Get(paths.EnvRealHome) != "" {
		return nil
	}
	if configOverridden(env) {
		return nil
	}
	repo, err := paths.ReadSourceRepoMarker(home)
	if err != nil {
		return nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}
	for directory := cwd; directory != filepath.Dir(directory); directory = filepath.Dir(directory) {
		if paths.PhysicalPath(repo) == paths.PhysicalPath(directory) {
			return fmt.Errorf(
				"refusing ambient config in real clone %s inside a test: set %s to a jailed file",
				repo,
				paths.EnvConfig,
			)
		}
	}
	return nil
}
