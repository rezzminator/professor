package testjail

import (
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
)

// Where a profiled process sits: its launch directory, the module root above it and the label its
// process directory carries.

// launchDir is the working directory this process started in, read before any TestMain runs: go
// test starts a package's binary in its package directory, and RunInJail later moves the process
// into the jail, where no go.mod lies above. "" when it could not be read.
var launchDir, _ = os.Getwd()

// moduleLocation is the launch directory and the directory of the nearest go.mod
// at or above it; each is "" when unknown (a go test binary starts in its
// package directory, so inside this module both are known).
func moduleLocation() (wd, root string) {
	if launchDir == "" {
		return "", ""
	}
	return launchDir, moduleRootOf(launchDir)
}

// ModuleRoot is the module root above the directory this test process started in, "" when there
// is none: a test that builds from the module (go build ./cmd/pfm) asks here, because RunInJail
// moves the process into the jail, where a path relative to the package directory no longer holds.
func ModuleRoot() string {
	_, root := moduleLocation()
	return root
}

// moduleRootOf is the nearest directory at or above dir holding a go.mod, or
// "" when there is none.
func moduleRootOf(dir string) string {
	for ; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		if filepath.Dir(dir) == dir {
			return ""
		}
	}
}

// buildInfoPath is the import path the toolchain recorded in this binary
// ("pkg.test" for a test binary), "" when it recorded none.
func buildInfoPath() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		return info.Path
	}
	return ""
}

// packageLabel names this process by its package directory relative to the
// module root, slashes flattened: internal/pricing → internal_pricing. With no
// module above the working directory, or the working directory at the module
// root, the binary's import path stands in with the ".test" suffix and this
// module's path dropped; a path outside this module yields the binary's own
// name minus ".test".
func packageLabel(wd, root, importPath, arg0 string) string {
	if root != "" {
		if rel, err := filepath.Rel(root, wd); err == nil && rel != "." {
			return strings.ReplaceAll(rel, string(filepath.Separator), "_")
		}
	}
	prefix := modulePrefix()
	if rest, ok := strings.CutPrefix(
		strings.TrimSuffix(importPath, ".test"),
		prefix,
	); ok && prefix != "" &&
		rest != "" {
		return strings.ReplaceAll(rest, "/", "_")
	}
	return strings.TrimSuffix(filepath.Base(arg0), ".test")
}
