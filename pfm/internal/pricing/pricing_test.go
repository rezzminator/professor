package pricing

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func ptr(value float64) *float64 { return &value }

// resolveCases is testdata/resolve-cases.json, the resolution cases the
// /tokens mirror of Resolve reads too.
type resolveCases struct {
	Keys  []string `json:"keys"`
	Cases []struct {
		ID  string  `json:"id"`
		Key *string `json:"key"`
	} `json:"cases"`
}

func TestResolveSharedCases(t *testing.T) {
	content, err := os.ReadFile("testdata/resolve-cases.json")
	if err != nil {
		t.Fatalf("read resolve cases: %v", err)
	}
	var cases resolveCases
	if err := json.Unmarshal(content, &cases); err != nil {
		t.Fatalf("decode resolve cases: %v", err)
	}
	if len(cases.Keys) == 0 || len(cases.Cases) == 0 {
		t.Fatalf("resolve cases: %d keys, %d cases, want both non-empty", len(cases.Keys), len(cases.Cases))
	}
	table := Table{}
	for _, key := range cases.Keys {
		engine := EngineCodex
		if strings.HasPrefix(key, "claude-") {
			engine = EngineClaude
		}
		table.Rows = append(table.Rows, Row{Key: key, Engine: engine, Rates: Rates{In: ptr(1), Out: ptr(2)}})
	}
	for _, tc := range cases.Cases {
		row, ok := table.Resolve(tc.ID)
		switch {
		case tc.Key == nil && ok:
			t.Errorf("Resolve(%q) = %q, want unpriced", tc.ID, row.Key)
		case tc.Key != nil && (!ok || row.Key != *tc.Key):
			t.Errorf("Resolve(%q) = %q, %v; want %q", tc.ID, row.Key, ok, *tc.Key)
		}
	}
}

const validTable = `{
  "version": 2,
  "fetched_at": "2026-10-07T23:02:05Z",
  "sources": [
    {"provider":"anthropic","url":"https://platform.claude.com/docs/en/about-claude/pricing.md"},
    {"provider":"openai","url":"https://developers.openai.com/api/docs/pricing.md"}
  ],
  "rows": [
    {"key":"claude-haiku-5-5","engine":"claude","in":0.1,"out":0.5,"hit":0.01,"w5m":0.125,"w1h":0.2,"long":{"above":100000,"in":0.5,"out":2.5,"hit":0.05,"w5m":0.625,"w1h":1}},
    {"key":"claude-opus-5-5","engine":"claude","in":4,"out":20,"hit":0.2,"w5m":5,"w1h":8},
    {"key":"gpt-5.5","engine":"codex","in":5,"out":30,"cached":0.5,"long":{"above":272000,"in":10,"out":45,"cached":1}},
    {"key":"gpt-5.5-pro","engine":"codex","in":30,"out":180,"long":{"above":272000}}
  ]
}
`

func TestDecodeAcceptsBothEnginesAndLongTiers(t *testing.T) {
	table, err := DecodeTable([]byte(validTable), "fixture")
	if err != nil {
		t.Fatalf("DecodeTable: %v", err)
	}
	if table.Version != Version || table.FetchedAt != "2026-10-07T23:02:05Z" || len(table.Sources) != 2 ||
		table.Sources[1] != (Source{Provider: "openai", URL: "https://developers.openai.com/api/docs/pricing.md"}) {
		t.Fatalf("header = %d %q %+v", table.Version, table.FetchedAt, table.Sources)
	}
	if len(table.Rows) != 4 {
		t.Fatalf("rows = %d, want 4", len(table.Rows))
	}
	haiku, gpt, pro := table.Rows[0], table.Rows[2], table.Rows[3]
	if haiku.Long == nil || haiku.Long.Above != 100000 || *haiku.Long.W1h != 1 || *haiku.Hit != 0.01 {
		t.Fatalf("claude long row = %+v long=%+v", haiku, haiku.Long)
	}
	if gpt.Engine != EngineCodex || *gpt.Cached != 0.5 || gpt.Hit != nil || *gpt.Long.Cached != 1 {
		t.Fatalf("codex row = %+v long=%+v", gpt, gpt.Long)
	}
	if pro.Cached != nil || pro.Long == nil || pro.Long.In != nil || pro.Long.Above != 272000 {
		t.Fatalf("an unpublished rate must stay nil, never 0: %+v long=%+v", pro, pro.Long)
	}
}

func TestDecodeRejects(t *testing.T) {
	header := `"version":2,"fetched_at":"2026-10-07T23:02:05Z","sources":[{"provider":"anthropic","url":"https://x.invalid/p.md"}]`
	doc := func(rows string) string { return "{" + header + `,"rows":[` + rows + "]}" }
	for _, tc := range []struct{ name, doc, want string }{
		{"not json", "{", "fixture"},
		{"version 1", strings.Replace(doc(""), `"version":2`, `"version":1`, 1), "version must be 2, got 1"},
		{"missing version", `{"fetched_at":"2026-10-07T23:02:05Z","sources":[],"rows":[]}`, "version must be 2"},
		{"missing fetched_at", strings.Replace(doc(""), `"fetched_at":"2026-10-07T23:02:05Z",`, "", 1), "fetched_at"},
		{"bad fetched_at", strings.Replace(doc(""), "2026-10-07T23:02:05Z", "yesterday", 1), "fetched_at"},
		{"no sources", strings.Replace(doc(""), `[{"provider":"anthropic","url":"https://x.invalid/p.md"}]`, "[]", 1), "sources"},
		{"source without url", strings.Replace(doc(""), `"url":"https://x.invalid/p.md"`, `"url":""`, 1), "source"},
		{"missing rows", "{" + header + "}", "rows"},
		{"unknown field", doc(`{"key":"gpt-x","engine":"codex","in":1,"out":2,"match":["x"]}`), "unknown field"},
		{"missing in", doc(`{"key":"gpt-x","engine":"codex","out":2}`), `gpt-x: in`},
		{"missing out", doc(`{"key":"gpt-x","engine":"codex","in":1}`), `gpt-x: out`},
		{"negative rate", doc(`{"key":"gpt-x","engine":"codex","in":-1,"out":2}`), "gpt-x: in"},
		{"claude row with cached", doc(`{"key":"claude-x","engine":"claude","in":1,"out":2,"cached":1}`), "claude-x: cached"},
		{"codex row with hit", doc(`{"key":"gpt-x","engine":"codex","in":1,"out":2,"hit":1}`), "gpt-x: hit"},
		{"long foreign column", doc(`{"key":"gpt-x","engine":"codex","in":1,"out":2,"long":{"above":5,"w5m":1}}`), "gpt-x: long w5m"},
		{"long above zero", doc(`{"key":"gpt-x","engine":"codex","in":1,"out":2,"long":{"above":0}}`), "gpt-x: long above"},
		{"duplicate key", doc(`{"key":"gpt-x","engine":"codex","in":1,"out":2},{"key":"gpt-x","engine":"codex","in":1,"out":2}`), "duplicate key gpt-x"},
		{"uppercase key", doc(`{"key":"Claude-X","engine":"claude","in":1,"out":2}`), "key"},
		{"empty key", doc(`{"key":"","engine":"codex","in":1,"out":2}`), "key"},
		{"unknown engine", doc(`{"key":"gpt-x","engine":"gemini","in":1,"out":2}`), "engine"},
		{"row source in a file", doc(`{"key":"gpt-x","engine":"codex","in":1,"out":2,"source":"published"}`), "source"},
		{"second value", doc("") + "{}", "fixture"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeTable([]byte(tc.doc), "fixture")
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "fixture") {
				t.Fatalf("DecodeTable error = %v, want one naming fixture and %q", err, tc.want)
			}
		})
	}
}

func TestEncodeSortsClaudeFirstOneRowPerLineAndRoundTrips(t *testing.T) {
	table := Table{
		Version:   Version,
		FetchedAt: "2026-10-08T01:00:00Z",
		Sources:   []Source{{Provider: "anthropic", URL: "https://a.invalid/p.md"}},
		Rows: []Row{
			{Key: "gpt-5.5", Engine: EngineCodex, Rates: Rates{In: ptr(5), Out: ptr(30), Cached: ptr(0.5)}},
			{Key: "claude-sonnet-5", Engine: EngineClaude, Rates: Rates{In: ptr(3), Out: ptr(15)}},
			{
				Key: "claude-haiku-5-5", Engine: EngineClaude, Rates: Rates{In: ptr(0.1), Out: ptr(0.5)},
				Long: &Long{Above: 100000, Rates: Rates{In: ptr(0.5)}},
			},
		},
	}
	content, err := Encode(table)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	want := `{
  "version": 2,
  "fetched_at": "2026-10-08T01:00:00Z",
  "sources": [
    {"provider":"anthropic","url":"https://a.invalid/p.md"}
  ],
  "rows": [
    {"key":"claude-haiku-5-5","engine":"claude","in":0.1,"out":0.5,"long":{"above":100000,"in":0.5}},
    {"key":"claude-sonnet-5","engine":"claude","in":3,"out":15},
    {"key":"gpt-5.5","engine":"codex","in":5,"out":30,"cached":0.5}
  ]
}
`
	if string(content) != want {
		t.Fatalf("Encode =\n%s\nwant\n%s", content, want)
	}
	back, err := DecodeTable(content, "encoded")
	if err != nil {
		t.Fatalf("DecodeTable(Encode): %v", err)
	}
	if !SameRates(back, table) || back.FetchedAt != table.FetchedAt {
		t.Fatalf("round trip = %+v, want %+v", back, table)
	}
	if _, err := Encode(Table{Version: Version, FetchedAt: "now", Sources: table.Sources}); err == nil {
		t.Fatal("Encode of an invalid table succeeded, want the validation error")
	}
}

func TestSameRatesIgnoresOnlyTheFetchTime(t *testing.T) {
	base, err := DecodeTable([]byte(validTable), "fixture")
	if err != nil {
		t.Fatal(err)
	}
	clone := func() Table {
		copied, err := DecodeTable([]byte(validTable), "fixture")
		if err != nil {
			t.Fatal(err)
		}
		return copied
	}
	later := clone()
	later.FetchedAt = "2027-01-01T00:00:00Z"
	reordered := clone()
	reordered.Rows[0], reordered.Rows[3] = reordered.Rows[3], reordered.Rows[0]
	if !SameRates(base, later) || !SameRates(base, reordered) {
		t.Fatal("SameRates must ignore fetched_at and row order")
	}
	rate := clone()
	*rate.Rows[1].Out = 21
	source := clone()
	source.Sources[0].URL = "https://moved.invalid/p.md"
	dropped := clone()
	dropped.Rows[2].Long = nil
	for name, other := range map[string]Table{"rate": rate, "source": source, "long tier": dropped} {
		if SameRates(base, other) {
			t.Errorf("SameRates ignored a changed %s", name)
		}
	}
}

func TestRatesAtSelectsTheLongTierAboveItsThreshold(t *testing.T) {
	row := Row{
		Key: "claude-haiku-5-5", Engine: EngineClaude, Rates: Rates{In: ptr(0.1), Out: ptr(0.5)},
		Long: &Long{Above: 100000, Rates: Rates{In: ptr(0.5), Out: ptr(2.5)}},
	}
	if got := row.RatesAt(100000); *got.In != 0.1 {
		t.Fatalf("RatesAt(100000).In = %v, want the base 0.1 at the threshold", *got.In)
	}
	if got := row.RatesAt(100001); *got.In != 0.5 || *got.Out != 2.5 {
		t.Fatalf("RatesAt(100001) = %v/%v, want the long 0.5/2.5", *got.In, *got.Out)
	}
	flat := Row{Key: "claude-opus-5-5", Engine: EngineClaude, Rates: Rates{In: ptr(4), Out: ptr(20)}}
	if got := flat.RatesAt(5_000_000); *got.In != 4 {
		t.Fatalf("a row without a long tier priced %v, want its base 4", *got.In)
	}
}

func TestClaudeCost(t *testing.T) {
	opus := Row{
		Key: "claude-opus-5-5", Engine: EngineClaude,
		Rates: Rates{In: ptr(4), Out: ptr(20), Hit: ptr(0.2), W5m: ptr(5), W1h: ptr(8)},
	}
	haiku := Row{
		Key: "claude-haiku-5-5", Engine: EngineClaude,
		Rates: Rates{In: ptr(0.1), Out: ptr(0.5), Hit: ptr(0.01), W5m: ptr(0.125), W1h: ptr(0.2)},
		Long:  &Long{Above: 100000, Rates: Rates{In: ptr(0.5), Out: ptr(2.5), Hit: ptr(0.05), W5m: ptr(0.625)}},
	}
	noHit := Row{Key: "claude-x", Engine: EngineClaude, Rates: Rates{In: ptr(1), Out: ptr(2)}}
	codex := Row{Key: "gpt-5.5", Engine: EngineCodex, Rates: Rates{In: ptr(5), Out: ptr(30)}}
	for _, tc := range []struct {
		name  string
		row   Row
		usage ClaudeUsage
		want  float64
		ok    bool
	}{
		{"every column at its rate", opus, ClaudeUsage{1e6, 1e6, 1e6, 1e6, 1e6}, 4 + 20 + 0.2 + 5 + 8, true},
		{"base tier at the threshold", haiku, ClaudeUsage{Input: 100000, Output: 1e6}, 0.01 + 0.5, true},
		{"long tier above it", haiku, ClaudeUsage{Input: 50000, CacheRead: 50001, Output: 1e6}, 0.025 + 0.0025 + 2.5, true},
		{"long tier lacks w1h", haiku, ClaudeUsage{Input: 100000, Write1h: 1}, 0, false},
		{"unpublished rate used", noHit, ClaudeUsage{Input: 1, CacheRead: 1}, 0, false},
		{"unpublished rate unused", noHit, ClaudeUsage{Input: 1e6, Output: 1e6}, 3, true},
		{"codex row", codex, ClaudeUsage{Input: 1}, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := tc.row.ClaudeCost(tc.usage)
			if ok != tc.ok || (ok && (got < tc.want-1e-6 || got > tc.want+1e-6)) {
				t.Fatalf("ClaudeCost = %v, %v; want %v, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}
