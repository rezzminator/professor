package installer

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/pricing"
	"github.com/rezzminator/professor/pfm/internal/pricing/modelcost"
)

type installPriceTransport func(*http.Request) (*http.Response, error)

func (f installPriceTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// installPricePages is a fetch over two minimal publisher pages.
func installPricePages() func(context.Context) ([]modelcost.Catalog, error) {
	const claude = "# Pricing\n## Model pricing\n| Model | Base input tokens | Output tokens |\n| --- | --- | --- |\n" +
		"| Claude Opus 5.5 | $4 / MTok | $20 / MTok |\n"
	const openai = "# Pricing\nPrices per 1M tokens.\n### Standard pricing data\n| Model | Input | Output |\n" +
		"| --- | --- | --- |\n| gpt-6-astra | $10.00 | $50.00 |\n"
	return modelcost.NewFetcher(
		&http.Client{Transport: installPriceTransport(func(r *http.Request) (*http.Response, error) {
			body := openai
			if r.URL.Host == "platform.claude.com" {
				body = claude
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		})},
		nil,
	).Fetch
}

func TestRefreshPricesReportsOneLinePerOutcome(t *testing.T) {
	embedded, err := pricing.Embedded()
	if err != nil {
		t.Fatalf("embedded prices: %v", err)
	}
	later := clock.NewFake(time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC))
	failing := func(context.Context) ([]modelcost.Catalog, error) { return nil, errors.New("publisher unreachable") }
	cases := []struct {
		name     string
		setup    func(t *testing.T, home, configPath string)
		options  modelcost.Options
		want     []string
		unwanted []string
	}{
		{
			name:    "offline serves the embedded table in one ok line",
			options: modelcost.Options{Offline: true},
			want: []string{
				"  ok      prices offline · " + strconv.Itoa(
					len(embedded.Rows),
				) + " rows · fetched " + embedded.FetchedAt + "\n",
			},
			unwanted: []string{"warn"},
		},
		{
			name:    "a failed fetch is one warn line, never ok",
			options: modelcost.Options{Clock: later, Fetch: failing},
			want: []string{
				"  warn    prices refresh failed, serving the table fetched " + embedded.FetchedAt + ": publisher unreachable\n",
			},
			unwanted: []string{"  ok      "},
		},
		{
			name:     "a refresh with no recorded clone is not persisted",
			options:  modelcost.Options{Clock: later, Fetch: installPricePages()},
			want:     []string{"  warn    prices refreshed but not persisted: ", "source repository marker"},
			unwanted: []string{"  ok      "},
		},
		{
			name: "an invalid override is one warn line naming it",
			setup: func(t *testing.T, _, configPath string) {
				t.Helper()
				writeInstallPriceFile(t, config.PricesPath(configPath), `{"version": 1, "rows": []}`)
			},
			options:  modelcost.Options{Offline: true},
			want:     []string{"  warn    prices: prices ", "pfm.prices.json: version must be 2, got 1\n"},
			unwanted: []string{"  ok      "},
		},
		{
			name: "an unreadable check stamp warns beside the ok line",
			setup: func(t *testing.T, home, _ string) {
				t.Helper()
				writeInstallPriceFile(t, paths.PricesStampPath(home), "not a stamp\n")
			},
			options: modelcost.Options{Offline: true},
			want:    []string{"  ok      prices offline · ", "  warn    prices check stamp: price check stamp "},
		},
		{
			name: "an unusable clone file warns beside the ok line",
			setup: func(t *testing.T, home, _ string) {
				t.Helper()
				if err := paths.WriteSourceRepoMarker(home, t.TempDir()); err != nil {
					t.Fatal(err)
				}
			},
			options: modelcost.Options{Offline: true},
			want: []string{
				"  ok      prices offline · ",
				"  warn    prices clone file unusable, serving the embedded table: read clone prices: ",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			configPath := filepath.Join(t.TempDir(), config.FileName)
			if tc.setup != nil {
				tc.setup(t, home, configPath)
			}
			options := tc.options
			options.Home, options.ConfigPath = home, configPath
			var stdout bytes.Buffer
			RefreshPrices(&stdout, options)
			for _, want := range tc.want {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("output lacks %q:\n%s", want, stdout.String())
				}
			}
			for _, unwanted := range tc.unwanted {
				if strings.Contains(stdout.String(), unwanted) {
					t.Errorf("output holds %q:\n%s", unwanted, stdout.String())
				}
			}
		})
	}
}

func writeInstallPriceFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
