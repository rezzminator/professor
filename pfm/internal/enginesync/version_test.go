package enginesync

import (
	"os"
	"path/filepath"
	"regexp"
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
