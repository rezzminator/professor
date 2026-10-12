package tokens

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestQuantileAndBasename(t *testing.T) {
	quantiles := []struct {
		values []float64
		p      float64
		want   float64
	}{
		{nil, 0.5, 0},
		{[]float64{3, 1, 2}, 0.5, 2},
		{[]float64{3, 1, 2}, 0, 1},
		{[]float64{3, 1, 2}, 1, 3},
		{[]float64{4, 1, 3, 2}, 0.5, 3},
	}
	for _, c := range quantiles {
		if got := quantile(c.values, c.p); got != c.want {
			t.Errorf("quantile(%v, %v) = %v, want %v", c.values, c.p, got, c.want)
		}
	}
	for path, want := range map[string]string{"": "", "/": "", "/w/demo/": "demo", "a/b.go": "b.go", "c": "c"} {
		if got := jsBasename(path); got != want {
			t.Errorf("jsBasename(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestCategoryMixNamesTheTopSharesShort(t *testing.T) {
	var cats [categoryCount]float64
	cats[catPrompt], cats[catOwn], cats[catPrefix] = 30, 60, 10
	if got, want := categoryMix(cats, 100, 2), "own earlier output 60.0% · prompts + briefs 30.0%"; got != want {
		t.Errorf("categoryMix = %q, want %q", got, want)
	}
	if got, want := categoryMix(cats, 0, 1), "own earlier output 0.0%"; got != want {
		t.Errorf("categoryMix over a zero total = %q, want %q", got, want)
	}
}

func TestOutWritesTheFullDataInItsMemberOrder(t *testing.T) {
	root := filepath.Join(t.TempDir(), "projects")
	writeTranscript(t, filepath.Join(root, "-w-demo", "s1.jsonl"),
		assistant("m1", "claude-sonnet-5", "2026-09-21T05:00:00Z", 1000, 100, 2000, 400))
	out := filepath.Join(t.TempDir(), "nested", "audit.json")
	code, stdout, stderr := auditWith(t, Options{ClaudeRoots: []string{root}},
		"--prices", fixture(t, "model-cost.json"), "--out", out)
	if code != 0 {
		t.Fatalf("exit %d stderr %q", code, stderr)
	}
	if !strings.Contains(stdout, "\nfull data → "+out+" (0.0 MB)") {
		t.Errorf("stdout lacks the full-data line:\n%s", stdout)
	}
	content, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	if _, err := decoder.Token(); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, key.(string))
		var skip json.RawMessage
		if err := decoder.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{
		"v", "pricing", "host", "at", "hours", "scan", "total", "usd", "tok", "calls", "cats", "bands",
		"modelEffort", "rewrites", "rewriteTool", "tools", "bash", "attach", "hourly", "repeats", "rereads", "landings",
		"byProject", "families", "groups", "runs",
	}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("members %v, want %v", keys, want)
	}
	var document struct {
		Host  string `json:"host"`
		Calls int    `json:"calls"`
		Runs  []any  `json:"runs"`
	}
	if err := json.Unmarshal(content, &document); err != nil {
		t.Fatal(err)
	}
	if document.Host != "test-host" || document.Calls != 1 || len(document.Runs) != 1 {
		t.Errorf("host %q calls %d runs %d; want test-host, 1, 1", document.Host, document.Calls, len(document.Runs))
	}
}
