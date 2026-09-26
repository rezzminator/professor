package hostfixture

import (
	"os"
	"path/filepath"
	"testing"
)

// CaseFoldFixture is CaseFoldProbe's result: the jailed Base plus whether
// this filesystem folded the probe's two differently-cased names.
type CaseFoldFixture struct {
	Base
	Dir   string // the directory the probe ran in
	Folds bool   // true when "a" and "A" named the same file here
}

// CaseFoldProbe jails a fleet, then creates "a" and "A" in a scratch
// directory and reports whether the filesystem folded them to one entry —
// the case installer's link/ledger paths and codexgen's output names must
// branch on rather than assume either way. It probes by content, not just
// existence: it writes "lower" to "a", then "upper" to "A" — on a
// case-folding filesystem the second write lands on the SAME file, so
// reading "a" back afterward yields "upper"; on a case-sensitive one it
// stays "lower".
func CaseFoldProbe(t *testing.T) CaseFoldFixture {
	t.Helper()
	base := newBase(t)
	dir := filepath.Join(base.Root, "casefold")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("hostfixture: create case-probe dir: %v", err)
	}
	lower := filepath.Join(dir, "a")
	upper := filepath.Join(dir, "A")
	if err := os.WriteFile(lower, []byte("lower"), 0o600); err != nil {
		t.Fatalf("hostfixture: write case-probe lower file: %v", err)
	}
	if err := os.WriteFile(upper, []byte("upper"), 0o600); err != nil {
		t.Fatalf("hostfixture: write case-probe upper file: %v", err)
	}
	content, err := os.ReadFile(lower)
	if err != nil {
		t.Fatalf("hostfixture: read case-probe lower file back: %v", err)
	}
	return CaseFoldFixture{Base: base, Dir: dir, Folds: string(content) == "upper"}
}
