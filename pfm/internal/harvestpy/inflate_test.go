package harvestpy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// inflateSidecar answers the inflate op the way converter.py's protocol does:
// a body starting "bomb" is a DecompressionBomb, any other request than an xz
// inflate a ValueError, and a good body is written to out as "inner:<body>".
const inflateSidecar = `
import json, pathlib, sys
for line in sys.stdin:
    req = json.loads(line)
    if req.get("op") == "smoke":
        print(json.dumps({"ok": True, "runtime": "test"}), flush=True)
        continue
    if req.get("op") != "inflate" or req.get("codec") != "xz" or req.get("limit") != 4096:
        print(json.dumps({"ok": False, "error_class": "ValueError", "error": "ValueError: bad request %r" % req}), flush=True)
        continue
    body = pathlib.Path(req["path"]).read_bytes()
    if body.startswith(b"bomb"):
        print(json.dumps({"ok": False, "error_class": "DecompressionBomb",
                          "error": "DecompressionBomb: more than %d bytes" % req["limit"]}), flush=True)
        continue
    pathlib.Path(req["out"]).write_bytes(b"inner:" + body)
    print(json.dumps({"ok": True, "bytes": len(body) + 6}), flush=True)
`

// TestInflateSendsTheOpAndNamesTheCap: Converter.Inflate sends the inflate op
// with its codec, paths and limit; a DecompressionBomb answer is ErrInflateCap,
// any other failure a named converter failure that is not.
func TestInflateSendsTheOpAndNamesTheCap(t *testing.T) {
	converter := testConverter(t, fakePython(t, inflateSidecar))
	t.Cleanup(func() { _ = converter.Close() })
	dir := t.TempDir()
	write := func(name, body string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	ctx := context.Background()

	out := filepath.Join(dir, "inner")
	if err := converter.Inflate(ctx, "xz", write("doc.xz", "notes"), out, 4096); err != nil {
		t.Fatalf("Inflate = %v, want nil", err)
	}
	if got, err := os.ReadFile(out); err != nil || string(got) != "inner:notes" {
		t.Errorf("inflated body = %q, %v; want the sidecar's output", got, err)
	}

	err := converter.Inflate(ctx, "xz", write("bomb.xz", "bomb"), filepath.Join(dir, "b"), 4096)
	if !errors.Is(err, ErrInflateCap) || !strings.Contains(err.Error(), "more than 4096 bytes") {
		t.Errorf("bomb: err = %v, want ErrInflateCap naming the cap", err)
	}

	err = converter.Inflate(ctx, "zst", write("doc.zst", "notes"), filepath.Join(dir, "z"), 4096)
	if err == nil || errors.Is(err, ErrInflateCap) || !strings.Contains(err.Error(), "ValueError") {
		t.Errorf("unsupported codec: err = %v, want a named ValueError that is not ErrInflateCap", err)
	}
}
