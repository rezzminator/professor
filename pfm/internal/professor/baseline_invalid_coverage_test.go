package professor

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRejectsNamedCoverageRule(t *testing.T) {
	root := t.TempDir()
	path := BaselinePath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := []byte(
		`{"version":3,"files":{},"retiredAccepted":[{"path":"docs","name":"/old","coverage":true,"reason":"hand edited"}]}`,
	)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(
		root,
	); err == nil || !strings.Contains(err.Error(), "BASELINE-MALFORMED") ||
		!strings.Contains(err.Error(), "coverage") {
		t.Fatalf("unclosable named coverage rule loaded: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatal("invalid baseline rewritten")
	}
}
