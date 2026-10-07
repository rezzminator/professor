package hostfixture

import (
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
