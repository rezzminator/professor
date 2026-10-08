package command

import (
	"bytes"
	"context"
	"encoding/json"
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
	"github.com/rezzminator/professor/pfm/internal/pricing"
	"github.com/rezzminator/professor/pfm/internal/pricing/modelcost"
)

type modelCostTransport func(*http.Request) (*http.Response, error)

func (f modelCostTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const (
	claudePage = "# Pricing\nAll prices are in USD.\n## Model pricing\n" +
		"| Model | Base input tokens | 5m cache writes | 1h cache writes | Cache hits and refreshes | Output tokens |\n" +
		"| --- | --- | --- | --- | --- | --- |\n" +
		"| Claude Opus 5.5 | $4 / MTok | $5 / MTok | $8 / MTok | $0.20 / MTok | $20 / MTok |\n" +
		"| Claude Opus 4.1 | $15 / MTok | $18.75 / MTok | $30 / MTok | $1.50 / MTok | $75 / MTok |\n"
	openAIPage = "# Pricing\nPrices per 1M tokens.\n### Standard pricing data\n" +
		"| Model | Input | Cached input | Output |\n| --- | --- | --- | --- |\n| gpt-6-astra | $10.00 | $1.00 | $50.00 |\n"
)

// t0 is the clone file's fetch time, later than any embedded table.
var t0 = time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)

// fixture is one model-cost run's world: a clone file fetched at t0, a
// fetcher over the two pages (or failing), and its call count.
type fixture struct {
	home, file, configPath string
	calls                  int
	fail                   error
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	home, clone := t.TempDir(), t.TempDir()
	file := filepath.Join(clone, filepath.FromSlash(pricing.FileRel))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	in, out := 15.0, 75.0
	table := pricing.Table{
		Version:   pricing.Version,
		FetchedAt: t0.Format(time.RFC3339),
		Sources: []pricing.Source{
			{Provider: "anthropic", URL: "https://platform.claude.com/docs/en/about-claude/pricing.md"},
		},
		Rows: []pricing.Row{
			{Key: "claude-opus-4-1", Engine: pricing.EngineClaude, Rates: pricing.Rates{In: &in, Out: &out}},
		},
	}
	if _, err := pricing.WriteFile(file, table); err != nil {
		t.Fatal(err)
	}
	if err := paths.WriteSourceRepoMarker(home, clone); err != nil {
		t.Fatal(err)
	}
	resolved, err := pricing.CloneFile(home)
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{home: home, file: resolved, configPath: filepath.Join(t.TempDir(), config.FileName)}
}

func (f *fixture) run(at time.Time, offline bool, args ...string) (int, string, string) {
	fetcher := modelcost.NewFetcher(
		&http.Client{Transport: modelCostTransport(func(r *http.Request) (*http.Response, error) {
			if f.fail != nil {
				return nil, f.fail
			}
			body := openAIPage
			if r.URL.Host == "platform.claude.com" {
				body = claudePage
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		})},
		nil,
	)
	fetch := func(ctx context.Context) ([]modelcost.Catalog, error) {
		f.calls++
		return fetcher.Fetch(ctx)
	}
	var stdout, stderr bytes.Buffer
	code := runModelCost(args, &stdout, &stderr, modelcost.Options{
		Home: f.home, ConfigPath: f.configPath, Offline: offline, Clock: clock.NewFake(at), Fetch: fetch,
	})
	return code, stdout.String(), stderr.String()
}

func TestModelCostUsageIsDecidedBeforeAnyFetch(t *testing.T) {
	f := newFixture(t)
	for _, args := range [][]string{
		{},
		{"--json"},
		{"--force"},
		{"--all", "claude-opus-4-1"},
		{"--check", "--json"},
		{"--check", "claude-opus-4-1"},
		{"--all", "--check"},
		{"claude-opus-4-1", "gpt-6-astra"},
		{"--nonesuch"},
	} {
		code, stdout, stderr := f.run(t0.Add(48*time.Hour), false, args...)
		if code != 2 || !strings.Contains(stderr, "usage: pfm model-cost") || stdout != "" {
			t.Errorf("%v: code %d stdout %q stderr %q; want 2 with the usage on stderr", args, code, stdout, stderr)
		}
	}
	if f.calls != 0 {
		t.Fatalf("usage errors fetched %d times, want 0", f.calls)
	}
	code, stdout, _ := f.run(t0, false, "--help")
	if code != 0 || !strings.Contains(stdout, "usage: pfm model-cost [--json] [--force] MODEL_ID | --all | --check") {
		t.Fatalf("--help: code %d stdout %q", code, stdout)
	}
}

// document is the --json document, decoded for assertions.
type decodedDocument struct {
	Version    int              `json:"version"`
	FetchedAt  string           `json:"fetched_at"`
	CheckedAt  string           `json:"checked_at"`
	Sources    []pricing.Source `json:"sources"`
	ServedFrom string           `json:"served_from"`
	File       string           `json:"file"`
	FileError  string           `json:"file_error"`
	Refresh    struct {
		Status       string `json:"status"`
		Error        string `json:"error"`
		Changed      *bool  `json:"changed"`
		Persisted    *bool  `json:"persisted"`
		PersistError string `json:"persist_error"`
	} `json:"refresh"`
	Override *pricing.Override `json:"override"`
	Model    string            `json:"model"`
	Rows     []pricing.Row     `json:"rows"`
}

func decodeDocument(t *testing.T, stdout string) decodedDocument {
	t.Helper()
	var doc decodedDocument
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("--json output is not one JSON document: %v\n%s", err, stdout)
	}
	return doc
}

func TestModelCostAllJSONRefreshesAStaleTable(t *testing.T) {
	f := newFixture(t)
	now := t0.Add(25 * time.Hour)
	code, stdout, stderr := f.run(now, false, "--all", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
	doc := decodeDocument(t, stdout)
	servedRight := doc.Version == 2 && doc.ServedFrom == pricing.FromFile && doc.File == f.file
	timedRight := doc.FetchedAt == now.Format(time.RFC3339) && doc.CheckedAt == now.Format(time.RFC3339Nano)
	if !servedRight || !timedRight || doc.Override != nil || doc.Model != "" || len(doc.Sources) != 2 {
		t.Fatalf("document header = %+v", doc)
	}
	if doc.Refresh.Status != "refreshed" || doc.Refresh.Changed == nil || !*doc.Refresh.Changed ||
		doc.Refresh.Persisted == nil || !*doc.Refresh.Persisted || doc.Refresh.Error != "" {
		t.Fatalf("refresh = %+v", doc.Refresh)
	}
	keys := []string{}
	for _, row := range doc.Rows {
		keys = append(keys, row.Key+"/"+row.Source)
	}
	if strings.Join(keys, " ") != "claude-opus-4-1/published claude-opus-5-5/published gpt-6-astra/published" {
		t.Fatalf("rows = %v", keys)
	}
	if !strings.HasPrefix(stdout, "{\n  \"version\": 2,") || !strings.HasSuffix(stdout, "}\n") || f.calls != 1 {
		t.Fatalf("want an indented, newline-terminated document after one fetch; %d fetches:\n%s", f.calls, stdout)
	}
}

func TestModelCostModelTextInsideTheWindowFetchesNothing(t *testing.T) {
	f := newFixture(t)
	code, stdout, stderr := f.run(t0.Add(time.Hour), false, "claude-opus-4-1")
	lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
	if code != 0 || stderr != "" || f.calls != 0 || len(lines) != 3 {
		t.Fatalf("code %d, %d fetches, stderr %q, stdout:\n%s", code, f.calls, stderr, stdout)
	}
	summary := "prices: 1 rows · fetched " + t0.Format(
		time.RFC3339,
	) + " · from file " + f.file + " · refresh current · override: none"
	if lines[0] != summary {
		t.Fatalf("summary = %q\nwant      %q", lines[0], summary)
	}
	if fields := strings.Fields(
		lines[1],
	); strings.Join(
		fields,
		" ",
	) != "KEY ENGINE IN OUT HIT CACHED W5M W1H LONG SOURCE" {
		t.Fatalf("header = %q", lines[1])
	}
	if fields := strings.Fields(
		lines[2],
	); strings.Join(
		fields,
		" ",
	) != "claude-opus-4-1 claude 15 75 - - - - - published" {
		t.Fatalf("row = %q", lines[2])
	}
}

func TestModelCostForceRefreshesInsideTheWindowAndResolvesASnapshot(t *testing.T) {
	f := newFixture(t)
	code, stdout, stderr := f.run(t0.Add(time.Hour), false, "--force", "claude-opus-4-1-20250805", "--json")
	doc := decodeDocument(t, stdout)
	if code != 0 || stderr != "" || f.calls != 1 || doc.Refresh.Status != "refreshed" {
		t.Fatalf("code %d, %d fetches, stderr %q, refresh %+v", code, f.calls, stderr, doc.Refresh)
	}
	if doc.Model != "claude-opus-4-1-20250805" || len(doc.Rows) != 1 || doc.Rows[0].Key != "claude-opus-4-1" ||
		*doc.Rows[0].Hit != 1.5 {
		t.Fatalf("model %q rows %+v", doc.Model, doc.Rows)
	}
}

func TestModelCostFetchFailureWarnsAndFailsOnlyWhenForced(t *testing.T) {
	f := newFixture(t)
	f.fail = errors.New("fixture network failure")
	code, stdout, stderr := f.run(t0.Add(25*time.Hour), false, "--all")
	if code != 0 || !strings.Contains(stdout, "refresh failed") ||
		!strings.Contains(
			stderr,
			"pfm model-cost: warning: refresh failed, serving the table fetched "+t0.Format(time.RFC3339),
		) ||
		!strings.Contains(stderr, "fixture network failure") {
		t.Fatalf(
			"unforced failure: code %d stdout %q stderr %q; want 0, the summary and a named warning",
			code,
			stdout,
			stderr,
		)
	}
	if code, _, stderr := f.run(
		t0.Add(25*time.Hour),
		false,
		"--force",
		"--all",
	); code != 1 ||
		!strings.Contains(stderr, "fixture network failure") {
		t.Fatalf("forced failure: code %d stderr %q; want 1 naming the cause", code, stderr)
	}
}

func TestModelCostUnknownModelNamesTheListCommand(t *testing.T) {
	f := newFixture(t)
	code, stdout, stderr := f.run(t0.Add(time.Hour), false, "gpt-nonesuch")
	want := "pfm model-cost: gpt-nonesuch is not priced in the table fetched " + t0.Format(time.RFC3339) +
		"; pfm model-cost --all lists every priced key\n"
	if code != 1 || stdout != "" || stderr != want {
		t.Fatalf("code %d stdout %q stderr %q; want 1 and %q", code, stdout, stderr, want)
	}
}

func TestModelCostCheckPrintsTheSummaryAndFailsOnAnInvalidOverride(t *testing.T) {
	f := newFixture(t)
	code, stdout, _ := f.run(t0.Add(time.Hour), false, "--check")
	if code != 0 || strings.Count(stdout, "\n") != 1 || !strings.HasPrefix(stdout, "prices: 1 rows · ") {
		t.Fatalf("--check: code %d stdout %q; want 0 and one summary line", code, stdout)
	}
	if err := os.WriteFile(config.PricesPath(f.configPath), []byte(`{"version":1,"rows":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := f.run(t0.Add(time.Hour), false, "--check")
	if code != 1 || !strings.Contains(stderr, config.PricesPath(f.configPath)) {
		t.Fatalf("invalid override: code %d stderr %q; want 1 naming %s", code, stderr, config.PricesPath(f.configPath))
	}
}

func TestModelCostOfflineFetchesNothingEvenWhenForced(t *testing.T) {
	f := newFixture(t)
	code, stdout, _ := f.run(t0.Add(48*time.Hour), true, "--force", "--check")
	if code != 0 || f.calls != 0 || !strings.Contains(stdout, "refresh offline") {
		t.Fatalf("offline: code %d, %d fetches, stdout %q", code, f.calls, stdout)
	}
}
