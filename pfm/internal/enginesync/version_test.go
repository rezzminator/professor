package enginesync

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestMinimumVersionDoesNotExceedEngineSyncSource(t *testing.T) {
	source := os.Getenv("ENGINE_SYNC_FENCE_SRC")
	if source == "" {
		source = "/engine-sync"
	}
	data, err := os.ReadFile(filepath.Join(source, "internal", "cli", "main.go"))
	if err != nil {
		t.Fatalf("read engine-sync cli.Version from %s: %v", source, err)
	}
	match := regexp.MustCompile(`(?m)^var Version = "([^"\n]+)"`).FindSubmatch(data)
	if len(match) != 2 {
		t.Fatal("engine-sync cli.Version declaration not found")
	}
	order, err := CompareVersions(MinimumVersion, string(match[1]))
	if err != nil || order > 0 {
		t.Fatalf("pfm requires %s, engine-sync cli.Version=%s: %v", MinimumVersion, match[1], err)
	}
}

func TestCompareEngineSyncVersionPrecedence(t *testing.T) {
	for _, tc := range []struct {
		left, right string
		want        int
	}{
		{"0.1.0", "0.2.0-alpha", -1},
		{"0.2.0", "0.2.0-alpha", 1},
		{"0.2.0-alpha.2", "0.2.0-alpha.10", -1},
		{"0.2.0-alpha", "0.2.0-beta", -1},
		{"0.2.0+build", "0.2.0", 0},
		{"0.2.0-alpha", "0.2.0-alpha", 0},
	} {
		t.Run(tc.left+"_"+tc.right, func(t *testing.T) {
			got, err := CompareVersions(tc.left, tc.right)
			if err != nil || got != tc.want {
				t.Fatalf("compare=%d (%v), want %d", got, err, tc.want)
			}
		})
	}
}

func TestCompareVersionsRefusesALeadingZeroNumericPrereleaseID(t *testing.T) {
	for _, tc := range []struct{ left, right, offender string }{
		{"01.2.3", "1.2.3", "01.2.3"},
		{"1.02.3", "1.2.3", "1.02.3"},
		{"1.2.3", "1.2.03", "1.2.03"},
		{"1.0.0-01", "1.0.0-1", "1.0.0-01"},
		{"1.0.0-1", "1.0.0-01", "1.0.0-01"},
		{"1.0.0-alpha.007", "1.0.0-alpha.7", "1.0.0-alpha.007"},
	} {
		_, err := CompareVersions(tc.left, tc.right)
		if err == nil || !strings.Contains(err.Error(), tc.offender) {
			t.Fatalf(
				"CompareVersions(%q, %q) err = %v, want invalid semver naming %q",
				tc.left, tc.right, err, tc.offender,
			)
		}
	}
	if got, err := CompareVersions("1.0.0-1", "1.0.0-2"); err != nil || got != -1 {
		t.Fatalf("1.0.0-1 vs 1.0.0-2 = %d (%v), want -1", got, err)
	}
	if got, err := CompareVersions("1.0.0-0", "1.0.0-0"); err != nil || got != 0 {
		t.Fatalf("a bare 0 id is valid, got %d (%v)", got, err)
	}
}
