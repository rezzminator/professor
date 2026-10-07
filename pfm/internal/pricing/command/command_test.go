package command

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/pricing"
)

// priceOverride replaces the shipped opus row and adds opus-6.
const priceOverride = `{"version": 1, "rows": [
  {"key": "opus", "engine": "claude", "match": ["opus"], "in": 7, "out": 35, "hit": 0.7, "w5m": 8.75, "w1h": 14, "long_in": 1, "long_out": 1},
  {"key": "opus-6", "engine": "claude", "match": ["opus-6"], "in": 6, "out": 30, "hit": 0.6, "w5m": 7.5, "w1h": 12, "long_in": 2, "long_out": 1.5}
]}`

// priceRuntime is a runtime whose pfm.config.json sits in a fresh temp dir;
// override, when non-empty, is written beside it as pfm.prices.json.
func priceRuntime(t *testing.T, override string) (pfmconfig.Runtime, string) {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), pfmconfig.FileName)
	pricesPath := pfmconfig.PricesPath(configPath)
	if override != "" {
		if err := os.WriteFile(pricesPath, []byte(override), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return pfmconfig.Runtime{Config: pfmconfig.Config{Path: configPath}}, pricesPath
}

func runPrice(runtime pfmconfig.Runtime, args ...string) (code int, stdout, stderr string) {
	var out, errs bytes.Buffer
	code = Price(args, &out, &errs, runtime)
	return code, out.String(), errs.String()
}

func effectiveTable(t *testing.T, runtime pfmconfig.Runtime) pricing.Table {
	t.Helper()
	table, err := pricing.Effective(runtime.Config.Path)
	if err != nil {
		t.Fatal(err)
	}
	return table
}

// tableLines splits the plain output into its header, column titles and rows,
// each row as its whitespace-separated fields.
func tableLines(
	t *testing.T,
	stdout string,
) (header string, titles []string, rows map[string][]string, order []string) {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("output too short:\n%s", stdout)
	}
	rows = map[string][]string{}
	for _, line := range lines[2:] {
		fields := strings.Fields(line)
		rows[fields[0]] = fields
		order = append(order, fields[0])
	}
	return lines[0], strings.Fields(lines[1]), rows, order
}

func TestPricePlainWithoutOverrideMarksEveryRowShipped(t *testing.T) {
	runtime, pricesPath := priceRuntime(t, "")
	table := effectiveTable(t, runtime)
	code, stdout, stderr := runPrice(runtime)
	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	header, titles, rows, order := tableLines(t, stdout)
	if want := fmt.Sprintf(
		"price table: %d rows · override: none (no %s)",
		len(table.Rows),
		pricesPath,
	); header != want {
		t.Errorf("header=%q, want %q", header, want)
	}
	if got := strings.Join(titles, " "); got != "KEY ENGINE MATCH IN OUT HIT CACHED W5M W1H LONG SOURCE" {
		t.Errorf("titles=%q", got)
	}
	if len(order) != len(table.Rows) {
		t.Fatalf("rows=%d, want %d", len(order), len(table.Rows))
	}
	for i, key := range order {
		if key != table.Rows[i].Key {
			t.Errorf("row %d key=%q, want %q (table order)", i, key, table.Rows[i].Key)
		}
		if source := rows[key][len(rows[key])-1]; source != "shipped" {
			t.Errorf("row %q source=%q, want shipped", key, source)
		}
	}
	if got := strings.Join(rows["opus"], " "); got != "opus claude opus 5 25 0.5 - 6.25 10 1x/1x shipped" {
		t.Errorf("claude row=%q", got)
	}
	if got := strings.Join(rows["gpt-5.4"], " "); got != "gpt-5.4 codex gpt-5.4 2.5 15 - 0.25 - - 1x/1x shipped" {
		t.Errorf("codex row=%q", got)
	}
	// Column-aligned: every title starts at the same offset as its row cells.
	lines := strings.Split(stdout, "\n")
	sourceAt := strings.Index(lines[1], "SOURCE")
	for _, line := range lines[2 : len(lines)-1] {
		if !strings.HasPrefix(line[sourceAt:], "shipped") {
			t.Errorf("SOURCE column misaligned at %d: %q", sourceAt, line)
		}
	}
}

func TestPricePlainWithOverrideMarksOverriddenRows(t *testing.T) {
	runtime, pricesPath := priceRuntime(t, priceOverride)
	table := effectiveTable(t, runtime)
	code, stdout, stderr := runPrice(runtime)
	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	header, _, rows, order := tableLines(t, stdout)
	if want := fmt.Sprintf(
		"price table: %d rows · override: %s (2 rows)",
		len(table.Rows),
		pricesPath,
	); header != want {
		t.Errorf("header=%q, want %q", header, want)
	}
	if len(order) != len(table.Rows) {
		t.Fatalf("rows=%d, want %d", len(order), len(table.Rows))
	}
	for key, fields := range rows {
		want := "shipped"
		if key == "opus" || key == "opus-6" {
			want = "override"
		}
		if source := fields[len(fields)-1]; source != want {
			t.Errorf("row %q source=%q, want %s", key, source, want)
		}
	}
	if got := strings.Join(rows["opus"], " "); got != "opus claude opus 7 35 0.7 - 8.75 14 1x/1x override" {
		t.Errorf("overridden row=%q", got)
	}
	if got := strings.Join(rows["opus-6"], " "); got != "opus-6 claude opus-6 6 30 0.6 - 7.5 12 2x/1.5x override" {
		t.Errorf("added row=%q", got)
	}
}

func TestPriceJSONIsTheTableDocument(t *testing.T) {
	for _, override := range []string{"", priceOverride} {
		runtime, _ := priceRuntime(t, override)
		want, err := effectiveTable(t, runtime).JSON()
		if err != nil {
			t.Fatal(err)
		}
		code, stdout, stderr := runPrice(runtime, "--json")
		if code != 0 || stderr != "" {
			t.Fatalf("override=%v code=%d stderr=%q", override != "", code, stderr)
		}
		if stdout != string(want) {
			t.Errorf("override=%v stdout differs from Table.JSON:\n%s", override != "", stdout)
		}
		if override == "" && !strings.Contains(stdout, `"override": null`) {
			t.Errorf("no override must read null:\n%s", stdout)
		}
	}
}

func TestPriceCheckReportsAValidTable(t *testing.T) {
	runtime, _ := priceRuntime(t, "")
	n := len(effectiveTable(t, runtime).Rows)
	code, stdout, stderr := runPrice(runtime, "--check")
	if want := fmt.Sprintf(
		"price table: ok · %d rows · override: none\n",
		n,
	); code != 0 || stdout != want ||
		stderr != "" {
		t.Errorf("code=%d stdout=%q stderr=%q, want %q", code, stdout, stderr, want)
	}
	runtime, pricesPath := priceRuntime(t, priceOverride)
	n = len(effectiveTable(t, runtime).Rows)
	code, stdout, stderr = runPrice(runtime, "--check")
	if want := fmt.Sprintf(
		"price table: ok · %d rows · override: %s (2 rows)\n",
		n,
		pricesPath,
	); code != 0 || stdout != want ||
		stderr != "" {
		t.Errorf("code=%d stdout=%q stderr=%q, want %q", code, stdout, stderr, want)
	}
}

func TestPriceInvalidOverrideFailsNamingThePath(t *testing.T) {
	overrides := map[string]string{
		"malformed":   `{"version": 1, "rows": [`,
		"invalid row": `{"version": 1, "rows": [{"key": "opus", "engine": "claude", "match": ["opus"], "in": -1, "out": 25, "hit": 0.5, "w5m": 6.25, "w1h": 10, "long_in": 1, "long_out": 1}]}`,
	}
	for name, override := range overrides {
		runtime, pricesPath := priceRuntime(t, override)
		for _, args := range [][]string{nil, {"--json"}, {"--check"}} {
			code, stdout, stderr := runPrice(runtime, args...)
			if code != 1 || stdout != "" {
				t.Errorf("%s %v: code=%d stdout=%q, want 1 and nothing", name, args, code, stdout)
			}
			if !strings.HasPrefix(stderr, "pfm price: ") || !strings.Contains(stderr, pricesPath) {
				t.Errorf("%s %v: stderr=%q, want pfm price: naming %s", name, args, stderr, pricesPath)
			}
		}
	}
}

func TestPriceUsageErrorsExitTwo(t *testing.T) {
	runtime, _ := priceRuntime(t, "")
	for _, args := range [][]string{{"--bogus"}, {"extra"}, {"--json", "extra"}, {"--json", "--check"}} {
		code, stdout, stderr := runPrice(runtime, args...)
		if code != 2 || stdout != "" || !strings.Contains(stderr, usage) {
			t.Errorf("%v: code=%d stdout=%q stderr=%q, want 2 with usage on stderr", args, code, stdout, stderr)
		}
	}
	_, _, stderr := runPrice(runtime, "--json", "--check")
	if !strings.Contains(stderr, "pfm price: --json and --check are exclusive") {
		t.Errorf("stderr=%q lacks the exclusive message", stderr)
	}
}

func TestPriceHelpPrintsUsageOnStdout(t *testing.T) {
	runtime, _ := priceRuntime(t, "")
	for _, arg := range []string{"help", "-h", "--help"} {
		code, stdout, stderr := runPrice(runtime, arg)
		if code != 0 || stdout != usage+"\n" || stderr != "" {
			t.Errorf("%s: code=%d stdout=%q stderr=%q", arg, code, stdout, stderr)
		}
	}
}
