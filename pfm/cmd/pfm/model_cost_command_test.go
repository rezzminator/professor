package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestModelCostCLIEntry(t *testing.T) {
	jailTest(t)
	var out, errs bytes.Buffer
	if code := run(
		[]string{"model-cost", "--help"},
		&out,
		&errs,
	); code != 0 || !strings.Contains(out.String(), "--all") || !strings.Contains(out.String(), "--force") {
		t.Fatalf("model-cost --help code=%d stdout=%q stderr=%q", code, out.String(), errs.String())
	}
	out.Reset()
	// The jail sets PFM_PRICES_OFFLINE=1: the dispatch reaches the refresh,
	// which fetches nothing and reports why.
	if code := run([]string{"model-cost", "--check"}, &out, &errs); code != 0 ||
		!strings.HasPrefix(out.String(), "prices: ") || !strings.Contains(out.String(), "refresh offline") {
		t.Fatalf("model-cost --check code=%d stdout=%q stderr=%q", code, out.String(), errs.String())
	}
	out.Reset()
	if code := run([]string{"help"}, &out, &errs); code != 0 || !strings.Contains(out.String(), "model-cost") {
		t.Fatalf("help code=%d lacks model-cost: %s", code, out.String())
	}
}
