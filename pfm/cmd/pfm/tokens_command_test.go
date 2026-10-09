package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestTokensCLIEntry(t *testing.T) {
	jailTest(t)
	var out, errs bytes.Buffer
	if code := run([]string{"tokens", "--help"}, &out, &errs); code != 0 ||
		!strings.Contains(out.String(), "--codex") || !strings.Contains(out.String(), "--timeline") {
		t.Fatalf("tokens --help code=%d stdout=%q stderr=%q", code, out.String(), errs.String())
	}
	out.Reset()
	errs.Reset()
	// The jail sets PFM_PRICES_OFFLINE=1: the served table loads without a fetch.
	if code := run([]string{"tokens", "--root", t.TempDir()}, &out, &errs); code != 0 ||
		!strings.HasPrefix(out.String(), "TOKEN AUDIT · last 24h · ") {
		t.Fatalf("tokens over an empty root code=%d stdout=%q stderr=%q", code, out.String(), errs.String())
	}
	out.Reset()
	if code := run([]string{"tokens", "--since", "3x"}, &out, &errs); code != 2 ||
		!strings.Contains(errs.String(), "pfm tokens: --since wants a number plus h or d") {
		t.Fatalf("tokens --since 3x code=%d stderr=%q", code, errs.String())
	}
	out.Reset()
	if code := run([]string{"help"}, &out, &errs); code != 0 || !strings.Contains(out.String(), "  tokens ") {
		t.Fatalf("help code=%d lacks tokens: %s", code, out.String())
	}
}
