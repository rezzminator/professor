package harvest

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// inflatingConverter is a converter that opens xz (harvest.Inflater) the way
// the sidecar does: it answers the inner body it was built with, or err.
type inflatingConverter struct {
	inner []byte
	err   error
}

func (c *inflatingConverter) Convert(_ context.Context, kind, _ string, body []byte) (string, error) {
	return "converted " + kind + ": " + string(body), nil
}

func (c *inflatingConverter) Inflate(_ context.Context, codec string, _ []byte, limit int64) ([]byte, error) {
	if codec != kindXZ || limit != compressedDocumentCap {
		return nil, errors.New("inflate asked for " + codec + " under the wrong cap")
	}
	return c.inner, c.err
}

// TestXZDocumentInflatedByTheConverterAndRoutedByInnerBytes: an xz document
// is no longer refused; the converter inflates it under the compressed-
// document cap and the inner BYTES pick the route (a PDF named .xz is a PDF);
// a bomb, a file-only inner body and a converter without xz are named.
func TestXZDocumentInflatedByTheConverterAndRoutedByInnerBytes(t *testing.T) {
	xz := []byte("\xfd7zXZ\x00\x00\x04\xe6\xd6\xb4\x46")
	inflating := &Harvester{options: Options{Converter: &inflatingConverter{inner: []byte("%PDF-1.7\n")}}}
	if found := resolveFormat("notes.xz", xz, inflating.inflater(context.Background())); found.class == formatRefused {
		t.Fatalf("resolveFormat(xz) = refused (%s), want it left to the converter", found.reason)
	}
	convert := func(converter Converter) (string, error) {
		h := &Harvester{options: Options{Converter: converter}}
		return h.convertFetchedContent(context.Background(), classifyFetchedKind("paper.xz", "", xz), "paper.xz", xz)
	}
	got, err := convert(&inflatingConverter{inner: []byte("%PDF-1.7\n1 0 obj\n")})
	if err != nil || !strings.HasPrefix(got, "converted pdf") {
		t.Errorf("xz over a PDF = %q, %v; want the inner bytes routed to the pdf converter", got, err)
	}
	got, err = convert(&inflatingConverter{inner: []byte("plain notes inside an xz stream\n")})
	if err != nil || !strings.Contains(got, "plain notes inside an xz stream") {
		t.Errorf("xz over text = %q, %v; want the text", got, err)
	}
	failures := map[string]struct {
		converter Converter
		want      string
	}{
		"bomb":      {&inflatingConverter{err: ErrDecompressionBomb}, "decompression bomb"},
		"file-only": {&inflatingConverter{inner: []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")}, "not a document"},
		"no xz":     {&fakeConverter{}, "no xz decoder"},
	}
	for name, failure := range failures {
		if _, err := convert(failure.converter); err == nil || !strings.Contains(err.Error(), failure.want) {
			t.Errorf("%s: err = %v, want it named %q", name, err, failure.want)
		}
	}
}
