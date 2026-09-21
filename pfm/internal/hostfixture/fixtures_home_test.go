package hostfixture

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestNoHomeMakesPathsHomeRefuse(t *testing.T) {
	base := NoHome(t)

	if _, err := paths.Home(); err == nil {
		t.Fatal("paths.Home() succeeded after NoHome, want a refusal error")
	}
	if _, err := paths.HomeFrom(base.Env); err == nil {
		t.Fatal("paths.HomeFrom(base.Env) succeeded after NoHome, want a refusal error")
	}
}

func TestReadOnlyHomeRefusesAWriteInsideIt(t *testing.T) {
	if isRoot() {
		t.Skip("hostfixture: running as root — read-only home is not enforceable")
	}
	base := ReadOnlyHome(t)

	target := filepath.Join(base.Values.Home, "probe.txt")
	err := os.WriteFile(target, []byte("x"), 0o600)
	if err == nil {
		t.Fatal("write inside ReadOnlyHome succeeded, want a permission error")
	}
	if !os.IsPermission(err) {
		t.Fatalf("write error = %v, want a permission error", err)
	}
}
