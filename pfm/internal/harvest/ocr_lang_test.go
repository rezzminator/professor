package harvest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOCRLangReachesTheConverterAndTheLatinDefaultIsFlagged: a local scan
// whose conversion carries converter.py's Latin-default note comes back
// partial, naming the assumption and ocr_language; with ocr_language "ar" the
// converter is handed "ar" (a fresh read, never the cached Latin copy); an
// unknown script is a named error listing the staged ones.
func TestOCRLangReachesTheConverterAndTheLatinDefaultIsFlagged(t *testing.T) {
	scan := filepath.Join(t.TempDir(), "scan.pdf")
	if err := os.WriteFile(scan, []byte("%PDF-1.7\n% a scan\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var seen []string
	converter := &browserSpyConverter{convertFn: func(ctx context.Context, _, _ string, _ []byte) (string, error) {
		lang := OCRLangFrom(ctx)
		seen = append(seen, lang)
		if lang == "ar" {
			return "_Converter note: OCR read page(s) 1 of 1 as Arabic (ocr_language 'ar' was requested)_\n\nنص عربي مقروء من الصفحة الممسوحة ضوئيا", nil
		}
		return "_Converter note: OCR read page(s) 1 of 1 as Latin (the document names no language, so Latin by default" +
			" — pass ocr_language to read it in another script (script detection before OCR is unmeasured))_\n\n" +
			"garbled latin letters read from an arabic scan page", nil
	}}
	h := mustNew(t, Options{CacheDir: t.TempDir(), Converter: converter, BrowserRung: browserOff()})
	plain := h.FetchPublic(context.Background(), scan, FetchOptions{})
	if plain.Error != "" || plain.Partial != ocrAssumedPartial {
		t.Fatalf("unlabelled scan: partial %q (error %q), want %q", plain.Partial, plain.Error, ocrAssumedPartial)
	}
	// PublicGaps splits a partial reason on "; ": the Latin-default reason is
	// one gap, so it carries no "; " of its own.
	if strings.Contains(ocrAssumedPartial, "; ") {
		t.Fatalf("ocrAssumedPartial %q contains \"; \", which PublicGaps reads as a gap boundary", ocrAssumedPartial)
	}
	if gaps := PublicGaps(plain.Partial); len(gaps) != 1 || gaps[0] != ocrAssumedPartial {
		t.Fatalf("unlabelled scan: gaps %q, want the one gap [%q]", gaps, ocrAssumedPartial)
	}
	arabic := h.FetchPublic(context.Background(), scan, FetchOptions{OCRLang: "ar"})
	if arabic.Error != "" || arabic.Partial != "" || !strings.Contains(arabic.Content, "نص عربي") {
		t.Fatalf("ocr_language ar: partial %q error %q content %q", arabic.Partial, arabic.Error, arabic.Content)
	}
	if len(seen) != 2 || seen[0] != "" || seen[1] != "ar" {
		t.Fatalf("converter saw ocr_language %q, want [\"\" \"ar\"] (the second read fresh)", seen)
	}
	if _, err := ParseOCRLang("klingon"); err == nil || !strings.Contains(err.Error(), "latin, zh, ja, ar, ru, he") {
		t.Fatalf("ParseOCRLang(klingon) = %v, want a named error listing the staged scripts", err)
	}
}
