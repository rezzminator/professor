package installer

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/mockengine"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestMain(m *testing.M) {
	if os.Getenv("MOCK_ENGINE_HELPER") == "1" {
		os.Exit(
			mockengine.Main(context.Background(), os.Args[0], os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv),
		)
	}
	testjail.UnsetXDGConfigHome = true
	os.Exit(testjail.Run(m))
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
