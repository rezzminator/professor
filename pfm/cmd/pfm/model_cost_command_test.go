package main

import (
	"bytes"
	"regexp"
	"testing"
)

func TestModelCostCLIEntry(t *testing.T) {
	jailTest(t)
	// The jail sets PFM_PRICES_OFFLINE=1: the dispatch reaches the refresh, which fetches nothing and reports why.
	var out, errs bytes.Buffer
	code := run([]string{"model-cost", "--check"}, &out, &errs)
	// The row count, fetch time and clone path belong to the published table and the checkout, not to this dispatch.
	got := regexp.MustCompile(`\d+ rows · fetched \S+ · from file \S+`).
		ReplaceAllString(out.String(), "{n} rows · fetched {t} · from file {file}")
	if want := "prices: {n} rows · fetched {t} · from file {file} · refresh offline · billing: direct/global (default; cloud rates may differ) · override: none\n"; code != 0 ||
		got != want {
		t.Fatalf("model-cost --check code=%d stderr=%q stdout = %q, want %q", code, errs.String(), got, want)
	}
}
