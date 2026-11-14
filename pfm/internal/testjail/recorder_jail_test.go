//go:build jail

package testjail

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestPauseFlightRecorderStopsItForTheTestAndRestartsItAfter(t *testing.T) {
	t.Parallel()
	res := (&child{mode: "pause", artifacts: t.TempDir()}).run(t)
	if res.code != 0 {
		t.Fatalf("child exit %d: %s%s", res.code, res.stdout, res.stderr)
	}
}

func TestPauseFlightRecorderIsANoOpWithProfilingOff(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]*child{
		"PFM_TEST_PROFILE=0": {mode: "pause-off", artifacts: t.TempDir(), env: []string{paths.EnvTestProfile + "=0"}},
		"no artifact dir":    {mode: "pause-off"},
	} {
		if res := c.run(t); res.code != 0 {
			t.Errorf("%s: child exit %d: %s%s", name, res.code, res.stdout, res.stderr)
		}
	}
}

func TestPauseFlightRecorderPanicsInAParallelTest(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"pause-parallel-after", "pause-parallel-before"} {
		res := (&child{mode: mode, artifacts: t.TempDir()}).run(t)
		panicLine := ""
		for _, line := range strings.Split(res.stdout+res.stderr, "\n") {
			if strings.HasPrefix(line, "panic: ") {
				panicLine = line
			}
		}
		if got, want := fmt.Sprintf(
			"%d %s",
			res.code,
			panicLine,
		), "2 panic: testing: test using t.Setenv, t.Chdir, or cryptotest.SetGlobalRandom can not use t.Parallel [recovered, repanicked]"; got != want {
			t.Errorf("%s: child exit and panic = %q, want %q", mode, got, want)
		}
	}
}

func TestProfileBundleAfterAPauseHoldsTheTraceWindow(t *testing.T) {
	t.Parallel()
	artifacts := t.TempDir()
	res := (&child{mode: "pause-fail", artifacts: artifacts}).run(t)
	if res.code == 0 || !strings.Contains(res.stdout, "deliberate failure after a pause") {
		t.Fatalf("child exit %d, want the deliberate red: %s%s", res.code, res.stdout, res.stderr)
	}
	bundle := filepath.Join(processDir(t, artifacts, profileLabel), "exit")
	info, err := os.Stat(filepath.Join(bundle, "trace.out"))
	if err != nil || info.Size() == 0 {
		t.Fatalf("exit bundle trace.out: %v (size %d); bundle holds %v", err, sizeOf(info), entryNames(t, bundle))
	}
	if slices.Contains(entryNames(t, bundle), "errors.txt") {
		errs := readFile(t, filepath.Join(bundle, "errors.txt"))
		t.Fatalf("exit bundle has errors.txt after a restarted recorder:\n%s", errs)
	}
}

func TestProfileBundleAfterARefusedRestartNamesTheMissingTrace(t *testing.T) {
	t.Parallel()
	artifacts := t.TempDir()
	res := (&child{mode: "pause-refused", artifacts: artifacts}).run(t)
	if res.code != 1 {
		t.Fatalf("child exit %d, want the deliberate red: %s%s", res.code, res.stdout, res.stderr)
	}
	bundle := filepath.Join(processDir(t, artifacts, profileLabel), "exit")
	errs := readFile(t, filepath.Join(bundle, "errors.txt"))
	if got, want := rooted(
		t,
		errs,
	), "trace window: flight recorder not running: flight recorder already enabled; no trace.out\n"; got != want {
		t.Errorf("errors.txt = %q, want %q", got, want)
	}
	if fileExists(filepath.Join(bundle, "trace.out")) {
		t.Errorf("exit bundle holds a trace.out though no recorder ran")
	}
}

func sizeOf(info os.FileInfo) int64 {
	if info == nil {
		return -1
	}
	return info.Size()
}
