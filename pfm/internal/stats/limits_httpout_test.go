package stats

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

type failingLimitsTransport struct{}

func (failingLimitsTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("fixture network unavailable")
}

func TestLimitsSamplerOfflineRequestsWarnAndKeepStatus(t *testing.T) {
	for _, engine := range []pfmengine.ID{pfmengine.Claude, pfmengine.Codex} {
		t.Run(string(engine), func(t *testing.T) {
			root := t.TempDir()
			account := LimitAccount{ID: 1, Engine: engine, Label: "fixture"}
			if engine == pfmengine.Claude {
				account.ConfigDir = filepath.Join(root, ".claude")
				if err := os.MkdirAll(account.ConfigDir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(account.ConfigDir, ".credentials.json"),
					[]byte(`{"claudeAiOauth":{"accessToken":"fixture-token"}}`), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				account.CodexAuthPath = writeCodexAuth(t, root, "fixture-token", "fixture-account")
			}
			sampler := NewLimitsSampler([]LimitAccount{account})
			sampler.Env = &paths.MapEnv{Values: map[string]string{paths.EnvHome: root}}
			sampler.Endpoint = "https://fixture.invalid/usage"
			sampler.CodexEndpoint = sampler.Endpoint
			client := &http.Client{Transport: failingLimitsTransport{}}
			sampler.Client, sampler.CodexClient = client, client
			_, recorder := obs.Test(t)
			limits, _ := sampler.Sample(context.Background())
			if len(limits) != 1 || !strings.Contains(limits[0].Status, "fixture network unavailable") {
				t.Fatalf("offline status = %#v", limits)
			}
			var count int
			for _, record := range recorder.Records() {
				if record.Message == "http.out.request" {
					count++
					if record.Level != "WARN" {
						t.Fatalf("offline request level = %s, want WARN", record.Level)
					}
				}
			}
			if count != 1 {
				t.Fatalf("http.out.request count = %d, want 1: %s", count, recorder.Raw())
			}
		})
	}
}

// TestLimitsSamplerClientsWriteHTTPOutRecords pins the two http.out doors of
// the limits sampler (spec § Middleware): the Claude usage client and the
// Codex usage client each leave one comp=http.out record per request, the
// Codex client keeps its no-redirect policy, and an injected client is wrapped
// in place rather than replaced.
func TestLimitsSamplerClientsWriteHTTPOutRecords(t *testing.T) {
	_, recorder := obs.Test(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	sampler := NewLimitsSampler(nil)
	for _, client := range []*http.Client{sampler.client(), sampler.codexClient()} {
		response, err := client.Get(server.URL + "/usage?key=LIMITSECRET")
		if err != nil {
			t.Fatal(err)
		}
		if err := response.Body.Close(); err != nil {
			t.Fatal(err)
		}
	}
	records := recorder.Records()
	if len(records) != 2 {
		t.Fatalf("records = %d, want one http.out record per client: %s", len(records), recorder.Raw())
	}
	for _, record := range records {
		for key, want := range map[string]any{
			obs.FieldComp: "http.out", "op": "request", "path": "/usage", "status": float64(http.StatusNoContent),
		} {
			if got, _ := record.Field(key); got != want {
				t.Fatalf("http.out record %s = %v, want %v: %v", key, got, want, record.Fields)
			}
		}
	}
	if strings.Contains(recorder.Raw(), "LIMITSECRET") {
		t.Fatalf("the query string reached the activity log: %s", recorder.Raw())
	}
	if err := sampler.codexClient().CheckRedirect(nil, nil); err != http.ErrUseLastResponse {
		t.Fatalf("the Codex client's redirect policy changed: %v", err)
	}
	injected := &http.Client{}
	sampler.Client = injected
	if sampler.client() != injected {
		t.Fatal("an injected client was replaced instead of wrapped in place")
	}
	if _, wrapped := injected.Transport.(interface {
		RoundTrip(*http.Request) (*http.Response, error)
	}); !wrapped {
		t.Fatal("the injected client's transport was not wrapped")
	}
}
