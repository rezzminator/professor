package harvestpy

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ocrRulesProbe drives converter.py's OCR rules under the pinned interpreter
// on PDFs it builds with pymupdf, and prints one JSON object of verdicts.
const ocrRulesProbe = `
import json, os, pathlib, sys, tempfile
sys.path.insert(0, sys.argv[1])
import converter as c
import pymupdf
fixtures = pathlib.Path(sys.argv[2])
out = {}
out["timeout"] = [c.ocr_timeout(n) for n in (1, 2, 3, 10)]

def one(build, lang=None, title=None):
    doc = pymupdf.open()
    page = doc.new_page(width=400, height=300)
    build(page)
    if lang:
        doc.xref_set_key(doc.pdf_catalog(), "Lang", pymupdf.get_pdf_str(lang))
    if title:
        doc.set_metadata({"title": title})
    return doc

text = "A clean born-digital text layer that carries enough words to read."
clean = one(lambda p: p.insert_text((20, 50), text, fontsize=9))
blank = one(lambda p: None)
def scanned(p):
    p.insert_image(p.rect, pixmap=pymupdf.Pixmap(pymupdf.csRGB, pymupdf.IRect(0, 0, 64, 48), 0))
def scanned_with_layer(p):
    scanned(p)
    p.insert_text((20, 50), text, fontsize=9, render_mode=3)
nounicode = pymupdf.open(str(fixtures / "no_tounicode.pdf"))
out["needs_ocr"] = {
    "clean": c.ocr_page_needed(c.page_features(clean[0])),
    "blank": c.ocr_page_needed(c.page_features(blank[0])),
    "scanned": c.ocr_page_needed(c.page_features(one(scanned)[0])),
    "invisible_ocr_layer": c.ocr_page_needed(c.page_features(one(scanned_with_layer)[0])),
    "no_tounicode": c.ocr_page_needed(c.page_features(nounicode[0])),
}
out["script"] = {
    "layer": c.choose_ocr_script(clean),
    "lang": c.choose_ocr_script(one(scanned, lang="ja-JP")),
    "title": c.choose_ocr_script(one(scanned, title="Война и мир")),
    "none": c.choose_ocr_script(one(scanned)),
    "requested": c.choose_ocr_script(one(scanned, lang="ja-JP"), "ar"),
}
with tempfile.TemporaryDirectory() as scratch:
    caption = pathlib.Path(scratch) / "caption.pdf"
    doc = pymupdf.open()
    page = doc.new_page(width=400, height=300)
    pix = pymupdf.Pixmap(pymupdf.csRGB, pymupdf.IRect(0, 0, 150, 100), 0)
    pix.clear_with(180)
    page.insert_image(pymupdf.Rect(20, 20, 350, 150), pixmap=pix, keep_proportion=False)
    page.insert_text((20, 155), "[Figure 1.A] A caption begins across the image boundary.", fontsize=10)
    page.insert_text((20, 170), "The caption continues below it.", fontsize=10)
    doc.save(caption)
    out["caption"] = c.convert({"path": str(caption), "kind": "pdf"})
os.environ["PATH"] = ""
out["hebrew_limit"] = c._hebrew_limit()
with tempfile.TemporaryDirectory() as scratch:
    hebrew = pathlib.Path(scratch) / "hebrew.pdf"
    one(scanned, lang="he").save(str(hebrew))
    out["hebrew_convert"] = c.convert({"path": str(hebrew), "kind": "pdf"})
print(json.dumps(out, ensure_ascii=False))
`

// TestOCRRulesNameTheirChoices: the prepass rule R3 (bake-off prepass.py),
// the timeout formula max(30, 12 x flagged pages), the script choice from
// the text layer, /Lang or title metadata else Latin — each with its reason —
// and a Hebrew page without tesseract failing as "no Hebrew OCR".
func TestOCRRulesNameTheirChoices(t *testing.T) {
	python := os.Getenv("HARVESTPY_CORPUS_PYTHON")
	if python == "" {
		t.Skip("HARVESTPY_CORPUS_PYTHON is not set; the OCR rules need the pinned interpreter")
	}
	scriptDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(scriptDir, "converter.py"), ConverterSource(), 0o600); err != nil {
		t.Fatal(err)
	}
	fixtures, err := filepath.Abs(filepath.Join("testdata", "formats", "ocr"))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(python, "-c", ocrRulesProbe, scriptDir, fixtures)
	command.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1", "HARVESTPY_MODEL_ROOT="+t.TempDir())
	output, err := command.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			t.Fatalf("OCR rules probe failed: %v\n%s", err, exit.Stderr)
		}
		t.Fatalf("OCR rules probe failed: %v", err)
	}
	var got struct {
		Timeout      []float64                  `json:"timeout"`
		NeedsOCR     map[string]bool            `json:"needs_ocr"`
		Script       map[string][2]string       `json:"script"`
		HebrewLimit  string                     `json:"hebrew_limit"`
		HebrewResult map[string]json.RawMessage `json:"hebrew_convert"`
		Caption      Result                     `json:"caption"`
	}
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("decode probe output: %v\n%s", err, output)
	}
	if want := []float64{30, 30, 36, 120}; !equalFloats(got.Timeout, want) {
		t.Errorf("ocr_timeout(1,2,3,10) = %v, want %v", got.Timeout, want)
	}
	if !strings.Contains(got.Caption.Markdown, "[Figure 1.A] A caption begins across the image boundary.") {
		t.Errorf("caption first line was lost: %q", got.Caption.Markdown)
	}
	wantOCR := map[string]bool{
		"clean": false, "blank": true, "scanned": true, "invisible_ocr_layer": false, "no_tounicode": true,
	}
	for page, want := range wantOCR {
		if got.NeedsOCR[page] != want {
			t.Errorf("R3 on %s page = %v, want %v", page, got.NeedsOCR[page], want)
		}
	}
	wantScript := map[string][2]string{
		"layer": {"latin", "text layer"}, "lang": {"ja", "/Lang"},
		"title": {"ru", "title"}, "none": {"latin", "the document names no language, so Latin by default"},
		"requested": {"ar", "ocr_language 'ar' was requested"},
	}
	for source, want := range wantScript {
		choice := got.Script[source]
		if choice[0] != want[0] || !strings.Contains(choice[1], want[1]) {
			t.Errorf("script from %s = %q, want %q with a reason naming %q", source, choice, want[0], want[1])
		}
	}
	if !strings.HasPrefix(got.HebrewLimit, "no Hebrew OCR") {
		t.Errorf("tesseract off PATH: Hebrew limit = %q, want it named \"no Hebrew OCR\"", got.HebrewLimit)
	}
	if string(got.HebrewResult["ok"]) != "false" || string(got.HebrewResult["error_class"]) != `"OCRUnavailable"` ||
		!strings.Contains(string(got.HebrewResult["error"]), "no Hebrew OCR") {
		t.Errorf(
			"a Hebrew scan without tesseract = %v, want ok:false OCRUnavailable naming \"no Hebrew OCR\"",
			got.HebrewResult,
		)
	}
}

// The loss audit is independent of a layout engine and runs in the ordinary gate.
func TestPDFConversionReportsTextLoss(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "converter.py"), ConverterSource(), 0o600); err != nil {
		t.Fatal(err)
	}
	const probe = `
import sys, types, pathlib
sys.path.insert(0, sys.argv[1])
import converter as c
class Page:
    number = 0
    def get_text(self, *args, **kwargs): return self.text
    def get_image_info(self): return []
class Document:
    page_count = 1
    def __enter__(self): return self
    def __exit__(self, *args): pass
    def __iter__(self): return iter([page])
    def __getitem__(self, index): return page
page = Page()
sys.modules['pymupdf'] = types.SimpleNamespace(open=lambda _: Document(), TEXTFLAGS_TEXT=0, TEXT_PRESERVE_LIGATURES=0)
c.page_features = lambda _: {}
c.ocr_page_needed = lambda _: False
cases = [
    ('missing caption', '[Figure 1.A] First line.\nRemaining text.', 'Remaining text.', True),
    ('repeated word', 'repeat repeat', 'repeat', True),
    ('ligature corruption', 'offline', 'offine', True),
    ('normalized ligature', 'o\ufb04ine', 'offline', False),
    ('formatting', 'A caption', '**A** caption', False),
    ('punctuation', 'Value = 3', 'Value 3', True),
    ('multiplication', 'Value 3 * 4', 'Value 3 4', True),
    ('identifier underscore', 'field_name', 'fieldname', True),
    ('standalone underscore', 'left _ right', 'left right', True),
    ('tilde', 'Estimate ~ 3', 'Estimate 3', True),
    ('backtick', 'literal ' + chr(96) + ' marker', 'literal marker', True),
    ('backslash', r'path \ segment', 'path segment', True),
]
for name, source, markdown, missing in cases:
    page.text = source
    c._pymupdf4llm_markdown = lambda *a, **kw: markdown
    result = c.convert({'path': 'fixture.pdf', 'kind': 'pdf'})
    assert result['ok'], (name, result)
    assert ('_Converter gap:' in result['markdown']) == missing, (name, result)
    if name == 'missing caption':
        assert source in result['markdown'], result
page.text = 'Readable native text'
def broken(*a, **kw): raise RuntimeError('text layer unreadable')
page.get_text = broken
result = c.convert({'path': 'fixture.pdf', 'kind': 'pdf'})
assert not result['ok'] or '_Converter gap:' in result['markdown'], result
print('PDF loss audit passed')
`
	cmd := exec.Command(python, "-c", probe, dir)
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("PDF loss audit: %v\n%s", err, out)
	}
}

func equalFloats(got, want []float64) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

// TestOCRMissingModelIsANamedFailure: a page the prepass flags, read with no
// staged models, fails by name — never a silent download, never the garbage
// text layer as if it were the document.
func TestOCRMissingModelIsANamedFailure(t *testing.T) {
	python := os.Getenv("HARVESTPY_CORPUS_PYTHON")
	if python == "" {
		t.Skip("HARVESTPY_CORPUS_PYTHON is not set; the OCR path needs the pinned interpreter")
	}
	t.Setenv("PYTHONDONTWRITEBYTECODE", "1")
	script := filepath.Join(t.TempDir(), "converter.py")
	if err := os.WriteFile(script, ConverterSource(), 0o600); err != nil {
		t.Fatal(err)
	}
	converter := NewConverter(Runtime{Python: python, Script: script, ModelRoot: t.TempDir()})
	t.Cleanup(func() { _ = converter.Close() })
	for _, name := range []string{"scan_latin.pdf", "no_tounicode.pdf"} {
		path, _ := formatFixture(t, filepath.Join("ocr", name))
		result, err := converter.Convert(context.Background(), Request{Path: path, Kind: "pdf"})
		if err == nil || !errors.Is(err, ErrConverterFailed) || !strings.Contains(err.Error(), "OCRModelsNotStaged") ||
			!strings.Contains(err.Error(), "run `pfm install`") {
			t.Errorf(
				"%s with no staged models = %.200q, %v; want a named OCRModelsNotStaged failure",
				name,
				result.Markdown,
				err,
			)
		}
	}
}

// TestOCRReadsAScannedLatinPage is opt-in (HARVESTPY_OCR_MODEL_ROOT names a
// staged model root): the bake-off's Latin scan read offline, the script
// choice named in the output.
func TestOCRReadsAScannedLatinPage(t *testing.T) {
	python, models := os.Getenv("HARVESTPY_CORPUS_PYTHON"), os.Getenv("HARVESTPY_OCR_MODEL_ROOT")
	if python == "" || models == "" {
		t.Skip("HARVESTPY_CORPUS_PYTHON and HARVESTPY_OCR_MODEL_ROOT are not both set; OCR needs staged models")
	}
	script := filepath.Join(t.TempDir(), "converter.py")
	if err := os.WriteFile(script, ConverterSource(), 0o600); err != nil {
		t.Fatal(err)
	}
	converter := NewConverter(Runtime{Python: python, Script: script, ModelRoot: models})
	t.Cleanup(func() { _ = converter.Close() })
	path, _ := formatFixture(t, filepath.Join("ocr", "scan_latin.pdf"))
	result, err := converter.Convert(context.Background(), Request{Path: path, Kind: "pdf"})
	if err != nil {
		t.Fatalf("OCR of the Latin scan: %v", err)
	}
	for _, want := range []string{"as Latin (", "campaign against the Rowlatt Act"} {
		if !strings.Contains(result.Markdown, want) {
			t.Errorf("OCR markdown lacks %q:\n%.600s", want, result.Markdown)
		}
	}
}

// TestOCRModelRootReachesTheWorker: the worker learns the staged-model root
// from Runtime or from the interpreter's place under the harvest-python root;
// an inherited HARVESTPY_MODEL_STAGING never lets a read download.
func TestOCRModelRootReachesTheWorker(t *testing.T) {
	python := "/state/harvest-python/env/linux-arm64/abc/project/.venv/bin/python"
	env := workerEnv(
		[]string{"HARVESTPY_MODEL_STAGING=1", "HARVESTPY_MODEL_ROOT=/elsewhere", "KEEP=1"},
		Runtime{Python: python},
	)
	joined := strings.Join(env, "\n")
	if want := "HARVESTPY_MODEL_ROOT=/state/harvest-python/models"; !strings.Contains(joined, want) {
		t.Errorf("worker env lacks %s:\n%s", want, joined)
	}
	if strings.Contains(joined, "HARVESTPY_MODEL_STAGING") || strings.Contains(joined, "/elsewhere") ||
		!strings.Contains(joined, "KEEP=1") {
		t.Errorf("worker env kept an inherited protocol variable or dropped a plain one:\n%s", joined)
	}
	staging := strings.Join(workerEnv(nil, Runtime{Python: python, ModelRoot: "/m", ModelStaging: true}), "\n")
	if !strings.Contains(staging, "HARVESTPY_MODEL_ROOT=/m") ||
		!strings.Contains(staging, "HARVESTPY_MODEL_STAGING=1") {
		t.Errorf("staging worker env = %q", staging)
	}
}
