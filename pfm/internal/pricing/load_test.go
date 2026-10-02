package pricing

import (
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
)

// bc538270Pricing is PRICING at bc538270 (token-audit.mjs), row for row, as
// [substring, input, output, hit-or-cached, long-context input, long-context output],
// plus the row's match patterns, where nil means [substring]. One deliberate
// departure: the opus-4-20 row also matches opus-4-0, because bc538270 priced the
// claude-opus-4-0 alias at the opus catch-all instead of Opus 4's rates.
var bc538270Pricing = []struct {
	sub                            string
	in, out, rate, longIn, longOut float64
	match                          []string
}{
	{"opus-4-1", 15.0, 75.0, 1.5, 2, 1.5, nil},
	{
		sub:     "opus-4-20",
		in:      15.0,
		out:     75.0,
		rate:    1.5,
		longIn:  2,
		longOut: 1.5,
		match:   []string{"opus-4-20", "opus-4-0"},
	},
	{"opus-4@", 15.0, 75.0, 1.5, 2, 1.5, nil},
	{"opus-5-5", 4.0, 20.0, 0.2, 1, 1, nil},
	{"opus", 5.0, 25.0, 0.5, 1, 1, nil},
	{"sonnet-4-6", 3.0, 15.0, 0.3, 1, 1, nil},
	{"sonnet-4", 3.0, 15.0, 0.3, 2, 1.5, nil},
	{"sonnet-5-5", 2.0, 10.0, 0.2, 1, 1, nil},
	{"sonnet-5", 2.0, 10.0, 0.2, 1, 1, nil},
	{"sonnet", 3.0, 15.0, 0.3, 2, 1.5, nil},
	{"haiku-4-5", 1.0, 5.0, 0.1, 2, 1.5, nil},
	{"haiku", 0.8, 4.0, 0.08, 2, 1.5, nil},
	{"fable-5-1", 10.0, 50.0, 0.25, 1, 1, nil},
	{"mythos-5-1", 10.0, 50.0, 0.25, 1, 1, nil},
	{"fable", 10.0, 50.0, 1.0, 1, 1, nil},
	{"mythos", 10.0, 50.0, 1.0, 1, 1, nil},
	{"gpt-6-astra", 10.0, 50.0, 1.0, 1, 1, nil},
	{"gpt-6.1-sol", 2.0, 10.0, 0.1, 1, 1, nil},
	{"gpt-6-sol", 2.0, 10.0, 0.2, 1, 1, nil},
	{"gpt-5.6-sol", 4.0, 20.0, 0.4, 1, 1, nil},
	{"gpt-5.6-luna", 0.2, 1.2, 0.02, 1, 1, nil},
	{"gpt-5.6-terra", 2.0, 12.0, 0.2, 1, 1, nil},
	{"gpt-5.4", 2.5, 15.0, 0.25, 1, 1, nil},
	{"gpt-5.3-codex", 1.75, 14.0, 0.175, 1, 1, nil},
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-12 }

func TestShippedMatchesBc538270PricingRowForRow(t *testing.T) {
	table := shippedTable(t)
	if table.Override != nil {
		t.Errorf("shipped Override = %+v, want nil", table.Override)
	}
	if len(table.Rows) != 24 || len(bc538270Pricing) != 24 {
		t.Fatalf("shipped rows = %d (expected table %d), want 24", len(table.Rows), len(bc538270Pricing))
	}
	for i, want := range bc538270Pricing {
		row := table.Rows[i]
		fail := func(field string, got, wantValue any) {
			t.Errorf("row %d (%s): %s = %v, want %v", i, want.sub, field, got, wantValue)
		}
		if row.Key != want.sub {
			fail("key", row.Key, want.sub)
		}
		wantMatch := want.match
		if wantMatch == nil {
			wantMatch = []string{want.sub}
		}
		if !reflect.DeepEqual(row.Match, wantMatch) {
			fail("match", row.Match, wantMatch)
		}
		if row.Source != SourceShipped {
			fail("source", row.Source, SourceShipped)
		}
		for field, pair := range map[string][2]float64{"in": {row.In, want.in}, "out": {row.Out, want.out}, "long_in": {row.LongIn, want.longIn}, "long_out": {row.LongOut, want.longOut}} {
			if pair[0] != pair[1] {
				fail(field, pair[0], pair[1])
			}
		}
		if strings.HasPrefix(want.sub, "gpt-") {
			if row.Engine != EngineCodex {
				fail("engine", row.Engine, EngineCodex)
			}
			if row.Cached != want.rate {
				fail("cached", row.Cached, want.rate)
			}
			if row.Hit != 0 || row.W5m != 0 || row.W1h != 0 {
				fail("claude columns", []float64{row.Hit, row.W5m, row.W1h}, "zeros")
			}
			continue
		}
		if row.Engine != EngineClaude {
			fail("engine", row.Engine, EngineClaude)
		}
		if row.Hit != want.rate {
			fail("hit", row.Hit, want.rate)
		}
		if !near(row.W5m, want.in*1.25) {
			fail("w5m", row.W5m, want.in*1.25)
		}
		if !near(row.W1h, want.in*2) {
			fail("w1h", row.W1h, want.in*2)
		}
		if row.Cached != 0 {
			fail("cached", row.Cached, 0)
		}
	}
}

func writeOverride(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), config.PricesFileName)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func loadOverride(t *testing.T, content string) Table {
	t.Helper()
	table, err := WithOverride(writeOverride(t, content))
	if err != nil {
		t.Fatalf("WithOverride: %v", err)
	}
	return table
}

func rowIndex(t *testing.T, table Table, key string) int {
	t.Helper()
	for i := range table.Rows {
		if table.Rows[i].Key == key {
			return i
		}
	}
	t.Fatalf("no row with key %q", key)
	return -1
}

func TestWithOverrideWithNoOverrideFileGivesTheShippedTable(t *testing.T) {
	table, err := WithOverride(filepath.Join(t.TempDir(), config.PricesFileName))
	if err != nil {
		t.Fatal(err)
	}
	if table.Override != nil {
		t.Errorf("Override = %+v, want nil", table.Override)
	}
	if !reflect.DeepEqual(table.Rows, shippedTable(t).Rows) {
		t.Error("rows differ from the shipped table")
	}
	for _, row := range table.Rows {
		if row.Source != SourceShipped {
			t.Errorf("row %s: source %q, want %q", row.Key, row.Source, SourceShipped)
		}
	}
}

func TestEffectiveWithNoConfigPathIsShippedOnly(t *testing.T) {
	table, err := Effective("")
	if err != nil {
		t.Fatal(err)
	}
	if table.Override != nil || !reflect.DeepEqual(table.Rows, shippedTable(t).Rows) {
		t.Fatalf("Effective(\"\") = %d rows with override %+v, want the shipped table", len(table.Rows), table.Override)
	}
}

func TestEffectiveReadsTheOverrideBesideTheConfigFile(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, config.FileName)
	body := tableDoc(
		`{"key":"opus-6","engine":"claude","match":["opus-6"],"in":6,"out":30,"hit":0.6,"w5m":7.5,"w1h":12,"long_in":1,"long_out":1}`,
	)
	if err := os.WriteFile(config.PricesPath(configPath), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	table, err := Effective(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if table.Override == nil || table.Override.Path != config.PricesPath(configPath) || table.Override.Rows != 1 {
		t.Fatalf("Override = %+v, want the file beside the config with 1 row", table.Override)
	}
	if row, ok := table.Resolve("claude-opus-6"); !ok || row.Key != "opus-6" {
		t.Fatalf("Resolve(claude-opus-6) = (%q, %v), want opus-6", row.Key, ok)
	}
}

func TestEffectiveWithoutAPricesFileIsShipped(t *testing.T) {
	table, err := Effective(filepath.Join(t.TempDir(), config.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if table.Override != nil || !reflect.DeepEqual(table.Rows, shippedTable(t).Rows) {
		t.Fatalf("want the shipped table, got %d rows with override %+v", len(table.Rows), table.Override)
	}
}

func TestOverrideReplacesAShippedRowInPlace(t *testing.T) {
	shipped := shippedTable(t)
	index := rowIndex(t, shipped, "opus")
	table := loadOverride(
		t,
		tableDoc(
			`{"key":"opus","engine":"claude","match":["opus"],"in":7,"out":35,"hit":0.7,"w5m":8.75,"w1h":14,"long_in":1,"long_out":1}`,
		),
	)

	if len(table.Rows) != len(shipped.Rows) {
		t.Fatalf("rows = %d, want %d", len(table.Rows), len(shipped.Rows))
	}
	want := Row{
		Key:     "opus",
		Engine:  EngineClaude,
		Match:   []string{"opus"},
		In:      7,
		Out:     35,
		Hit:     0.7,
		W5m:     8.75,
		W1h:     14,
		LongIn:  1,
		LongOut: 1,
		Source:  SourceOverride,
	}
	if !reflect.DeepEqual(table.Rows[index], want) {
		t.Errorf("row %d = %+v, want %+v", index, table.Rows[index], want)
	}
	for i, row := range table.Rows {
		if i != index && !reflect.DeepEqual(row, shipped.Rows[i]) {
			t.Errorf("row %d (%s) changed: %+v, want %+v", i, row.Key, row, shipped.Rows[i])
		}
	}
	if row, ok := table.Resolve("claude-opus-5"); !ok || row.In != 7 {
		t.Errorf("Resolve(claude-opus-5) = (%+v, %v), want the overridden rates", row, ok)
	}
}

func TestOverrideAddsAppendedRowsInFileOrder(t *testing.T) {
	shipped := shippedTable(t)
	opus6 := `{"key":"opus-6","engine":"claude","match":["opus-6"],"in":6,"out":30,"hit":0.6,"w5m":7.5,"w1h":12,"long_in":1,"long_out":1}`
	gptX := `{"key":"gpt-9","engine":"codex","match":["gpt-9"],"in":3,"out":9,"cached":0.3,"long_in":1,"long_out":1}`
	table := loadOverride(t, tableDoc(opus6, gptX))

	n := len(shipped.Rows)
	if len(table.Rows) != n+2 {
		t.Fatalf("rows = %d, want %d", len(table.Rows), n+2)
	}
	if !reflect.DeepEqual(table.Rows[:n], shipped.Rows) {
		t.Error("shipped rows changed by an append-only override")
	}
	for i, key := range []string{"opus-6", "gpt-9"} {
		row := table.Rows[n+i]
		if row.Key != key || row.Source != SourceOverride {
			t.Errorf("row %d = key %q source %q, want %q %q", n+i, row.Key, row.Source, key, SourceOverride)
		}
	}
	if row, ok := table.Resolve("claude-opus-6"); !ok || row.Key != "opus-6" {
		t.Errorf("Resolve(claude-opus-6) = (%q, %v), want opus-6", row.Key, ok)
	}
	if row, ok := shipped.Resolve("claude-opus-6"); !ok || row.Key != "opus" {
		t.Errorf("without the override claude-opus-6 resolves to (%q, %v), want opus", row.Key, ok)
	}
}

func TestOverrideIsReported(t *testing.T) {
	opus := `{"key":"opus","engine":"claude","match":["opus"],"in":7,"out":35,"hit":0.7,"w5m":8.75,"w1h":14,"long_in":1,"long_out":1}`
	opus6 := `{"key":"opus-6","engine":"claude","match":["opus-6"],"in":6,"out":30,"hit":0.6,"w5m":7.5,"w1h":12,"long_in":1,"long_out":1}`
	path := writeOverride(t, tableDoc(opus, opus6))
	table, err := WithOverride(path)
	if err != nil {
		t.Fatal(err)
	}
	if table.Override == nil || *table.Override != (Override{Path: path, Rows: 2}) {
		t.Fatalf("Override = %+v, want {%s 2}", table.Override, path)
	}
}

func TestOverridePathIsAbsolute(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(dir, config.PricesFileName),
		[]byte(`{"version":1,"rows":[]}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	table, err := WithOverride(config.PricesFileName)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, config.PricesFileName)
	if table.Override == nil || table.Override.Path != want {
		t.Fatalf("Override = %+v, want path %s", table.Override, want)
	}
}

func TestEmptyOverrideIsValid(t *testing.T) {
	table := loadOverride(t, `{"version":1,"rows":[]}`)
	if table.Override == nil || table.Override.Rows != 0 {
		t.Fatalf("Override = %+v, want 0 rows", table.Override)
	}
	if !reflect.DeepEqual(table.Rows, shippedTable(t).Rows) {
		t.Error("an empty override changed the rows")
	}
}

func TestOverrideDuplicatePatternAfterTheMerge(t *testing.T) {
	t.Run("a new key reusing a shipped pattern", func(t *testing.T) {
		path := writeOverride(
			t,
			tableDoc(
				`{"key":"my-opus","engine":"claude","match":["OPUS"],"in":1,"out":1,"hit":1,"w5m":1,"w1h":1,"long_in":1,"long_out":1}`,
			),
		)
		_, err := WithOverride(path)
		if err == nil {
			t.Fatal("want an error")
		}
		for _, want := range []string{path, "my-opus", `"opus"`, "pattern"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not name %q", err, want)
			}
		}
	})
	t.Run("a replaced row frees its old pattern", func(t *testing.T) {
		moved := `{"key":"opus","engine":"claude","match":["opus-x"],"in":1,"out":1,"hit":1,"w5m":1,"w1h":1,"long_in":1,"long_out":1}`
		taker := `{"key":"opus-y","engine":"claude","match":["opus"],"in":2,"out":2,"hit":2,"w5m":2,"w1h":2,"long_in":1,"long_out":1}`
		table := loadOverride(t, tableDoc(moved, taker))
		if row, ok := table.Resolve("claude-opus-5"); !ok || row.Key != "opus-y" {
			t.Fatalf("Resolve(claude-opus-5) = (%q, %v), want opus-y", row.Key, ok)
		}
	})
}

func TestWithOverrideErrorsNameTheOverridePath(t *testing.T) {
	opus := claudeRow
	cases := []struct {
		name    string
		content string
		wants   []string
	}{
		{"malformed JSON", `{`, nil},
		{"version 2", `{"version":2,"rows":[]}`, []string{"version must be 1, got 2"}},
		{"unknown field", tableDoc(strings.TrimSuffix(opus, "}") + `,"w5":1}`), []string{"row 0", "opus", "w5"}},
		{"missing field", tableDoc(strings.Replace(opus, `"w1h":10,`, "", 1)), []string{"opus", "w1h"}},
		{"duplicate key", tableDoc(opus, opus), []string{"opus"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeOverride(t, tc.content)
			table, err := WithOverride(path)
			if err == nil {
				t.Fatalf("WithOverride = %d rows, want an error", len(table.Rows))
			}
			for _, want := range append([]string{path}, tc.wants...) {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not name %q", err, want)
				}
			}
		})
	}
}

func TestWithOverrideUnreadableOverrideIsAnError(t *testing.T) {
	t.Run("a directory", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), config.PricesFileName)
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		_, err := WithOverride(path)
		if err == nil || !strings.Contains(err.Error(), path) {
			t.Fatalf("WithOverride err = %v, want one naming %s", err, path)
		}
		if errors.Is(err, fs.ErrNotExist) {
			t.Fatal("a directory must not read as a missing file")
		}
	})
	t.Run("a parent that is a file", func(t *testing.T) {
		parent := filepath.Join(t.TempDir(), "parent")
		if err := os.WriteFile(parent, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(parent, config.PricesFileName)
		_, err := WithOverride(path)
		if err == nil || !strings.Contains(err.Error(), path) {
			t.Fatalf("WithOverride err = %v, want one naming %s", err, path)
		}
	})
	if os.Geteuid() != 0 {
		t.Run("a file without read permission", func(t *testing.T) {
			path := writeOverride(t, `{"version":1,"rows":[]}`)
			if err := os.Chmod(path, 0); err != nil {
				t.Fatal(err)
			}
			_, err := WithOverride(path)
			if err == nil || !strings.Contains(err.Error(), path) {
				t.Fatalf("WithOverride err = %v, want one naming %s", err, path)
			}
		})
	}
}
