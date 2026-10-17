package tokens

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoleMatchNamesOneJob(t *testing.T) {
	cases := []struct {
		left, right string
		want        bool
	}{
		{"flights-lander", "Flights Lander", true},
		{"code reviewer", "reviewer", true},
		{"pr reviewer", "plan reviewer", true},
		{"tracer", "flights-foreman", false},
		{"", "tracer", false},
		{"--", "tracer", false},
	}
	for _, c := range cases {
		if got := roleMatch(c.left, c.right); got != c.want {
			t.Errorf("roleMatch(%q, %q) = %v, want %v", c.left, c.right, got, c.want)
		}
	}
}

func TestWriteOutputReplacesTheFileAndCreatesItsDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a", "b", "metrics.md")
	if n, err := writeOutput(path, "old content"); err != nil || n != 11 {
		t.Fatalf("writeOutput = %d, %v", n, err)
	}
	if n, err := writeOutput(path, "new"); err != nil || n != 3 {
		t.Fatalf("writeOutput = %d, %v", n, err)
	}
	if content, err := os.ReadFile(path); err != nil || string(content) != "new" {
		t.Errorf("file holds %q, %v; want the whole file replaced", content, err)
	}
	blocked := filepath.Join(path, "under-a-file.md")
	if _, err := writeOutput(blocked, "x"); err == nil || !strings.HasPrefix(err.Error(), "write "+blocked+": ") {
		t.Errorf("writeOutput under a file = %v, want a write error naming the path", err)
	}
}

func TestLedgerRowNamesTheEngineFamilyAndReadsAUnitHeader(t *testing.T) {
	for _, c := range []struct {
		engine, want string
		ok           bool
	}{
		{"claude", "claude", true},
		{"claude-opus", "claude", true},
		{"codex-gpt", "codex", true},
		{"claude-opus", "codex", false},
		{"seat", "codex", true},
	} {
		if got := (ledgerRow{engine: c.engine}).names(c.want); got != c.ok {
			t.Errorf("ledgerRow{engine: %q}.names(%q) = %v, want %v", c.engine, c.want, got, c.ok)
		}
	}
	plan := &flightPlan{}
	tsv := "unit\ttype\tagent_id\tround\ttime\tengine\nA\tflights-foreman\ta9f1\t1\t2026-10-17T12:26:13Z\tclaude-opus\n"
	if err := plan.readLedger("agents.tsv", tsv, nil); err != nil {
		t.Fatal(err)
	}
	if len(plan.gaps) != 0 || len(plan.rows) != 1 || !plan.rows[0].names("claude") {
		t.Errorf("unit header ledger: gaps %v, rows %+v", plan.gaps, plan.rows)
	}
}
