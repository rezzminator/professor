package harvestpy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestConverterDispatchTakesTheDetectedFormatKinds: the Go side routes a
// text or Office format by its bytes under its own kind (format_detect.go);
// convert() has one dispatch table for them, where a later parser replaces
// today's reading (bibtex and the Office readers need the pinned
// interpreter: TestFormatParsersMatchTheBakeOffRecall,
// TestOfficeFormatsMatchTheBakeOff). Watched FAILING before _CONVERTERS:
// every kind below answered "unsupported conversion kind".
func TestConverterDispatchTakesTheDetectedFormatKinds(t *testing.T) {
	converter := realPythonConverter(t)
	dir := t.TempDir()
	for kind, body := range map[string]string{
		"vtt": "WEBVTT\n\n00:00:00.000 --> 00:00:01.000\nHello caption\n",
		"srt": "1\n00:00:00,000 --> 00:00:01,000\nHello caption\n",
		"ris": "TY  - JOUR\nTI  - Hello caption\nER  - \n",
		"eml": "From: sender@example.com\nSubject: Hello caption\n\nBody\n",
	} {
		document := filepath.Join(dir, "input."+kind)
		if err := os.WriteFile(document, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := converter.Convert(context.Background(), Request{Path: document, Kind: kind})
		if err != nil || !strings.Contains(got.Markdown, "Hello caption") {
			t.Errorf("%s: want its text through the dispatch table, got %q, err %v", kind, got.Markdown, err)
		}
	}
	// The Office kinds the Go side routes (format_detect.go) each have a
	// dispatch entry: a body their reader cannot open fails in that reader,
	// never as an unknown kind.
	for _, kind := range []string{
		"doc", "xls", "rtf", "odt", "ods", "odp", "encrypted",
		"docm", "dotx", "dotm", "xlsm", "xltx", "xltm", "pptm", "potx", "potm", "ppsx", "ppsm",
	} {
		document := filepath.Join(dir, "input."+kind)
		if err := os.WriteFile(document, []byte("not an office body"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := converter.Convert(context.Background(), Request{Path: document, Kind: kind})
		if err != nil && strings.Contains(err.Error(), "unsupported conversion kind") {
			t.Errorf("%s: want a dispatch entry, got %v", kind, err)
		}
	}
}
