package installer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/mockengine"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestMain(m *testing.M) {
	if os.Getenv("MOCK_ENGINE_HELPER") == "1" {
		os.Exit(
			mockengine.Main(context.Background(), os.Args[0], os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv),
		)
	}
	// Install convergence must not discover or signal another suite's painter.
	procRoot, err := os.MkdirTemp("", "installer-proc-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "installer process jail: %v\n", err)
		os.Exit(1)
	}
	code := 1
	defaultProcRoot = procRoot
	if err := os.Setenv(paths.EnvProcRoot, procRoot); err != nil {
		fmt.Fprintf(os.Stderr, "installer process jail environment: %v\n", err)
	} else {
		testjail.UnsetXDGConfigHome = true
		code = testjail.Run(m)
	}
	if err := os.RemoveAll(procRoot); err != nil {
		fmt.Fprintf(os.Stderr, "remove installer process jail: %v\n", err)
		code = 1
	}
	os.Exit(code)
}

var testConfigPaths sync.Map

func testConfigPath(t *testing.T) string {
	t.Helper()
	if path, ok := testConfigPaths.Load(t); ok {
		return path.(string)
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	actual, _ := testConfigPaths.LoadOrStore(t, path)
	t.Cleanup(func() { testConfigPaths.Delete(t) })
	return actual.(string)
}

// emptyProcRoot makes {home}/proc: gather.NewProcFS reads the host's /proc when a root is missing.
func emptyProcRoot(t *testing.T, home string) string {
	t.Helper()
	root := filepath.Join(home, "proc")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	return root
}
