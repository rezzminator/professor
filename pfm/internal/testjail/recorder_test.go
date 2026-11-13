package testjail

import (
	"errors"
	"runtime/trace"
	"testing"
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
