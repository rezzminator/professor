package pricing

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

const (
	claudeRow = `{"key":"opus","engine":"claude","match":["opus"],"in":5,"out":25,"hit":0.5,"w5m":6.25,"w1h":10,"long_in":1,"long_out":1}`
	codexRow  = `{"key":"gpt-5.4","engine":"codex","match":["gpt-5.4"],"in":2.5,"out":15,"cached":0.25,"long_in":1,"long_out":1}`

	testSource = "/scratch/pfm.prices.json"
)

func tableDoc(rows ...string) string {
	return `{"version":1,"rows":[` + strings.Join(rows, ",") + `]}`
}

func shippedTable(t *testing.T) Table {
	t.Helper()
	table, err := Shipped()
	if err != nil {
		t.Fatalf("Shipped: %v", err)
	}
	return table
}

// publishedRate is one id of testdata/published-rates.json: a Claude id carries
// hit, w5m and w1h, a Codex id carries cached.
type publishedRate struct {
	ID     string   `json:"id"`
	In     float64  `json:"in"`
	Out    float64  `json:"out"`
	Hit    *float64 `json:"hit"`
	W5m    *float64 `json:"w5m"`
	W1h    *float64 `json:"w1h"`
	Cached *float64 `json:"cached"`
}

func readPublishedRates(t *testing.T) []publishedRate {
	t.Helper()
	content, err := os.ReadFile("testdata/published-rates.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Version int             `json:"version"`
		IDs     []publishedRate `json:"ids"`
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&doc); err != nil {
		t.Fatalf("published-rates.json: %v", err)
	}
	if doc.Version != 1 || len(doc.IDs) != 35 {
		t.Fatalf("published-rates.json: version %d with %d ids, want version 1 with 35", doc.Version, len(doc.IDs))
	}
	return doc.IDs
}

func TestResolvePublishedIDs(t *testing.T) {
	table := shippedTable(t)
	for _, want := range readPublishedRates(t) {
		t.Run(want.ID, func(t *testing.T) {
			row, ok := table.Resolve(want.ID)
			if !ok {
				t.Fatalf("id %s: unpriced, want a priced row", want.ID)
			}
			check := func(field string, got, wantRate float64) {
				t.Helper()
				if got != wantRate {
					t.Errorf("id %s: winning key %s: %s = %v, want %v", want.ID, row.Key, field, got, wantRate)
				}
			}
			check("in", row.In, want.In)
			check("out", row.Out, want.Out)
			if want.Cached != nil {
				if row.Engine != EngineCodex {
					t.Errorf("id %s: winning key %s: engine = %q, want codex", want.ID, row.Key, row.Engine)
				}
				check("cached", row.Cached, *want.Cached)
				return
			}
			if row.Engine != EngineClaude {
				t.Errorf("id %s: winning key %s: engine = %q, want claude", want.ID, row.Key, row.Engine)
			}
			check("hit", row.Hit, *want.Hit)
			check("w5m", row.W5m, *want.W5m)
			check("w1h", row.W1h, *want.W1h)
		})
	}
}

func TestEveryShippedKeyWinsForAPublishedID(t *testing.T) {
	table := shippedTable(t)
	reached := map[string]bool{}
	for _, published := range readPublishedRates(t) {
		if row, ok := table.Resolve(published.ID); ok {
			reached[row.Key] = true
		}
	}
	for _, row := range table.Rows {
		if !reached[row.Key] {
			t.Errorf("shipped key %q wins for no published id: a dead row", row.Key)
		}
	}
}

func TestResolveIsOrderFree(t *testing.T) {
	short := Row{Key: "opus", Match: []string{"opus"}}
	long := Row{Key: "opus-4-1", Match: []string{"opus-4-1"}}
	for _, tc := range []struct {
		name string
		rows []Row
	}{
		{"short pattern first", []Row{short, long}},
		{"long pattern first", []Row{long, short}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row, ok := Table{Rows: tc.rows}.Resolve("claude-opus-4-1-20250805")
			if !ok || row.Key != "opus-4-1" {
				t.Fatalf("Resolve = (%q, %v), want opus-4-1", row.Key, ok)
			}
		})
	}
}

func TestResolveEqualLengthTiePrefersTheEarlierStart(t *testing.T) {
	// Both five-letter patterns sit inside the id: "alpha" at 1, "phaze" at 3.
	const id = "xalphazex"
	early := Row{Key: "early", Match: []string{"alpha"}}
	late := Row{Key: "late", Match: []string{"phaze"}}
	for _, tc := range []struct {
		name string
		rows []Row
	}{
		{"early first", []Row{early, late}},
		{"late first", []Row{late, early}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row, ok := Table{Rows: tc.rows}.Resolve(id)
			if !ok || row.Key != "early" {
				t.Fatalf("Resolve(%q) = (%q, %v), want early", id, row.Key, ok)
			}
		})
	}
}

func TestResolveAnyPatternOfARowSelectsIt(t *testing.T) {
	table := Table{Rows: []Row{
		{Key: "multi", Match: []string{"first-name", "second-name"}},
		{Key: "other", Match: []string{"other"}},
	}}
	for _, id := range []string{"x-first-name-y", "x-second-name-y"} {
		if row, ok := table.Resolve(id); !ok || row.Key != "multi" {
			t.Errorf("Resolve(%q) = (%q, %v), want multi", id, row.Key, ok)
		}
	}
}

func TestResolveIsCaseInsensitive(t *testing.T) {
	table := shippedTable(t)
	if row, ok := table.Resolve("Claude-Opus-5-5"); !ok || row.Key != "opus-5-5" {
		t.Fatalf("Resolve(Claude-Opus-5-5) = (%q, %v), want opus-5-5", row.Key, ok)
	}
	upper := Table{Rows: []Row{{Key: "k", Match: []string{"MiXeD"}}}}
	if row, ok := upper.Resolve("a-mixed-b"); !ok || row.Key != "k" {
		t.Fatalf("an upper-case pattern must match a lower-case id, got (%q, %v)", row.Key, ok)
	}
}

func TestResolveUnpriced(t *testing.T) {
	table := shippedTable(t)
	for _, id := range []string{"llama-3", ""} {
		if row, ok := table.Resolve(id); ok {
			t.Errorf("Resolve(%q) = %q, want not found", id, row.Key)
		}
	}
}

func TestDecodeTableAcceptsBothEngines(t *testing.T) {
	rows, err := decodeTable([]byte(tableDoc(claudeRow, codexRow)), testSource, SourceOverride)
	if err != nil {
		t.Fatal(err)
	}
	want := []Row{
		{
			Key:     "opus",
			Engine:  EngineClaude,
			Match:   []string{"opus"},
			In:      5,
			Out:     25,
			Hit:     0.5,
			W5m:     6.25,
			W1h:     10,
			LongIn:  1,
			LongOut: 1,
			Source:  SourceOverride,
		},
		{
			Key:     "gpt-5.4",
			Engine:  EngineCodex,
			Match:   []string{"gpt-5.4"},
			In:      2.5,
			Out:     15,
			Cached:  0.25,
			LongIn:  1,
			LongOut: 1,
			Source:  SourceOverride,
		},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows = %+v, want %+v", rows, want)
	}
}

func TestDecodeTableRejects(t *testing.T) {
	without := func(row, field string) string {
		i := strings.Index(row, `"`+field+`"`)
		if i < 0 {
			t.Fatalf("test row has no %q", field)
		}
		end := strings.IndexAny(row[i:], ",}")
		out := row[:i] + row[i+end:]
		out = strings.Replace(out, ",,", ",", 1)
		out = strings.Replace(out, ",}", "}", 1)
		return strings.Replace(out, "{,", "{", 1)
	}
	with := func(row, extra string) string { return strings.TrimSuffix(row, "}") + "," + extra + "}" }
	replace := func(row, old, now string) string {
		if !strings.Contains(row, old) {
			t.Fatalf("test row has no %q", old)
		}
		return strings.Replace(row, old, now, 1)
	}

	cases := []struct {
		name    string
		content string
		wants   []string
	}{
		{"malformed JSON", `{`, []string{testSource}},
		{"trailing JSON", tableDoc(claudeRow) + ` {}`, []string{testSource, "multiple JSON values"}},
		{"unknown top-level field", `{"version":1,"rows":[],"extra":1}`, []string{testSource, "extra"}},
		{"unknown row field", tableDoc(with(claudeRow, `"w5":1`)), []string{testSource, "row 0", "opus", "w5"}},
		{"wrong type", tableDoc(replace(claudeRow, `"in":5`, `"in":"5"`)), []string{testSource, "row 0", "opus"}},
		{"row is not an object", `{"version":1,"rows":[5]}`, []string{testSource, "row 0"}},
		{"version 2", `{"version":2,"rows":[]}`, []string{testSource, "version must be 1, got 2"}},
		{"version missing", `{"rows":[]}`, []string{testSource, "version"}},
		{"rows missing", `{"version":1}`, []string{testSource, "rows"}},
		{"claude without w1h", tableDoc(without(claudeRow, "w1h")), []string{testSource, "row 0", "opus", "w1h"}},
		{"claude without w5m", tableDoc(without(claudeRow, "w5m")), []string{testSource, "opus", "w5m"}},
		{"claude without hit", tableDoc(without(claudeRow, "hit")), []string{testSource, "opus", "hit"}},
		{"codex without cached", tableDoc(without(codexRow, "cached")), []string{testSource, "gpt-5.4", "cached"}},
		{"row without long_in", tableDoc(without(claudeRow, "long_in")), []string{testSource, "opus", "long_in"}},
		{"row without long_out", tableDoc(without(codexRow, "long_out")), []string{testSource, "gpt-5.4", "long_out"}},
		{"row without in", tableDoc(without(claudeRow, "in")), []string{testSource, "opus", "in"}},
		{"row without out", tableDoc(without(codexRow, "out")), []string{testSource, "gpt-5.4", "out"}},
		{"codex carrying hit", tableDoc(with(codexRow, `"hit":1`)), []string{testSource, "gpt-5.4", "hit"}},
		{"codex carrying w5m", tableDoc(with(codexRow, `"w5m":1`)), []string{"gpt-5.4", "w5m"}},
		{"codex carrying w1h", tableDoc(with(codexRow, `"w1h":1`)), []string{"gpt-5.4", "w1h"}},
		{"claude carrying cached", tableDoc(with(claudeRow, `"cached":1`)), []string{testSource, "opus", "cached"}},
		{"negative in", tableDoc(replace(claudeRow, `"in":5`, `"in":-1`)), []string{testSource, "opus", "in", "-1"}},
		{"negative hit", tableDoc(replace(claudeRow, `"hit":0.5`, `"hit":-0.5`)), []string{"opus", "hit", "-0.5"}},
		{
			"negative long_out",
			tableDoc(replace(codexRow, `"long_out":1`, `"long_out":-2`)),
			[]string{"gpt-5.4", "long_out", "-2"},
		},
		{
			"negative cached",
			tableDoc(replace(codexRow, `"cached":0.25`, `"cached":-3`)),
			[]string{"gpt-5.4", "cached", "-3"},
		},
		{"empty key", tableDoc(replace(claudeRow, `"key":"opus"`, `"key":""`)), []string{testSource, "row 0", "key"}},
		{"missing key", tableDoc(without(claudeRow, "key")), []string{testSource, "row 0", "key"}},
		{
			"unknown engine",
			tableDoc(replace(claudeRow, `"claude"`, `"gemini"`)),
			[]string{testSource, "opus", "engine", "gemini"},
		},
		{"missing engine", tableDoc(without(claudeRow, "engine")), []string{testSource, "opus", "engine"}},
		{"empty match", tableDoc(replace(claudeRow, `["opus"]`, `[]`)), []string{testSource, "opus", "match"}},
		{"missing match", tableDoc(without(claudeRow, "match")), []string{testSource, "opus", "match"}},
		{"empty pattern", tableDoc(replace(claudeRow, `["opus"]`, `[""]`)), []string{testSource, "opus", "match"}},
		{
			"empty second pattern",
			tableDoc(replace(claudeRow, `["opus"]`, `["opus",""]`)),
			[]string{testSource, "opus", "match[1]"},
		},
		{"duplicate key", tableDoc(claudeRow, codexRow, claudeRow), []string{testSource, "row 2", "opus", "key"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := decodeTable([]byte(tc.content), testSource, SourceOverride)
			if err == nil {
				t.Fatalf("decoded %+v, want an error", rows)
			}
			for _, want := range tc.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not name %q", err, want)
				}
			}
		})
	}
}

func TestDecodeTableAcceptsAnEmptyRowList(t *testing.T) {
	rows, err := decodeTable([]byte(`{"version":1,"rows":[]}`), testSource, SourceOverride)
	if err != nil || len(rows) != 0 {
		t.Fatalf("decodeTable = (%v, %v), want no rows and no error", rows, err)
	}
}

func TestCheckPatterns(t *testing.T) {
	rows := func(a, b []string) []Row {
		return []Row{{Key: "first", Match: a}, {Key: "second", Match: b}}
	}
	t.Run("a shared pattern names both keys and the pattern", func(t *testing.T) {
		err := checkPatterns(rows([]string{"Opus"}, []string{"x", "oPUS"}), testSource)
		if err == nil {
			t.Fatal("want an error")
		}
		for _, want := range []string{testSource, "first", "second", "opus"} {
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(want)) {
				t.Errorf("error %q does not name %q", err, want)
			}
		}
	})
	t.Run("distinct patterns pass", func(t *testing.T) {
		if err := checkPatterns(rows([]string{"opus"}, []string{"opus-4-1"}), testSource); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("a repeat inside one row passes", func(t *testing.T) {
		if err := checkPatterns([]Row{{Key: "k", Match: []string{"opus", "OPUS"}}}, testSource); err != nil {
			t.Fatal(err)
		}
	})
}

// jsonDocument is the `pfm price --json` document, decoded loosely so a test
// reads exactly the keys the table wrote.
type jsonDocument struct {
	Version  int                          `json:"version"`
	Override json.RawMessage              `json:"override"`
	Rows     []map[string]json.RawMessage `json:"rows"`
}

func decodeJSONDocument(t *testing.T, content []byte) jsonDocument {
	t.Helper()
	var doc jsonDocument
	if err := json.Unmarshal(content, &doc); err != nil {
		t.Fatalf("JSON() is not JSON: %v\n%s", err, content)
	}
	return doc
}

func keysOf(row map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(row))
	for key := range row {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func TestJSONWithoutOverride(t *testing.T) {
	table := shippedTable(t)
	content, err := table.JSON()
	if err != nil {
		t.Fatal(err)
	}
	doc := decodeJSONDocument(t, content)
	if doc.Version != 1 {
		t.Errorf("version = %d, want 1", doc.Version)
	}
	if string(doc.Override) != "null" {
		t.Errorf("override = %s, want null", doc.Override)
	}
	if len(doc.Rows) != len(table.Rows) {
		t.Fatalf("rows = %d, want %d", len(doc.Rows), len(table.Rows))
	}
	for i, row := range doc.Rows {
		want := table.Rows[i]
		var key, source string
		_ = json.Unmarshal(row["key"], &key)
		_ = json.Unmarshal(row["source"], &source)
		if key != want.Key || source != SourceShipped {
			t.Errorf("row %d = key %q source %q, want %q %q", i, key, source, want.Key, SourceShipped)
		}
	}
}

func TestJSONEmitsOnlyTheColumnsOfTheEngine(t *testing.T) {
	table := Table{Rows: []Row{
		{
			Key:     "opus",
			Engine:  EngineClaude,
			Match:   []string{"opus"},
			In:      5,
			Out:     25,
			Hit:     0.5,
			W5m:     6.25,
			W1h:     10,
			LongIn:  1,
			LongOut: 1,
			Source:  SourceShipped,
		},
		{
			Key:     "gpt-5.4",
			Engine:  EngineCodex,
			Match:   []string{"gpt-5.4"},
			In:      2.5,
			Out:     15,
			Cached:  0.25,
			LongIn:  1,
			LongOut: 1,
			Source:  SourceOverride,
		},
	}}
	content, err := table.JSON()
	if err != nil {
		t.Fatal(err)
	}
	doc := decodeJSONDocument(t, content)
	wantClaude := []string{"engine", "hit", "in", "key", "long_in", "long_out", "match", "out", "source", "w1h", "w5m"}
	wantCodex := []string{"cached", "engine", "in", "key", "long_in", "long_out", "match", "out", "source"}
	if got := keysOf(doc.Rows[0]); !slices.Equal(got, wantClaude) {
		t.Errorf("claude row keys = %v, want %v", got, wantClaude)
	}
	if got := keysOf(doc.Rows[1]); !slices.Equal(got, wantCodex) {
		t.Errorf("codex row keys = %v, want %v", got, wantCodex)
	}
}

func TestJSONRejectsAnUnknownEngine(t *testing.T) {
	table := Table{Rows: []Row{{Key: "k", Engine: "gemini", Match: []string{"k"}}}}
	if _, err := table.JSON(); err == nil || !strings.Contains(err.Error(), "k") {
		t.Fatalf("JSON err = %v, want one naming the row", err)
	}
}

// TestJSONRoundTripsThroughTheDecoder feeds each emitted row, minus its
// report-only source, back through the table-file decoder.
func TestJSONRoundTripsThroughTheDecoder(t *testing.T) {
	shipped := shippedTable(t)
	overridden := shipped
	overridden.Rows = slices.Clone(shipped.Rows)
	overridden.Rows[4].Source = SourceOverride
	overridden.Override = &Override{Path: "/abs/dir/pfm.prices.json", Rows: 2}

	for _, tc := range []struct {
		name  string
		table Table
	}{
		{"without override", shipped},
		{"with override", overridden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content, err := tc.table.JSON()
			if err != nil {
				t.Fatal(err)
			}
			var doc struct {
				Version  int `json:"version"`
				Override *struct {
					Path string `json:"path"`
					Rows int    `json:"rows"`
				} `json:"override"`
				Rows []map[string]json.RawMessage `json:"rows"`
			}
			if err := json.Unmarshal(content, &doc); err != nil {
				t.Fatal(err)
			}
			if tc.table.Override == nil {
				if doc.Override != nil {
					t.Errorf("override = %+v, want null", doc.Override)
				}
			} else if doc.Override == nil || doc.Override.Path != tc.table.Override.Path || doc.Override.Rows != tc.table.Override.Rows {
				t.Errorf("override = %+v, want %+v", doc.Override, *tc.table.Override)
			}

			sources := make([]string, len(doc.Rows))
			stripped := make([]json.RawMessage, len(doc.Rows))
			for i, row := range doc.Rows {
				if err := json.Unmarshal(row["source"], &sources[i]); err != nil {
					t.Fatalf("row %d source: %v", i, err)
				}
				delete(row, "source")
				if stripped[i], err = json.Marshal(row); err != nil {
					t.Fatal(err)
				}
			}
			body, _ := json.Marshal(map[string]any{"version": doc.Version, "rows": stripped})
			rows, err := decodeTable(body, "round trip", "")
			if err != nil {
				t.Fatalf("decoder rejects the JSON output: %v", err)
			}
			if len(rows) != len(tc.table.Rows) {
				t.Fatalf("round trip gave %d rows, want %d", len(rows), len(tc.table.Rows))
			}
			for i, row := range rows {
				want := tc.table.Rows[i]
				if sources[i] != want.Source {
					t.Errorf("row %d (%s) source = %q, want %q", i, want.Key, sources[i], want.Source)
				}
				want.Source = ""
				if !reflect.DeepEqual(row, want) {
					t.Errorf("row %d round-trips to %+v, want %+v", i, row, want)
				}
			}
		})
	}
}

func TestClaudeCostPricesEveryColumnAtItsRate(t *testing.T) {
	row := Row{In: 4, Out: 20, Hit: 0.2, W5m: 5, W1h: 8, LongIn: 2, LongOut: 1.5}
	usage := ClaudeUsage{Input: 1_000_000, Output: 100_000, CacheRead: 2_000_000, Write5m: 200_000, Write1h: 50_000}
	// 4 + 2 + 0.4 + 1 + 0.4 at base rates; the long-context multipliers stay out.
	if got, want := row.ClaudeCost(usage), 7.8; math.Abs(got-want) > 1e-9 {
		t.Fatalf("ClaudeCost = %v, want %v", got, want)
	}
}
