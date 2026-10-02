package harvestpy

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// The converter's HARVESTER_PDF_* protocol variables come only from the
// configured Runtime; a stray value in the pfm process environment is
// stripped so harvester.config.json stays the single source.
func TestWorkerEnvCarriesOnlyConfiguredConverterFlags(t *testing.T) {
	parent := []string{"PATH=/usr/bin", "HARVESTER_PDF_OCR=1", "HARVESTER_PDF_LAYOUT=true", "HOME=/fixture-home"}
	off := workerEnv(parent, Runtime{})
	if slices.Contains(off, "HARVESTER_PDF_OCR=1") || slices.Contains(off, "HARVESTER_PDF_LAYOUT=true") {
		t.Fatalf("inherited converter flags leaked into the worker: %q", off)
	}
	if !slices.Contains(off, "PATH=/usr/bin") || !slices.Contains(off, "HOME=/fixture-home") {
		t.Fatalf("unrelated environment dropped: %q", off)
	}
	on := workerEnv([]string{"PATH=/usr/bin"}, Runtime{PDFOCR: true, PDFLayout: true})
	if !slices.Contains(on, "HARVESTER_PDF_OCR=1") || !slices.Contains(on, "HARVESTER_PDF_LAYOUT=1") {
		t.Fatalf("configured converter flags missing: %q", on)
	}
}

// wantPycachePrefix is the one fixed bytecode home every sidecar launch
// carries, so a sidecar run from the clone never writes __pycache__ beside its
// sources (pfm/internal/harvestpy/assets/**).
func wantPycachePrefix() string {
	return "PYTHONPYCACHEPREFIX=" + filepath.Join(os.TempDir(), "pfm-pycache")
}

func TestWorkerEnvSendsBytecodeToTheFixedTempHome(t *testing.T) {
	env := workerEnv([]string{"PATH=/usr/bin", "PYTHONPYCACHEPREFIX=/elsewhere"}, Runtime{})
	if !slices.Contains(env, wantPycachePrefix()) {
		t.Fatalf("converter env lacks %q: %q", wantPycachePrefix(), env)
	}
	if slices.Contains(env, "PYTHONPYCACHEPREFIX=/elsewhere") {
		t.Fatalf("an inherited bytecode prefix survived beside the fixed one: %q", env)
	}
}

func TestBrowserWorkerLaunchSendsBytecodeToTheFixedTempHome(t *testing.T) {
	runner := &deps.FakeRunner{}
	runner.ScriptStart([]string{"fake-browser"}, 7010, nil, nil)
	worker := NewBrowserWorker(Runtime{Python: "fake-browser", Script: "script", Runner: runner})
	t.Cleanup(func() { _ = worker.Close() })
	_, _ = worker.ensureWorkerLocked()
	starts := runner.Starts()
	if len(starts) != 1 {
		t.Fatalf("browser worker launches = %d, want 1", len(starts))
	}
	if !slices.Contains(starts[0].Opts.Env, wantPycachePrefix()) {
		t.Fatalf("browser worker env lacks %q: %q", wantPycachePrefix(), starts[0].Opts.Env)
	}
}
