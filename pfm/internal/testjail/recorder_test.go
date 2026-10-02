package testjail

import (
	"errors"
	"os"
	"path/filepath"
	"runtime/trace"
	"slices"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// PauseFlightRecorder is tested in a child (TestProfileChild with a pause-*
// mode), where the profiler's environment is the test's own; the child checks
// the runtime's tracing state across the pause and fails when it is wrong.

// pauseChild is TestProfileChild's body for the pause-* modes.
func pauseChild(t *testing.T, mode string) {
	switch mode {
	case "pause-parallel-after":
		PauseFlightRecorder(t)
		t.Parallel()
		return
	case "pause-parallel-before":
		t.Parallel()
		PauseFlightRecorder(t)
		return
	}
	recording := mode != "pause-off"
	if trace.IsEnabled() != recording {
		t.Fatalf("before the pause: tracing enabled %v, want %v", trace.IsEnabled(), recording)
	}
	t.Run("paused", func(t *testing.T) {
		PauseFlightRecorder(t)
		if trace.IsEnabled() {
			t.Fatal("tracing still enabled after PauseFlightRecorder")
		}
		if mode == "pause-refused" {
			// Another recorder takes the one slot, so the cleanup's restart is refused.
			if err := trace.NewFlightRecorder(flightRecorderConfig).Start(); err != nil {
				t.Fatalf("start the recorder that refuses the restart: %v", err)
			}
		}
	})
	if mode == "pause-refused" {
		t.Fatal("deliberate failure after a refused restart")
	}
	if trace.IsEnabled() != recording {
		t.Fatalf("after the pause's cleanup: tracing enabled %v, want %v", trace.IsEnabled(), recording)
	}
	if recording {
		p := activeProfiler.Load()
		p.mu.Lock()
		running := p.fr != nil && p.fr.Enabled()
		p.mu.Unlock()
		if !running {
			t.Fatal("the profiler holds no running recorder after the pause's cleanup")
		}
	}
	if mode == "pause-fail" {
		t.Fatal("deliberate failure after a pause")
	}
}

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

func TestPauseRecorderWithNoRecorderRunningLeavesItsReason(t *testing.T) {
	reason := errors.New("never started")
	p := &profiler{frErr: reason}
	if p.pauseRecorder("TestX") {
		t.Fatal("pauseRecorder reported a running recorder on a profiler that has none")
	}
	if !errors.Is(p.frErr, reason) {
		t.Fatalf("frErr = %v, want the start failure kept", p.frErr)
	}
}

func TestPauseFlightRecorderPanicsInAParallelTest(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"pause-parallel-after", "pause-parallel-before"} {
		res := (&child{mode: mode, artifacts: t.TempDir()}).run(t)
		out := res.stdout + res.stderr
		if res.code == 0 || !strings.Contains(out, "can not use t.Parallel") {
			t.Errorf("%s: child exit %d without Go's t.Setenv/t.Parallel panic:\n%s", mode, res.code, out)
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
	if res.code == 0 || !strings.Contains(res.stdout, "deliberate failure after a refused restart") {
		t.Fatalf("child exit %d, want the deliberate red: %s%s", res.code, res.stdout, res.stderr)
	}
	bundle := filepath.Join(processDir(t, artifacts, profileLabel), "exit")
	errs := readFile(t, filepath.Join(bundle, "errors.txt"))
	for _, want := range []string{"trace window: flight recorder not running: ", "already enabled", "no trace.out"} {
		if !strings.Contains(errs, want) {
			t.Errorf("errors.txt lacks %q:\n%s", want, errs)
		}
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
