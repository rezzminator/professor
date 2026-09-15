package harvest

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Parity with harvester tests/test_wave3.py — the wave additions must behave
// identically on both sides of the seam.

func TestWaveLooksLikeEpubBoundaries(t *testing.T) {
	epub := buildMinimalEpub(t)
	if !LooksLikeEpub(epub) {
		t.Fatalf("a real EPUB must carry its mimetype marker in the first 200 bytes")
	}
	plain := []byte("PK\x03\x04 plain zip, not a book")
	if LooksLikeEpub(plain) {
		t.Fatalf("plain zip bytes must not be mistaken for an EPUB")
	}
	buried := append([]byte("PK\x03\x04"), append(make([]byte, 4096), []byte(epubContentMarker)...)...)
	if LooksLikeEpub(buried) {
		t.Fatalf("the marker buried in member DATA is not an EPUB — only the stored mimetype position counts")
	}
}

func buildMinimalEpub(t *testing.T) []byte {
	t.Helper()
	// Hand-rolled minimal EPUB: mimetype STORED first (spec order), then container + OPF.
	var out []byte
	// Every member is written STORED (compression method 0) — the fixture only
	// needs the mimetype member's bytes to sit where the spec puts them.
	appendFile := func(name string, data []byte) {
		out = append(out, 'P', 'K', 3, 4, 14, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, byte(len(name)), 0)
		out = append(out, name...)
		out = append(out, data...)
	}
	appendFile("mimetype", []byte(epubContentMarker))
	appendFile("OEBPS/c1.xhtml", []byte("<html><body><h1>Hello</h1></body></html>"))
	return out
}

func TestWaveStripDefuddleEnvelope(t *testing.T) {
	raw := "---\ntitle: \"T\"\nsource: \"u\"\nword_count: 17\n---\n\nBody line.\n"
	if got := strings.TrimSpace(stripDefuddleEnvelope(raw)); got != "Body line." {
		t.Fatalf("stripDefuddleEnvelope=%q", got)
	}
	if got := stripDefuddleEnvelope("# Just markdown"); got != "# Just markdown" {
		t.Fatalf("envelope-less body must pass through, got %q", got)
	}
}

// ocrTestConverter is a plain Converter that returns empty text for PDFs (a
// "scan") plus an OCR-capable escalation path, mirroring pythonConverter.
type ocrTestConverter struct{}

func (ocrTestConverter) Convert(_ context.Context, kind, _ string, _ []byte) (string, error) {
	if kind == "pdf" {
		return "", nil // scanned page: empty text layer
	}
	return "web body", nil
}

func (ocrTestConverter) ConvertOCR(_ context.Context, _, _ string, _ []byte) (string, error) {
	return strings.Repeat("OCR TEXT ", 120), nil
}

func TestWaveOCREscalationRescuesScannedPDF(t *testing.T) {
	pdfTransport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(r, http.StatusOK, "application/pdf", "%PDF-1.7 scanned pages"), nil
	})
	h := mustNew(t, Options{
		ContactEmail: "test@example.org",
		CacheDir:     t.TempDir(),
		Client:       &http.Client{Transport: pdfTransport},
		Chrome:       &http.Client{Transport: pdfTransport},
		Jina: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return response(nil, http.StatusNotFound, "text/plain", ""), nil
		})},
		OA: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return response(nil, http.StatusNotFound, "application/json", `{}`), nil
		})},
		Converter: ocrTestConverter{},
	})
	result := h.Fetch(context.Background(), "https://journals.example.test/scanned-paper.pdf")
	if result.Error != "" || result.Method != "pdf:ocr" {
		t.Fatalf("OCR rung should rescue the scan: method=%q err=%q", result.Method, result.Error)
	}
	if !strings.Contains(result.Content, "OCR TEXT") {
		t.Fatalf("OCR content missing: %q", result.Content[:80])
	}
	found := false
	for _, rung := range result.Rungs {
		if rung == "ocr" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the ocr rung must appear in the rescue trace: %#v", result.Rungs)
	}
}

func TestWavePMCOAPDFURLRewritesDeadFTP(t *testing.T) {
	payload := `<OA><records><record id="PMC10450651">` +
		`<link format="tgz" href="ftp://ftp.ncbi.nlm.nih.gov/pub/pmc/oa_package/67/4a/PMC10450651.tar.gz"/>` +
		`<link format="pdf" href="ftp://ftp.ncbi.nlm.nih.gov/pub/pmc/oa_pdf/b8/d6/pnas.202302738.PMC10450651.pdf"/>` +
		`</record></records></OA>`
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(r, http.StatusOK, "text/xml", payload), nil
	})}
	url2, err := PMCOAPDFURL(context.Background(), client, "PMC10450651")
	// NCBI's own ftp href is DEAD; only the deprecated/ https path serves the file.
	want := "https://ftp.ncbi.nlm.nih.gov/pub/pmc/deprecated/oa_pdf/b8/d6/pnas.202302738.PMC10450651.pdf"
	if err != nil || url2 != want {
		t.Fatalf("PMCOAPDFURL=%q err=%v want=%q", url2, err, want)
	}
}

func TestWaveStatsScoreboardRoundtrip(t *testing.T) {
	dir := t.TempDir()
	h := mustNew(t, Options{CacheDir: dir})
	h.recordStat("https://a.example/x", Result{Method: "jina"})
	h.recordStat("https://b.example/y", Result{Error: "timeout", ErrorKind: "timeout"})
	h.recordStat("https://c.example/z", Result{Method: "jina"})
	buckets, err := SummarizeStats(dir, 100)
	if err != nil {
		t.Fatalf("SummarizeStats: %v", err)
	}
	got := buckets["jina"]
	if got == nil || got.Total != 2 || got.OK != 2 || got.Rate != 1.0 {
		t.Fatalf("jina bucket=%#v", got)
	}
	fails := buckets["timeout"]
	if fails == nil || fails.Total != 1 || fails.Rate != 0.0 {
		t.Fatalf("timeout bucket=%#v", fails)
	}
}

func TestWaveStatsAllCorruptIsDistinctFromNoData(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, statsFilename), []byte("{broken\nnot-json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if buckets, err := SummarizeStats(dir, 100); err == nil {
		t.Fatalf("all-corrupt stats returned healthy empty result: %#v", buckets)
	}
}

func TestWaveStatsWrittenByRealFetch(t *testing.T) {
	// The scoreboard provably RUNS on the live dispatch path — an absent stats.jsonl
	// after a real fetch means the recorder never fired (coincidence-detector failure).
	dir := t.TempDir()
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return response(r, http.StatusNotFound, "application/json", `{}`), nil
	})}
	h := mustNew(t, Options{
		ContactEmail: "test@example.org",
		CacheDir:     dir,
		Client:       client,
		Chrome:       client,
		Jina:         client,
		OA:           client,
	})
	_ = h.Fetch(context.Background(), "10.9999/no-copy")
	if _, err := os.Stat(filepath.Join(dir, "stats.jsonl")); err != nil {
		t.Fatalf("stats.jsonl missing after a real fetch (recorder never fired): %v", err)
	}
}
