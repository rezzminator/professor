package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadCompactConfig(t *testing.T, content string) (Config, string, error) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path, home, nil)
	return got, path, err
}

func TestParseCompactTokens(t *testing.T) {
	accepted := map[string]int{
		"150000": 150000, "150k": 150000, "150K": 150000, "1m": 1000000, "1M": 1000000,
		"100k": 100000, "1000000": 1000000, "0.5m": 500000, "150.5k": 150500, "200k\n": 200000,
	}
	for raw, want := range accepted {
		got, err := ParseCompactTokens(raw)
		if err != nil || got != want {
			t.Errorf("ParseCompactTokens(%q) = %d, %v; want %d, nil", raw, got, err, want)
		}
	}
	for _, raw := range []string{
		"99999", "1.1m", "1000001", "0", "-5", "abc", "", "150kk", "1.5", "100.0001k", "k", "1e5", "99999999999999999999",
	} {
		if got, err := ParseCompactTokens(raw); err == nil {
			t.Errorf("ParseCompactTokens(%q) = %d, nil; want an error", raw, got)
		}
	}
}

func TestLoadCompactThresholds(t *testing.T) {
	for _, tc := range []struct{ value, sub string }{
		{`150000`, `"150k"`}, {`"1m"`, `"100k"`}, {`"1000000"`, `100000`},
	} {
		got, _, err := loadCompactConfig(t, `{"version": 2, "claude": {"autoCompactMain": `+tc.value+
			`, "autoCompactSubagent": `+tc.sub+`}}`)
		if err != nil {
			t.Fatalf("Load(main=%s, sub=%s) error = %v", tc.value, tc.sub, err)
		}
		main, sub, ok := got.Claude.CompactThresholds()
		wantMain, _ := ParseCompactTokens(strings.Trim(tc.value, `"`))
		wantSub, _ := ParseCompactTokens(strings.Trim(tc.sub, `"`))
		if !ok || main != wantMain || sub != wantSub || wantMain == 0 || wantSub == 0 {
			t.Fatalf("CompactThresholds() = %d, %d, %t for main=%s sub=%s", main, sub, ok, tc.value, tc.sub)
		}
		for _, key := range []string{"claude.autoCompactMain", "claude.autoCompactSubagent"} {
			if source := got.Source(key); source != SourceFile {
				t.Fatalf("Source(%s) = %q, want %q", key, source, SourceFile)
			}
		}
		// Machine-wide: an account that set other claude keys still sees them.
		if effective := got.EffectiveClaude(0); effective.AutoCompactMain != main {
			t.Fatalf("EffectiveClaude(0).AutoCompactMain = %d, want %d", effective.AutoCompactMain, main)
		}
	}
}

func TestLoadCompactThresholdsUnsetAndHalfSet(t *testing.T) {
	got, _, err := loadCompactConfig(t, `{"version": 2}`)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if main, sub, ok := got.Claude.CompactThresholds(); main != 0 || sub != 0 || ok {
		t.Fatalf("unset CompactThresholds() = %d, %d, %t; want 0, 0, false", main, sub, ok)
	}
	if source := got.Source("claude.autoCompactMain"); source != SourceDefault {
		t.Fatalf("Source(claude.autoCompactMain) = %q, want %q", source, SourceDefault)
	}
	got, _, err = loadCompactConfig(t, `{"version": 2, "claude": {"autoCompactMain": "200k"}}`)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if main, sub, ok := got.Claude.CompactThresholds(); main != 200000 || sub != 0 || ok {
		t.Fatalf("half-set CompactThresholds() = %d, %d, %t; want 200000, 0, false", main, sub, ok)
	}
}

func TestLoadCompactThresholdsRejects(t *testing.T) {
	for _, value := range []string{
		`99999`, `"1.1m"`, `1000001`, `0`, `-5`, `"abc"`, `""`, `"150kk"`, `null`, `true`, `150000.5`,
	} {
		for _, key := range []string{"autoCompactMain", "autoCompactSubagent"} {
			_, path, err := loadCompactConfig(t, `{"version": 2, "claude": {"`+key+`": `+value+`}}`)
			if err == nil {
				t.Fatalf("Load(claude.%s=%s) error = nil, want a rejection", key, value)
			}
			if !strings.Contains(err.Error(), "claude."+key) || !strings.Contains(err.Error(), path) {
				t.Fatalf("Load(claude.%s=%s) error = %q, want it to name the key and %s", key, value, err, path)
			}
		}
	}
}

// The thresholds are machine-wide: an account block that names one is a
// mistake the loader refuses, the way the strict decoder refuses any key the
// account scope does not own, instead of silently ignoring it.
func TestLoadCompactThresholdsRejectedPerAccount(t *testing.T) {
	_, path, err := loadCompactConfig(t, `{
  "version": 2,
  "accounts": [{"id": 3, "configDir": "~/three", "claude": {"autoCompactSubagent": "150k"}}]
}`)
	if err == nil {
		t.Fatal("Load(accounts[0].claude.autoCompactSubagent) error = nil, want a rejection")
	}
	if !strings.Contains(err.Error(), "accounts[0].autoCompactSubagent") || !strings.Contains(err.Error(), path) {
		t.Fatalf("per-account error = %q, want it to name accounts[0].autoCompactSubagent and %s", err, path)
	}
}

// claude.autoCompactWindow is the plain window: one point for every party,
// machine-wide, and never beside the per-party pair it would contradict.
func TestLoadAutoCompactWindow(t *testing.T) {
	got, _, err := loadCompactConfig(t, `{"version": 2, "claude": {"autoCompactWindow": "600k"}}`)
	if err != nil {
		t.Fatalf("Load(autoCompactWindow=600k) error = %v", err)
	}
	if got.Claude.AutoCompactWindow != 600000 || got.Source("claude.autoCompactWindow") != SourceFile {
		t.Fatalf("window = %d from %q, want 600000 from %q",
			got.Claude.AutoCompactWindow, got.Source("claude.autoCompactWindow"), SourceFile)
	}
	for _, tc := range []struct{ name, claude, accounts, wantErr string }{
		{
			name:    "beside the per-party pair",
			claude:  `{"autoCompactWindow": "600k", "autoCompactMain": "600k", "autoCompactSubagent": "150k"}`,
			wantErr: "claude.autoCompactWindow and claude.autoCompactMain/autoCompactSubagent",
		},
		{
			name: "in an account block", claude: `{}`,
			accounts: `, "accounts": [{"id": 1, "configDir": "~/.claude", "claude": {"autoCompactWindow": "600k"}}]`,
			wantErr:  "autoCompactWindow is machine-wide",
		},
		{name: "out of range", claude: `{"autoCompactWindow": "50k"}`, wantErr: "outside 100000..1000000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := loadCompactConfig(t, `{"version": 2, "claude": `+tc.claude+tc.accounts+`}`)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Load error = %v, want one containing %q", err, tc.wantErr)
			}
		})
	}
}
