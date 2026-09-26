package installer

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestMain(m *testing.M) { os.Exit(testjail.Run(m)) }

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
