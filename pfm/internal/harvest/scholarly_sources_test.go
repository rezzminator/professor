package harvest

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

const providerFixtureDOI = "10.1234/provider.fixture"

func TestProviderDownloadLimitRejectsOversizedPartialResponse(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	withPublicDNSForProviderTest(t)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(r, http.StatusOK, "application/pdf", "%PDF-1.7\nthis exceeds the configured limit\n%%EOF"), nil
	})}
	h := mustNew(
		t,
		Options{CacheDir: t.TempDir(), Client: client, Chrome: client, MaxBytes: 16, Converter: &fakeConverter{}},
	)
	got := h.fetchProviderArtifact(
		context.Background(),
		providerFixtureDOI,
		"doi-viewer",
		"https://doi-viewer.test/file.pdf",
		"",
		"",
		FetchOptions{},
		[]string{"doi-viewer"},
	)
	if got.Error == "" || got.ErrorKind != "too_large" || got.Content != "" {
		t.Fatalf("oversized provider response = %#v; want bounded failure", got)
	}
}

func TestProviderDownloadRejectsShortReadInsteadOfConvertingPartialBody(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	withPublicDNSForProviderTest(t)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"application/pdf"}},
			Body:       shortReadBody{reader: strings.NewReader("%PDF-1.7\npartial")},
			Request:    r,
		}, nil
	})}
	h := mustNew(
		t,
		Options{CacheDir: t.TempDir(), Client: client, Chrome: client, MaxBytes: 1024, Converter: &fakeConverter{}},
	)
	got := h.fetchProviderArtifact(
		context.Background(),
		providerFixtureDOI,
		"doi-viewer",
		"https://doi-viewer.test/file.pdf",
		"",
		"",
		FetchOptions{},
		[]string{"doi-viewer"},
	)
	if got.Error == "" || got.Content != "" || got.Path != "" {
		t.Fatalf("short provider response was accepted: %#v", got)
	}
}

type shortReadBody struct {
	reader *strings.Reader
}

func (body shortReadBody) Read(p []byte) (int, error) {
	n, _ := body.reader.Read(p)
	if n > 0 {
		return n, io.ErrUnexpectedEOF
	}
	return 0, io.EOF
}

func (body shortReadBody) Close() error { return nil }

func TestProviderPDFEmptyConversionErrorEscalatesToOCR(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	withPublicDNSForProviderTest(t)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(r, http.StatusOK, "application/pdf", "%PDF-1.7\nocr fixture\n%%EOF"), nil
	})}
	converter := &emptyPDFThenOCRConverter{}
	h := mustNew(t, Options{CacheDir: t.TempDir(), Client: client, Chrome: client, Converter: converter})
	got := h.fetchProviderArtifact(
		context.Background(),
		providerFixtureDOI,
		"doi-viewer",
		"https://doi-viewer.test/file.pdf",
		"",
		"",
		FetchOptions{},
		[]string{"doi-viewer"},
	)
	if got.Error != "" || got.Content != "OCR recovered provider fixture" ||
		!containsProviderString(got.Rungs, "ocr") ||
		converter.ocrCalls != 1 {
		t.Fatalf("provider OCR recovery = %#v calls=%d", got, converter.ocrCalls)
	}
}

type emptyPDFThenOCRConverter struct{ ocrCalls int }

func (c emptyPDFThenOCRConverter) Convert(_ context.Context, kind, _ string, _ []byte) (string, error) {
	if kind == "pdf" {
		return "", errors.New("EMPTY-text conversion")
	}
	return "", nil
}

func (c *emptyPDFThenOCRConverter) ConvertOCR(_ context.Context, _, _ string, _ []byte) (string, error) {
	c.ocrCalls++
	return "OCR recovered provider fixture", nil
}

func containsProviderString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
