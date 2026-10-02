package doctor

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
)

// priceDoctorRuntime is a runtime whose pfm.config.json sits in a fresh temp
// dir; override, when non-empty, is written beside it as pfm.prices.json.
func priceDoctorRuntime(t *testing.T, override string) (config.Runtime, string) {
	t.Helper()
	runtime := config.Runtime{Config: config.Defaults(t.TempDir(), nil)}
	runtime.Config.Path = filepath.Join(t.TempDir(), config.FileName)
	pricesPath := config.PricesPath(runtime.Config.Path)
	if override != "" {
		if err := os.WriteFile(pricesPath, []byte(override), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return runtime, pricesPath
}

func TestDoctorPriceOverrideRow(t *testing.T) {
	const valid = `{"version": 1, "rows": [
  {"key": "opus", "engine": "claude", "match": ["opus"], "in": 7, "out": 35, "hit": 0.7, "w5m": 8.75, "w1h": 14, "long_in": 1, "long_out": 1},
  {"key": "opus-6", "engine": "claude", "match": ["opus-6"], "in": 6, "out": 30, "hit": 0.6, "w5m": 7.5, "w1h": 12, "long_in": 1, "long_out": 1}
]}`
	cases := []struct {
		name, override, want string
		emptyPath            bool
		warnings             int
	}{
		{name: "none", want: "doctor: prices override=none path={path}\n"},
		{name: "no config path", emptyPath: true, want: "doctor: prices override=none\n"},
		{name: "active", override: valid, want: "doctor: prices override=active rows=2 path={path}\n"},
		{
			name:     "invalid",
			override: `{"version": 1, "rows": [`,
			want:     "doctor: prices override=invalid path={path} error=",
			warnings: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runtime, pricesPath := priceDoctorRuntime(t, tc.override)
			if tc.emptyPath {
				runtime.Config.Path = ""
			}
			var stdout bytes.Buffer
			if warnings := printPriceOverride(&stdout, runtime); warnings != tc.warnings {
				t.Errorf("warnings=%d, want %d", warnings, tc.warnings)
			}
			want := strings.ReplaceAll(tc.want, "{path}", pricesPath)
			if !strings.HasPrefix(stdout.String(), want) || strings.Count(stdout.String(), "\n") != 1 {
				t.Errorf("output=%q, want one row starting %q", stdout.String(), want)
			}
			if tc.warnings == 1 && !strings.Contains(stdout.String(), "error=prices "+pricesPath) {
				t.Errorf("invalid row does not carry the load error naming the path: %q", stdout.String())
			}
		})
	}
}
