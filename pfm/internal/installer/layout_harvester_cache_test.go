package installer

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

const harvesterCacheRow = "harvester-cache"

const harvesterCacheNested = ".private/handles/h1"

// harvesterCacheLegacy writes the pre-rename cache with one nested file.
func harvesterCacheLegacy(t *testing.T, env LayoutEnv, body string) string {
	t.Helper()
	legacy := paths.LegacyHarvesterCacheDir(env.Home)
	layoutWrite(t, filepath.Join(legacy, harvesterCacheNested), body)
	return legacy
}

func harvesterCacheRead(t *testing.T, dir string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, harvesterCacheNested))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func harvesterCacheAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("%s exists or is unreadable: %v", path, err)
	}
}

func TestLayoutHarvesterCacheMovesLegacyDirectoryJournaledAndIdempotent(t *testing.T) {
	env := layoutFixture(t)
	legacy := harvesterCacheLegacy(t, env, "handle-bytes\x00\x01")
	target := paths.HarvesterCacheDir(env.Home)
	finding := requireLayoutVerdict(t, ClassifyLayout(env), harvesterCacheRow, target, VerdictMove)
	if finding.Source != legacy {
		t.Fatalf("source = %q, want %q", finding.Source, legacy)
	}
	var output bytes.Buffer
	dir, err := ApplyLayout(context.Background(), env, nil, true, &output)
	if err != nil || dir == "" {
		t.Fatalf("apply journal=%q err=%v output=%s", dir, err, output.String())
	}
	if got := harvesterCacheRead(t, target); got != "handle-bytes\x00\x01" {
		t.Fatalf("moved bytes = %q", got)
	}
	harvesterCacheAbsent(t, legacy)
	requireLayoutVerdict(t, ClassifyLayout(env), harvesterCacheRow, target, VerdictOK)
	var second bytes.Buffer
	again, err := ApplyLayout(context.Background(), env, nil, true, &second)
	if err != nil || again != "" || !strings.Contains(second.String(), "layout: nothing to do") {
		t.Fatalf("second journal=%q err=%v output=%s", again, err, second.String())
	}

	if err := RollbackLayout(context.Background(), env, filepath.Base(dir), false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if got := harvesterCacheRead(t, legacy); got != "handle-bytes\x00\x01" {
		t.Fatalf("rolled back bytes = %q", got)
	}
	harvesterCacheAbsent(t, target)
}

func TestLayoutHarvesterCacheFreshAndMigratedHostsAreOK(t *testing.T) {
	for name, prepare := range map[string]func(t *testing.T, env LayoutEnv){
		"fresh": func(*testing.T, LayoutEnv) {},
		"migrated": func(t *testing.T, env LayoutEnv) {
			layoutWrite(t, filepath.Join(paths.HarvesterCacheDir(env.Home), harvesterCacheNested), "kept")
		},
	} {
		t.Run(name, func(t *testing.T) {
			env := layoutFixture(t)
			prepare(t, env)
			target := paths.HarvesterCacheDir(env.Home)
			requireLayoutVerdict(t, ClassifyLayout(env), harvesterCacheRow, target, VerdictOK)
			var output bytes.Buffer
			dir, err := ApplyLayout(context.Background(), env, nil, true, &output)
			if err != nil || dir != "" || !strings.Contains(output.String(), "layout: nothing to do") {
				t.Fatalf("apply journal=%q err=%v output=%s", dir, err, output.String())
			}
			if name == "migrated" && harvesterCacheRead(t, target) != "kept" {
				t.Fatal("migrated cache changed")
			}
			harvesterCacheAbsent(t, paths.LegacyHarvesterCacheDir(env.Home))
		})
	}
}

func TestLayoutHarvesterCacheBothPresentWarnsNeverMerges(t *testing.T) {
	env := layoutFixture(t)
	legacy := harvesterCacheLegacy(t, env, "legacy")
	target := paths.HarvesterCacheDir(env.Home)
	layoutWrite(t, filepath.Join(target, harvesterCacheNested), "target")
	finding := requireLayoutVerdict(t, ClassifyLayout(env), harvesterCacheRow, target, VerdictRefuse)
	if finding.Source != legacy || !strings.Contains(finding.Detail, "never merged") {
		t.Fatalf("finding = %+v", finding)
	}
	var output bytes.Buffer
	if _, err := ApplyLayout(context.Background(), env, nil, true, &output); err != nil {
		t.Fatalf("apply err=%v output=%s", err, output.String())
	}
	if !strings.Contains(output.String(), "  warn    layout harvester-cache "+target) ||
		strings.Contains(output.String(), "refuse  layout harvester-cache") {
		t.Fatalf("output = %s", output.String())
	}
	if harvesterCacheRead(t, legacy) != "legacy" || harvesterCacheRead(t, target) != "target" {
		t.Fatal("a both-present cache was touched")
	}
}

func TestLayoutHarvesterCacheNonDirectoryLegacyRefuses(t *testing.T) {
	env := layoutFixture(t)
	legacy := paths.LegacyHarvesterCacheDir(env.Home)
	layoutWrite(t, legacy, "a file")
	target := paths.HarvesterCacheDir(env.Home)
	finding := requireLayoutVerdict(t, ClassifyLayout(env), harvesterCacheRow, target, VerdictRefuse)
	if finding.Source != legacy || finding.Detail != "legacy cache path is not a directory" {
		t.Fatalf("finding = %+v", finding)
	}
}

func TestLayoutHarvesterCacheConfiguredDirLeavesEverythingUntouched(t *testing.T) {
	env := layoutFixture(t)
	legacy := harvesterCacheLegacy(t, env, "legacy")
	env.Config.Harvester.Cache.Dir = t.TempDir()
	target := paths.HarvesterCacheDir(env.Home)
	finding := requireLayoutVerdict(t, ClassifyLayout(env), harvesterCacheRow, target, VerdictOK)
	if finding.Detail != "cache.dir configured; untouched" {
		t.Fatalf("detail = %q", finding.Detail)
	}
	var output bytes.Buffer
	dir, err := ApplyLayout(context.Background(), env, nil, true, &output)
	if err != nil || dir != "" {
		t.Fatalf("apply journal=%q err=%v output=%s", dir, err, output.String())
	}
	if harvesterCacheRead(t, legacy) != "legacy" {
		t.Fatal("legacy cache changed")
	}
	harvesterCacheAbsent(t, target)
}
