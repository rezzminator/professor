package gather

import (
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func TestUnknownEngineIsANamedError(t *testing.T) {
	_, err := MatcherFor(pfmengine.ID("zz"))
	if err == nil || err.Error() != "engine zz: no process matcher registered" {
		t.Fatalf("MatcherFor(zz) error = %v", err)
	}
}
