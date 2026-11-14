package tokens

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/pricing"
	"github.com/rezzminator/professor/pfm/internal/pricing/modelcost"
)

// fixtureDocument is the saved `pfm model-cost --json --all` document the
// goldens price with.
func fixtureDocument(t *testing.T) pricing.Document {
	t.Helper()
	path := fixture(t, "model-cost.json")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := pricing.DecodeDocument(content, path)
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return doc
}

// fileTable is table as a prices.json holds it: pfm sets each row's source.
func fileTable(table pricing.Table) pricing.Table {
	rows := append([]pricing.Row(nil), table.Rows...)
	for i := range rows {
		rows[i].Source = ""
	}
	table.Rows = rows
	return table
}

func writeJSON(t *testing.T, path string, value any) string {
	t.Helper()
	content, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadSavedReadsADocumentOrABareTable(t *testing.T) {
	path := fixture(t, "model-cost.json")
	book, err := loadSaved(path)
	if err != nil {
		t.Fatalf("loadSaved(document) = %v", err)
	}
	if book.saved != path || book.servedFrom == nil || *book.servedFrom != pricing.FromEmbedded ||
		book.refresh == nil || book.refresh.Status != modelcost.StatusOffline {
		t.Errorf("document book = saved %q served %v refresh %v; want the path, embedded, offline",
			book.saved, book.servedFrom, book.refresh)
	}
	if _, found := book.table.Resolve("claude-sonnet-5"); !found {
		t.Errorf("the document's rows do not price claude-sonnet-5")
	}

	encoded, err := pricing.Encode(fileTable(book.table))
	if err != nil {
		t.Fatal(err)
	}
	bare := filepath.Join(t.TempDir(), "prices.json")
	if err := os.WriteFile(bare, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	table, err := loadSaved(bare)
	if err != nil {
		t.Fatalf("loadSaved(bare table) = %v", err)
	}
	if table.servedFrom != nil || table.refresh != nil || len(table.table.Rows) != len(book.table.Rows) {
		t.Errorf("bare table book = served %v refresh %v, %d rows; want no provenance and %d rows",
			table.servedFrom, table.refresh, len(table.table.Rows), len(book.table.Rows))
	}
	want := `{"saved":"` + bare + `","fetched_at":"2026-01-01T00:00:00Z","sources":[` +
		`{"provider":"anthropic","url":"https://example.invalid/anthropic/pricing"},` +
		`{"provider":"openai","url":"https://example.invalid/openai/pricing"}],"billing":null,"served_from":null,"refresh":null}`
	if got := stringify(table.catalog(), 0); got != want {
		t.Errorf("catalog of a bare table:\n got %s\nwant %s", got, want)
	}
}

func TestBillingSelectionSurvivesSavedDocumentAndReport(t *testing.T) {
	doc := fixtureDocument(t)
	doc.Billing = &pricing.Billing{Route: "bedrock", Region: "geo", Configured: true, Multiplier: 1.1}
	path := writeJSON(t, filepath.Join(t.TempDir(), "cloud.json"), doc)
	book, err := loadSaved(path)
	if err != nil {
		t.Fatal(err)
	}
	a, _, _ := testAuditor(t)
	a.prices = book
	if got, want := strings.ReplaceAll(
		a.gapsLine(),
		filepath.Dir(path),
		"{dir}",
	), "pricing: saved table {dir}/cloud.json; Standard service, published prompt/context tiers (Codex aggregates use the run peak tier); table fetched 2026-01-01T00:00:00Z from anthropic, openai; not historical billing; Claude bedrock/geo\ndata gaps: transcript totals exclude background API calls (auto_mode, prompt_suggestion, hook_prompt, generate_session_title, agent_summary); captured telemetry is reported separately by pfm tokens (without --tree); missing telemetry and metadata-filtered background sessions cannot be quantified"; got != want {
		t.Errorf("gaps line = %q, want %q", got, want)
	}
	var report struct {
		Billing json.RawMessage `json:"billing"`
	}
	if err := json.Unmarshal([]byte(stringify(book.catalog(), 0)), &report); err != nil {
		t.Fatal(err)
	}
	if got, want := string(
		report.Billing,
	), `{"route":"bedrock","region":"geo","configured":true,"multiplier":1.1}`; got != want {
		t.Errorf("billing in the report JSON = %q, want %q", got, want)
	}
}

func TestLoadSavedRefusals(t *testing.T) {
	dir := t.TempDir()
	var raw map[string]any
	content, err := os.ReadFile(fixture(t, "model-cost.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(content, &raw); err != nil {
		t.Fatal(err)
	}
	raw["model"] = "claude-sonnet-5"
	single := writeJSON(t, filepath.Join(dir, "single.json"), raw)
	garbled := filepath.Join(dir, "garbled.json")
	if err := os.WriteFile(garbled, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing.json")
	for path, want := range map[string]string{single: "--prices {dir}/single.json: a single-model lookup (claude-sonnet-5); prices come from `pfm model-cost --json --all`", missing: "--prices {dir}/missing.json: open {dir}/missing.json: no such file or directory", garbled: "--prices {dir}/garbled.json: invalid character 'n' looking for beginning of object key string"} {
		if _, err := loadSaved(path); strings.ReplaceAll(fmt.Sprint(err), dir, "{dir}") != want {
			t.Errorf(
				"loadSaved(%s) = %q, want %q",
				filepath.Base(path),
				strings.ReplaceAll(fmt.Sprint(err), dir, "{dir}"),
				want,
			)
		}
	}
}

func TestReadRateIsHitForClaudeAndCachedForCodex(t *testing.T) {
	hit, cached := 0.2, 0.4
	rates := pricing.Rates{Hit: &hit, Cached: &cached}
	if got := readRate(pricing.Row{Engine: pricing.EngineClaude}, rates); got != &hit {
		t.Errorf("a Claude row reads at hit, got %v", got)
	}
	if got := readRate(pricing.Row{Engine: pricing.EngineCodex}, rates); got != &cached {
		t.Errorf("a Codex row reads at cached, got %v", got)
	}
	if orZero(nil) != 0 || orZero(&hit) != 0.2 {
		t.Errorf("rate: nil is 0, a published rate is itself")
	}
	book, err := loadSaved(fixture(t, "model-cost.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, found := book.rates("no-such-model", 1000); found {
		t.Errorf("rates found a row for a model no row prices")
	}
	row, at, found := book.rates("claude-sonnet-5", 1000)
	if !found || row.Key != "claude-sonnet-5" || orZero(at.In) != 2 || orZero(readRate(row, at)) != 0.2 {
		t.Errorf("rates(claude-sonnet-5) = %s %v in %v read %v, want in 2 and read 0.2", row.Key, found, orZero(at.In),
			orZero(readRate(row, at)))
	}
}

func TestRefreshObjectKeepsPfmMemberOrder(t *testing.T) {
	yes, no := true, false
	report := pricing.RefreshReport{
		Status: "refreshed", Error: "e", Changed: &yes, Persisted: &no, PersistError: "p", StampError: "s",
	}
	want := `{"status":"refreshed","error":"e","changed":true,"persisted":false,"persist_error":"p","stamp_error":"s"}`
	if got := stringify(refreshObject(report), 0); got != want {
		t.Errorf("refreshObject = %s, want %s", got, want)
	}
	if got := stringify(refreshObject(pricing.RefreshReport{Status: "current"}), 0); got != `{"status":"current"}` {
		t.Errorf("refreshObject(current) = %s: an absent member is left out", got)
	}
}

// servedHome is a home whose clone holds the fixture table.
func servedHome(t *testing.T) string {
	t.Helper()
	home, clone := t.TempDir(), t.TempDir()
	file := filepath.Join(clone, filepath.FromSlash(pricing.FileRel))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := pricing.WriteFile(file, fileTable(fixtureDocument(t).Table())); err != nil {
		t.Fatal(err)
	}
	if err := paths.WriteSourceRepoMarker(home, clone); err != nil {
		t.Fatal(err)
	}
	return home
}

// countingFetch fails every fetch with err, counting the calls.
type countingFetch struct {
	calls int
	err   error
}

func (f *countingFetch) fetch(context.Context) ([]modelcost.Catalog, error) {
	f.calls++
	return nil, f.err
}

func TestLoadServedServesTheEmbeddedTableOfflineWithoutAClone(t *testing.T) {
	book, err := loadServed(context.Background(), modelcost.Options{
		Home: t.TempDir(), Offline: true, Clock: clock.NewFake(goldenNow),
		Fetch: (&countingFetch{err: errors.New("offline must not fetch")}).fetch,
	})
	if err != nil {
		t.Fatalf("loadServed error = %v, want the embedded table", err)
	}
	if book.saved != "" || *book.servedFrom != pricing.FromEmbedded || book.refresh.Status != modelcost.StatusOffline ||
		len(book.table.Rows) == 0 {
		t.Errorf("served book = saved %q from %q refresh %q, %d rows; want the embedded table, offline",
			book.saved, *book.servedFrom, book.refresh.Status, len(book.table.Rows))
	}
}

// A published table that cannot be loaded prices nothing and loses nothing
// else: every token still counts, every dollar reads "n/a", and the data gaps
// name the table failure.
func TestAnUnloadablePublishedTablePricesNothingAndStillCountsTokens(t *testing.T) {
	broken := errors.New("embedded prices.json: unexpected end of JSON input")
	saved := refreshPrices
	refreshPrices = func(context.Context, modelcost.Options) (pricing.Prices, modelcost.Result, error) {
		return pricing.Prices{}, modelcost.Result{}, broken
	}
	t.Cleanup(func() { refreshPrices = saved })
	root := fixture(t, "claude/projects")
	out := filepath.Join(t.TempDir(), "report.json")
	code, stdout, stderr := auditWith(t, Options{Served: modelcost.Options{Home: t.TempDir()}},
		"--since", "99999d", "--root", root, "--out", out)
	if code != 0 || stderr != "" {
		t.Fatalf("exit %d, stderr %q; want 0 and nothing", code, stderr)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Pricing struct {
			TableFailure string `json:"table_failure"`
		} `json:"pricing"`
	}
	wantFailure := "load the price table: " + broken.Error()
	if err := json.Unmarshal(raw, &document); err != nil || document.Pricing.TableFailure != wantFailure {
		t.Errorf("%s pricing.table_failure = %q, %v; want %q", out, document.Pricing.TableFailure, err, wantFailure)
	}
	for _, want := range []string{
		"pricing: none, no price table loaded; not historical billing\n",
		"data gaps: price table unusable (load the price table: embedded prices.json: unexpected end of JSON " +
			`input) — tokens counted, every dollar "n/a" · `,
		" UNPRICED calls ",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
}

func TestAFailedRefreshServesTheCloneTableOnceAndLeadsTheGapsLine(t *testing.T) {
	root := fixture(t, "claude/projects")
	modes := map[string][]string{
		"report": {"--since", "99999d", "--root", root},
		"codex":  {"--codex", "--since", "99999d", "--codex-root", fixture(t, "codex")},
	}
	for mode, args := range modes {
		t.Run(mode, func(t *testing.T) {
			fetch := &countingFetch{err: errors.New("fixture network failure")}
			served := modelcost.Options{Home: servedHome(t), Clock: clock.NewFake(goldenNow), Fetch: fetch.fetch}
			code, stdout, stderr := auditWith(t, Options{Served: served}, args...)
			if code != 0 || stderr != "" {
				t.Fatalf("exit %d, stderr %q; want 0 and nothing", code, stderr)
			}
			if fetch.calls != 1 {
				t.Errorf("fetched %d times, want once per run", fetch.calls)
			}
			// the served table is the newer of the clone's and the embedded one
			fetched := regexp.MustCompile(`table fetched (\S+) from`).FindStringSubmatch(stdout)
			if fetched == nil {
				t.Fatalf("stdout carries no pricing line:\n%s", stdout)
			}
			want := "data gaps: price refresh failed (fixture network failure) — served the table fetched " + fetched[1]
			if !strings.Contains(stdout, want) {
				t.Errorf("stdout lacks %q:\n%s", want, stdout)
			}
		})
	}
}

// An invalid pfm.prices.json loses only itself: the run counts every token, prices by the
// published table without the override, and names the override's failure in its data gaps.
func TestAnInvalidOverridePricesByThePublishedTableAndNamesTheFailure(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), config.FileName)
	override := config.PricesPath(configPath)
	if err := os.WriteFile(override, []byte(`{"version":1,"rows":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	served := modelcost.Options{
		Home:       servedHome(t),
		ConfigPath: configPath,
		Offline:    true,
		Clock:      clock.NewFake(goldenNow),
	}
	root := fixture(t, "claude/projects")
	code, stdout, stderr := auditWith(t, Options{Served: served}, "--since", "99999d", "--root", root)
	if code != 0 || stderr != "" {
		t.Fatalf("exit %d, stderr %q; want 0 and nothing", code, stderr)
	}
	want := "data gaps: price override unusable (prices " + override + ": version must be 2, got 1) — " +
		"dollars use the published table without it"
	if !strings.Contains(stdout, want) {
		t.Errorf("stdout lacks %q:\n%s", want, stdout)
	}
}
