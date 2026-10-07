package main

import (
	"bytes"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
)

// TestPriceCommandReadsTheOverrideBesideTheConfig proves `pfm price` dispatches
// to internal/pricing/command with the runtime it resolved: the override beside
// the jail's --config file is the one merged.
func TestPriceCommandReadsTheOverrideBesideTheConfig(t *testing.T) {
	root := jailTest(t)
	configPath := writeConfigFixture(t, root, `{"version":1}`)
	pricesPath := config.PricesPath(configPath)
	override := `{"version": 1, "rows": [{"key": "opus-6", "engine": "claude", "match": ["opus-6"], "in": 6, "out": 30, "hit": 0.6, "w5m": 7.5, "w1h": 12, "long_in": 1, "long_out": 1}]}`
	if err := os.WriteFile(pricesPath, []byte(override), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	if code := run([]string{"--config", configPath, "price", "--json"}, &out, &errs); code != 0 {
		t.Fatalf("price --json code=%d stderr=%q", code, errs.String())
	}
	for _, want := range []string{`"key": "opus-6"`, `"source": "override"`, `"path": "` + pricesPath + `"`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("price --json lacks %q:\n%s", want, out.String())
		}
	}
	out.Reset()
	errs.Reset()
	if code := run([]string{"--config", configPath, "price", "--check"}, &out, &errs); code != 0 {
		t.Fatalf("price --check code=%d stderr=%q", code, errs.String())
	}
	if want := "override: " + pricesPath + " (1 rows)\n"; !strings.HasPrefix(out.String(), "price table: ok · ") ||
		!strings.HasSuffix(out.String(), want) {
		t.Errorf("price --check=%q, want ok ending %q", out.String(), want)
	}
	if !slices.Contains(topLevelSubcommands, "price") {
		t.Error("topLevelSubcommands lacks price")
	}
	out.Reset()
	if code := run([]string{"help"}, &out, &errs); code != 0 || !strings.Contains(out.String(), "\n  price ") {
		t.Errorf("usage lacks the price line (code=%d):\n%s", code, out.String())
	}
}
