package harvestcli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/harvest"
)

// TestRenderReadNamesTheGapsOfAnIncompleteArtifact pins the CLI receipt
// header: a known-incomplete artifact names its gaps on the cached line,
// before the blank line and the content; a complete one names none.
func TestRenderReadNamesTheGapsOfAnIncompleteArtifact(t *testing.T) {
	result := harvest.Result{
		Source:      "https://fixture.example/source",
		CacheStatus: "miss",
		Bytes:       12,
		Tokens:      3,
		Path:        "/cache/source.md",
		Content:     "body text",
	}
	complete := renderRead(result, true)
	if strings.Contains(complete, "gaps:") {
		t.Fatalf("complete receipt names gaps:\n%s", complete)
	}
	result.Partial = "page 3 of 9 failed to convert; 2 images unreadable"
	want := "cached: false / bytes: 12 / tokens: 3 / path: /cache/source.md" +
		" / gaps: page 3 of 9 failed to convert; 2 images unreadable\n\nbody text"
	if got := renderRead(result, true); !strings.Contains(got, want) {
		t.Fatalf("incomplete receipt header:\n%s\nwant it to contain:\n%s", got, want)
	}
	// The size receipt (--include-content=false) is a receipt too: a caller
	// budgeting a read must learn the artifact is incomplete before it reads it.
	if got := renderRead(result, false); !strings.Contains(got, " / gaps: page 3 of 9 failed to convert") {
		t.Fatalf("size receipt hides the gaps:\n%s", got)
	}
	result.Partial = ""
	if got := renderRead(result, false); strings.Contains(got, "gaps:") {
		t.Fatalf("complete size receipt names gaps:\n%s", got)
	}
}

// harvestLocal runs `pfm harvest` over args with a fresh local text file
// appended, returning the exit code, stdout and stderr.
func harvestLocal(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	runtime := downloadRuntime(t)
	source := filepath.Join(runtime.Paths.Home, "plain.txt")
	if err := os.WriteFile(source, []byte("plain harvest body\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := Harvest(append(args, source), &stdout, &stderr, runtime)
	return code, stdout.String(), stderr.String()
}

// TestHarvestIncludeContentFalsePrintsOnlyTheSizeReceipt: the read caches the
// source and prints its size and cache path, never its body.
func TestHarvestIncludeContentFalsePrintsOnlyTheSizeReceipt(t *testing.T) {
	code, stdout, stderr := harvestLocal(t, "--include-content=false")
	if code != 0 {
		t.Fatalf("--include-content=false code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "\ntokens: ") || !strings.Contains(stdout, " / path: ") ||
		strings.Contains(stdout, "plain harvest body") {
		t.Fatalf("--include-content=false printed:\n%s\nwant the size receipt without the body", stdout)
	}
	code, stdout, stderr = harvestLocal(t)
	if code != 0 || !strings.Contains(stdout, "plain harvest body") {
		t.Fatalf("default read code=%d stdout=%q stderr=%q, want the body", code, stdout, stderr)
	}
}

// TestHarvestOCRLanguageIsValidatedByName: --ocr-language takes a staged
// script and refuses anything else naming the flag, before any read.
func TestHarvestOCRLanguageIsValidatedByName(t *testing.T) {
	if code, stdout, stderr := harvestLocal(t, "--ocr-language", "ja"); code != 0 {
		t.Fatalf("--ocr-language ja code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr := harvestLocal(t, "--ocr-language", "klingon")
	if code != 2 || !strings.Contains(stderr, "--ocr-language") || !strings.Contains(stderr, "klingon") ||
		stdout != "" {
		t.Fatalf("--ocr-language klingon code=%d stdout=%q stderr=%q, want 2 naming the flag", code, stdout, stderr)
	}
}

// TestHarvestDownloadVerbPointsAtDownloadFile keeps the CLI's recovery hint
// for callers who use the download verb.
func TestHarvestDownloadVerbPointsAtDownloadFile(t *testing.T) {
	code, stdout, stderr := harvestLocal(t, "download")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "download-file") {
		t.Fatalf(
			"harvest download: code=%d stdout=%q stderr=%q, want exit 2 and the download-file hint",
			code,
			stdout,
			stderr,
		)
	}
}

// TestRenderReadUsesTheItemFieldNames: both CLI receipts name what the MCP
// read item names — cached, tokens, chars — and no old field name.
func TestRenderReadUsesTheItemFieldNames(t *testing.T) {
	result := harvest.Result{
		Source:      "https://fixture.example/source",
		CacheStatus: "hit",
		Bytes:       12,
		Chars:       9,
		Tokens:      3,
		Path:        "/cache/source.md",
		Content:     "body text",
	}
	full := renderRead(result, true)
	want := "\ncached: true / bytes: 12 / tokens: 3 / path: /cache/source.md\n\nbody text"
	if !strings.Contains(full, want) {
		t.Fatalf("full receipt:\n%s\nwant it to contain:\n%s", full, want)
	}
	size := renderRead(result, false)
	if want = "\ntokens: 3 / chars: 9 / path: /cache/source.md / cached: true"; !strings.Contains(size, want) {
		t.Fatalf("size receipt:\n%s\nwant it to contain:\n%s", size, want)
	}
	for _, old := range []string{"cache_status", "size:"} {
		if strings.Contains(full, old) || strings.Contains(size, old) {
			t.Fatalf("a receipt carries the old word %q:\n%s\n%s", old, full, size)
		}
	}
}
