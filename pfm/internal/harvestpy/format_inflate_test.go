package harvestpy

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const inflateProbe = `
import json, lzma, pathlib, sys, tempfile
sys.path.insert(0, sys.argv[1])
import converter as c
out = {}
with tempfile.TemporaryDirectory() as scratch:
    d = pathlib.Path(scratch)
    (d / "in.xz").write_bytes(lzma.compress(b"%PDF-1.7 inner", format=lzma.FORMAT_XZ))
    out["ok"] = c.inflate({"codec": "xz", "path": str(d / "in.xz"), "out": str(d / "inner"), "limit": 1 << 20})
    out["inner"] = (d / "inner").read_text()
    (d / "bomb.xz").write_bytes(lzma.compress(b"\0" * (1 << 20), format=lzma.FORMAT_XZ))
    try:
        c.inflate({"codec": "xz", "path": str(d / "bomb.xz"), "out": str(d / "b"), "limit": 1000})
        out["bomb"] = "not refused"
    except c.DecompressionBomb as exc:
        out["bomb"] = str(exc)
    out["bomb_written"] = (d / "b").exists()
out["arabic_limit"] = c._script_limit("ar")
print(json.dumps(out))
`

// TestInflateXZAndArabicOCRAvailable: the sidecar inflates xz with the stdlib
// lzma under the caller's cap (a bomb is DecompressionBomb, nothing written),
// and python-bidi is pinned, so Arabic OCR is no longer named unavailable.
func TestInflateXZAndArabicOCRAvailable(t *testing.T) {
	python := os.Getenv("HARVESTPY_CORPUS_PYTHON")
	if python == "" {
		t.Skip("HARVESTPY_CORPUS_PYTHON is not set; the inflate probe needs the pinned interpreter")
	}
	scriptDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(scriptDir, "converter.py"), ConverterSource(), 0o600); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(python, "-c", inflateProbe, scriptDir).Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			t.Fatalf("inflate probe failed: %v\n%s", err, exit.Stderr)
		}
		t.Fatalf("inflate probe failed: %v", err)
	}
	var got struct {
		OK          map[string]any `json:"ok"`
		Inner       string         `json:"inner"`
		Bomb        string         `json:"bomb"`
		BombWritten bool           `json:"bomb_written"`
		ArabicLimit *string        `json:"arabic_limit"`
	}
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("decode probe output: %v\n%s", err, output)
	}
	if got.OK["ok"] != true || got.Inner != "%PDF-1.7 inner" {
		t.Errorf("inflate = %v, inner %q; want ok and the inner bytes", got.OK, got.Inner)
	}
	if !strings.Contains(got.Bomb, "more than 1000 bytes") || got.BombWritten {
		t.Errorf(
			"bomb = %q (written %v); want DecompressionBomb naming the cap and nothing written",
			got.Bomb,
			got.BombWritten,
		)
	}
	if got.ArabicLimit != nil && *got.ArabicLimit != "" {
		t.Errorf("Arabic OCR limit = %q; want none — python-bidi is pinned", *got.ArabicLimit)
	}
	if lock := string(LockMetadata()); !strings.Contains(lock, "name = \"python-bidi\"\nversion = \"0.6.11\"") {
		t.Error("uv.lock does not pin python-bidi 0.6.11, the bake-off's measured version")
	}
}
