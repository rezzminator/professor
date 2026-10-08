package doctor

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/pricing/modelcost"
)

type priceTransport func(*http.Request) (*http.Response, error)

func (f priceTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// pricePages is a fetch over two minimal publisher pages.
func pricePages() func(context.Context) ([]modelcost.Catalog, error) {
	const claude = "# Pricing\n## Model pricing\n| Model | Base input tokens | Output tokens |\n| --- | --- | --- |\n" +
		"| Claude Opus 5.5 | $4 / MTok | $20 / MTok |\n"
	const openai = "# Pricing\nPrices per 1M tokens.\n### Standard pricing data\n| Model | Input | Output |\n" +
		"| --- | --- | --- |\n| gpt-6-astra | $10.00 | $50.00 |\n"
	return modelcost.NewFetcher(&http.Client{Transport: priceTransport(func(r *http.Request) (*http.Response, error) {
		body := openai
		if r.URL.Host == "platform.claude.com" {
			body = claude
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}, nil).Fetch
}

// priceDoctorRuntime is a runtime with a fresh home (no clone record) whose
// pfm.config.json sits in a fresh temp dir; override, when non-empty, is
// written beside it as pfm.prices.json.
func priceDoctorRuntime(t *testing.T, override string) (config.Runtime, string) {
	t.Helper()
	runtime := config.Runtime{Config: config.Defaults(t.TempDir(), nil), Paths: paths.Values{Home: t.TempDir()}}
	runtime.Config.Path = filepath.Join(t.TempDir(), config.FileName)
	pricesPath := config.PricesPath(runtime.Config.Path)
	if override != "" {
		if err := os.WriteFile(pricesPath, []byte(override), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return runtime, pricesPath
}

func TestDoctorPricesRow(t *testing.T) {
	const valid = `{"version": 2, "rows": [{"key": "claude-negotiated-1", "engine": "claude", "in": 7, "out": 35}]}`
	offline := &paths.MapEnv{Values: map[string]string{paths.EnvPricesOffline: "1"}}
	online := &paths.MapEnv{Values: map[string]string{}}
	later := clock.NewFake(time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC))
	cases := []struct {
		name, override string
		dependencies   Dependencies
		want           []string
		warnings       int
		rowIsWarning   bool // the row itself is the one warning
	}{
		{
			name: "offline", dependencies: Dependencies{Env: offline},
			want: []string{"doctor: prices rows=", " from=embedded refresh=offline override=none\n"},
		},
		{
			name: "active override", override: valid, dependencies: Dependencies{Env: offline},
			want: []string{" refresh=offline override=active rows=1 path={path}\n"},
		},
		{
			name:         "invalid override",
			override:     `{"version": 1, "rows": []}`,
			dependencies: Dependencies{Env: offline},
			want: []string{
				"doctor: prices refresh=offline override=invalid error=prices {path}: version must be 2",
			},
			warnings:     1,
			rowIsWarning: true,
		},
		{
			name: "fetch failed",
			dependencies: Dependencies{
				Env:   online,
				Clock: later,
				FetchPrices: func(context.Context) ([]modelcost.Catalog, error) {
					return nil, errors.New("fixture network failure")
				},
			},
			want: []string{
				" refresh=failed override=none\n",
				"doctor: prices warning=fetch-failed error=fixture network failure\n",
			},
			warnings: 1,
		},
		{
			name:         "refreshed without a clone record",
			dependencies: Dependencies{Env: online, Clock: later, FetchPrices: pricePages()},
			want: []string{
				"doctor: prices rows=2 fetched=2100-01-01T00:00:00Z from=fetch refresh=refreshed override=none\n",
				"doctor: prices warning=not-persisted error=",
			},
			warnings: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runtime, pricesPath := priceDoctorRuntime(t, tc.override)
			var stdout bytes.Buffer
			if warnings := printPrices(
				&stdout,
				runtime,
				normalizeDependencies(tc.dependencies),
			); warnings != tc.warnings {
				t.Errorf("warnings=%d, want %d; output %q", warnings, tc.warnings, stdout.String())
			}
			wantLines := 1 + tc.warnings
			if tc.rowIsWarning {
				wantLines = tc.warnings
			}
			if lines := strings.Count(stdout.String(), "\n"); lines != wantLines {
				t.Errorf("output has %d lines, want the row plus one per warning: %q", lines, stdout.String())
			}
			for _, want := range tc.want {
				if want = strings.ReplaceAll(want, "{path}", pricesPath); !strings.Contains(stdout.String(), want) {
					t.Errorf("output=%q, want it to contain %q", stdout.String(), want)
				}
			}
		})
	}
}
