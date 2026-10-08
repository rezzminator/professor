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
	); code != 0 || !strings.Contains(out.String(), "--all") {
		t.Fatalf("model-cost --help code=%d stdout=%q stderr=%q", code, out.String(), errs.String())
	}
	out.Reset()
	if code := run([]string{"help"}, &out, &errs); code != 0 || !strings.Contains(out.String(), "model-cost") {
		t.Fatalf("help code=%d lacks model-cost: %s", code, out.String())
	}
}
