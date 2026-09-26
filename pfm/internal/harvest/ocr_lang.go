package harvest

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// OCRScripts are the staged OCR script sets a caller may name as ocr_language
// (converter.py's OCR_SCRIPTS): one script per conversion, since RapidOCR
// loads one language and script detection before OCR is unmeasured.
var OCRScripts = []string{"latin", "zh", "ja", "ar", "ru", "he"}

// ocrAssumedPartial is the result's partial reason when OCR ran on a scan
// that names no language: the converter read it as Latin.
const ocrAssumedPartial = "OCR read the scan as Latin script: the document names no language, so" +
	" Latin by default — pass ocr_language to read it in another script"

// ocrAssumedNote is the converter's own note for that default (converter.py
// OCR_LATIN_ASSUMED, inside "_Converter note: OCR read page(s) … as Latin (…)").
const ocrAssumedNote = "as Latin (the document names no language, so Latin by default"

const converterNotePrefix = "_Converter note: OCR read page(s) "

type ocrLangKey struct{}

// ParseOCRLang validates a caller's ocr_language: "" (the document decides) or
// one of OCRScripts; anything else is a named error listing them.
func ParseOCRLang(raw string) (string, error) {
	lang := strings.ToLower(strings.TrimSpace(raw))
	if lang == "" || slices.Contains(OCRScripts, lang) {
		return lang, nil
	}
	return "", fmt.Errorf(
		"ocr_language %q is not a staged OCR script; use one of %s",
		raw,
		strings.Join(OCRScripts, ", "),
	)
}

// withOCRLang carries the fetch's ocr_language to the converter (OCRLangFrom).
// The cache is keyed by source alone, so a read in a named script is always
// a fresh read, never an earlier conversion in another script.
func withOCRLang(ctx context.Context, options FetchOptions) (context.Context, FetchOptions) {
	if options.OCRLang == "" {
		return ctx, options
	}
	options.Refresh = true
	return context.WithValue(ctx, ocrLangKey{}, options.OCRLang), options
}

// OCRLangFrom is the ocr_language a converter adapter hands its OCR engine; ""
// lets the document's own text layer, /Lang or metadata decide.
func OCRLangFrom(ctx context.Context) string {
	lang, _ := ctx.Value(ocrLangKey{}).(string)
	return lang
}

// convertedDocument is a converter's document text through pageText, flagged
// partial when OCR read it as Latin only because it names no language.
func convertedDocument(converted string) string {
	text := pageText(converted)
	for line := range strings.Lines(text) {
		if strings.HasPrefix(line, converterNotePrefix) && strings.Contains(line, ocrAssumedNote) {
			return withPartial(text, ocrAssumedPartial)
		}
	}
	return text
}
