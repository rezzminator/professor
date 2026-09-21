package stats

import (
	"context"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

type builtinTestUsageSource struct{ id pfmengine.ID }

func (source builtinTestUsageSource) Fetch(ctx context.Context, account LimitAccount) (AccountLimits, error) {
	switch source.id {
	case pfmengine.Claude:
		return FetchClaude(ctx, account)
	case pfmengine.Codex:
		return FetchCodex(ctx, account)
	default:
		return AccountLimits{}, nil
	}
}

func init() {
	RegisterUsageSource(pfmengine.Claude, builtinTestUsageSource{id: pfmengine.Claude})
	RegisterUsageSource(pfmengine.Codex, builtinTestUsageSource{id: pfmengine.Codex})
}

func TestUnknownEngineIsANamedError(t *testing.T) {
	_, err := UsageSourceFor(pfmengine.ID("zz"))
	if err == nil || err.Error() != "engine zz: no usage source registered" {
		t.Fatalf("UsageSourceFor(zz) error = %v", err)
	}
}
