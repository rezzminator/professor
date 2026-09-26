package harvest

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// deadlineOCRConverter converts every PDF to empty text, so the DOI mirror
// forces its OCR rescue, and records how much time that rescue was given.
// With wait set it blocks until the rescue's context ends and returns its
// error, as the Python worker does when the process is killed at the deadline.
type deadlineOCRConverter struct {
	wait      bool
	remaining time.Duration
	deadline  bool
}

func (c *deadlineOCRConverter) Convert(_ context.Context, _, _ string, _ []byte) (string, error) {
	return "", nil
}

func (c *deadlineOCRConverter) ConvertOCR(ctx context.Context, _, _ string, _ []byte) (string, error) {
	var deadline time.Time
	deadline, c.deadline = ctx.Deadline()
	c.remaining = time.Until(deadline)
	if c.wait {
		<-ctx.Done()
		return "", fmt.Errorf("OCR worker stopped: %w", ctx.Err())
	}
	return "OCR recovered the scanned mirror PDF", nil
}

// scannedMirrorPDF is a PDF of pages uncompressed page objects; pages 0 is a
// PDF whose page tree sits in a compressed object stream, so no page count
// is visible.
func scannedMirrorPDF(pages int) string {
	var body strings.Builder
	body.WriteString("%PDF-1.7\n")
	if pages == 0 {
		body.WriteString("1 0 obj << /Type /ObjStm /N 5 /Filter /FlateDecode >> stream\nx\nendstream endobj\n")
	} else {
		fmt.Fprintf(&body, "1 0 obj << /Type /Pages /Count %d >> endobj\n", pages)
	}
	for page := 0; page < pages; page++ {
		fmt.Fprintf(&body, "%d 0 obj << /Type /Page /Parent 1 0 R >> endobj\n", page+2)
	}
	body.WriteString("%%EOF\n")
	return body.String()
}

func doiMirrorOCRHarvester(t *testing.T, pdf string, converter *deadlineOCRConverter) *Harvester {
	t.Helper()
	withPublicDNSForProviderTest(t)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Host != "doi-mirror.test" {
			return nil, errors.New("unexpected DOIMirror request: " + r.Method + " " + r.URL.String())
		}
		return response(r, http.StatusOK, "application/pdf", pdf), nil
	})}
	return mustNew(t, Options{
		CacheDir:     t.TempDir(),
		Client:       client,
		Chrome:       client,
		Converter:    converter,
		DOIMirrorURL: "https://doi-mirror.test/",
	})
}

// A forced OCR run of a scanned mirror PDF gets at least docling's own
// document_timeout (converter.py ocr_timeout: max(30, 12 × flagged pages)
// seconds) plus 10 s, never more than the 15-minute cap; a PDF showing no
// page count gets the cap.
func TestDOIMirrorForcedOCRDeadlineCoversDoclingsLimit(t *testing.T) {
	const ocrCap = 15 * time.Minute
	for _, tc := range []struct {
		name  string
		pages int
		least time.Duration
	}{
		{"4 pages", 4, 58 * time.Second},
		{"20 pages", 20, 250 * time.Second},
		{"no visible page count", 0, ocrCap - time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			converter := &deadlineOCRConverter{}
			h := doiMirrorOCRHarvester(t, scannedMirrorPDF(tc.pages), converter)
			got := h.fetchDOIMirror(context.Background(), doiMirrorFixtureDOI, FetchOptions{})
			if got.Error != "" {
				t.Fatalf("forced OCR of a %d-page mirror PDF = %#v", tc.pages, got)
			}
			if !converter.deadline {
				t.Fatalf("the OCR rescue of a %d-page PDF ran without a deadline", tc.pages)
			}
			// the rescue measures its remaining time a moment after the
			// deadline was set; a second covers that, never a 45 s cut
			if converter.remaining < tc.least-time.Second || converter.remaining > ocrCap {
				t.Fatalf("OCR rescue of a %d-page PDF got %s, want between %s and the %s cap",
					tc.pages, converter.remaining, tc.least, ocrCap)
			}
		})
	}
}

// The deadline formula itself: docling's limit plus the margin, exactly, and
// the named cap past it or for an unknown page count.
func TestDOIMirrorOCRTimeoutFormula(t *testing.T) {
	for pages, want := range map[int]time.Duration{
		0:    doiMirrorOCRCap,
		1:    40 * time.Second,
		4:    58 * time.Second,
		20:   250 * time.Second,
		74:   doiMirrorOCRCap - 2*time.Second,
		75:   doiMirrorOCRCap,
		1000: doiMirrorOCRCap,
	} {
		if got := doiMirrorOCRTimeout(pages); got != want {
			t.Errorf("doiMirrorOCRTimeout(%d) = %s, want %s", pages, got, want)
		}
	}
	for _, pages := range []int{0, 4, 20} {
		if got := pdfPageCount([]byte(scannedMirrorPDF(pages))); got != pages {
			t.Errorf("pdfPageCount of a %d-page PDF = %d", pages, got)
		}
	}
}

// A rescue that hits its deadline ends in a named timeout failure, never an
// empty page.
func TestDOIMirrorForcedOCRDeadlineIsANamedFailure(t *testing.T) {
	converter := &deadlineOCRConverter{wait: true}
	h := doiMirrorOCRHarvester(t, scannedMirrorPDF(4), converter)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	got := h.fetchDOIMirror(ctx, doiMirrorFixtureDOI, FetchOptions{})
	if got.ErrorKind != errorKindTimeout {
		t.Fatalf("OCR deadline hit: kind = %q, want %q; result %#v", got.ErrorKind, errorKindTimeout, got)
	}
	for _, want := range []string{"OCR", "4 page", "deadline"} {
		if !strings.Contains(got.Error, want) {
			t.Fatalf("OCR deadline failure %q does not name %q", got.Error, want)
		}
	}
}
