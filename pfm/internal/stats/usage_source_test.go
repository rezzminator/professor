package stats

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
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

// The Limits tab's requests go through the usage door carrying the picker's
// version: the sampler's Version reaches the User-Agent.
func TestLimitsSamplerRequestNamesThePfmVersion(t *testing.T) {
	home := t.TempDir()
	t.Setenv(paths.EnvHome, home)
	configDir := filepath.Join(home, "claude")
	writeFixtureCredentials(t, configDir)
	now := time.Unix(1_800_000_000, 0)
	agent := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		agent = request.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, usageJSONBody(51, 61, now))
	}))
	defer server.Close()
	sampler := NewLimitsSampler(
		[]LimitAccount{{ID: 11, Engine: pfmengine.Claude, Label: "account 11", ConfigDir: configDir}},
	)
	sampler.Now = func() time.Time { return now }
	sampler.Endpoint = server.URL
	sampler.Client = server.Client()
	sampler.Version = "7.7.7"
	if limits, _ := sampler.Sample(context.Background()); len(limits) != 1 || len(limits[0].Windows) != 2 {
		t.Fatalf("sample through the usage door: %#v", limits)
	}
	if agent != "pfm/7.7.7" {
		t.Fatalf("User-Agent = %q, want pfm/7.7.7", agent)
	}
}
