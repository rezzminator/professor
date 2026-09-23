package harvestmcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/harvest"
	"github.com/rezzminator/professor/pfm/internal/harvestpy"
)

// inflateWorker stands in for converter.py's inflate op: a body starting
// "bomb" is a DecompressionBomb, a good one is written to out as "inner:<body>".
const inflateWorker = `
import json, pathlib, sys
for line in sys.stdin:
    req = json.loads(line)
    if req.get("op") == "smoke":
        print(json.dumps({"ok": True, "runtime": "test"}), flush=True)
        continue
    body = pathlib.Path(req["path"]).read_bytes()
    if body.startswith(b"bomb"):
        print(json.dumps({"ok": False, "error_class": "DecompressionBomb",
                          "error": "DecompressionBomb: more than %d bytes" % req["limit"]}), flush=True)
        continue
    pathlib.Path(req["out"]).write_bytes(b"inner:" + body)
    print(json.dumps({"ok": True}), flush=True)
`

// TestInflateHandsTheBodyToTheSidecarAndRemovesTheScratch: pythonConverter
// (harvest.Inflater) writes the body to a private scratch, answers the inner
// bytes the sidecar wrote, maps its cap to harvest.ErrDecompressionBomb, and
// leaves no scratch behind either way.
func TestInflateHandsTheBodyToTheSidecarAndRemovesTheScratch(t *testing.T) {
	scratch := t.TempDir()
	t.Setenv("TMPDIR", scratch)
	dir := t.TempDir()
	python := filepath.Join(dir, "fake-python")
	launcher := "#!/bin/sh\nexec python3 -c '" + strings.ReplaceAll(inflateWorker, "'", "'\\''") + "'\n"
	if err := os.WriteFile(python, []byte(launcher), 0o700); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "converter.py")
	if err := os.WriteFile(script, harvestpy.ConverterSource(), 0o600); err != nil {
		t.Fatal(err)
	}
	worker := harvestpy.NewConverter(harvestpy.Runtime{Python: python, Script: script})
	t.Cleanup(func() { _ = worker.Close() })
	converter := pythonConverter{worker: worker}

	inner, err := converter.Inflate(context.Background(), "xz", []byte("notes"), 4096)
	if err != nil || string(inner) != "inner:notes" {
		t.Errorf("Inflate = %q, %v; want the sidecar's inner bytes", inner, err)
	}
	_, err = converter.Inflate(context.Background(), "xz", []byte("bomb"), 4096)
	if !errors.Is(err, harvest.ErrDecompressionBomb) || !strings.Contains(err.Error(), "more than 4096 bytes") {
		t.Errorf("bomb: err = %v, want harvest.ErrDecompressionBomb naming the sidecar's cap", err)
	}
	if left, err := os.ReadDir(scratch); err != nil || len(left) != 0 {
		t.Errorf("scratch left behind: %v, %v; want none", left, err)
	}
}
