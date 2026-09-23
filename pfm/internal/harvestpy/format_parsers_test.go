package harvestpy

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// pinnedPythonConverter runs converter.py under the pinned interpreter
// (HARVESTPY_CORPUS_PYTHON): the format parsers need lxml and bibtexparser.
func pinnedPythonConverter(t *testing.T) *Converter {
	t.Helper()
	python := os.Getenv("HARVESTPY_CORPUS_PYTHON")
	if python == "" {
		t.Skip("HARVESTPY_CORPUS_PYTHON is not set; the format parsers need the pinned interpreter")
	}
	t.Setenv("PYTHONDONTWRITEBYTECODE", "1")
	script := filepath.Join(t.TempDir(), "converter.py")
	if err := os.WriteFile(script, ConverterSource(), 0o600); err != nil {
		t.Fatal(err)
	}
	converter := NewConverter(Runtime{Python: python, Script: script})
	t.Cleanup(func() { _ = converter.Close() })
	return converter
}

func formatFixture(t *testing.T, name string) (string, string) {
	t.Helper()
	path := filepath.Join("testdata", "formats", name)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return absolute, string(body)
}

func countOf(pattern, text string) int {
	return len(regexp.MustCompile(pattern).FindAllStringIndex(text, -1))
}

func captionLines(markdown string) int {
	_, lines, _ := strings.Cut(markdown, "\n\n")
	return len(strings.Split(strings.TrimSpace(lines), "\n"))
}

// TestFormatParsersMatchTheBakeOffRecall: each bake-off winner, ported into
// converter.py, reads the bake-off's own real file with the recall the
// bake-off measured (want = the winner's number in
// formats/<fmt>/table.md). The oracle is counted from the fixture's bytes,
// never from the converter's output. Watched FAILING with _CONVERTERS on
// the FM1 entries (an XML format through the HTML converter, a notebook as
// JSON, captions/citations/mail as raw text, MHTML and webarchive not parsed).
func TestFormatParsersMatchTheBakeOffRecall(t *testing.T) {
	converter := pinnedPythonConverter(t)
	convert := func(t *testing.T, name, kind string) (string, string) {
		t.Helper()
		path, body := formatFixture(t, name)
		got, err := converter.Convert(context.Background(), Request{Path: path, Kind: kind})
		if err != nil {
			t.Fatalf("%s as %s: %v", name, kind, err)
		}
		return got.Markdown, body
	}
	counted := func(t *testing.T, what string, got, oracle, table int) {
		t.Helper()
		if oracle != table || got != oracle {
			t.Errorf("%s: converter %d, fixture oracle %d, bake-off table %d", what, got, oracle, table)
		}
	}
	t.Run("feeds", func(t *testing.T) {
		for _, tc := range []struct {
			name, shape, item string
			table             int
		}{
			{"feed/pythonblog-rss2.xml", "RSS feed", `<item[ >]`, 50},
			{"feed/xkcd-atom.xml", "Atom feed", `<entry[ >]`, 4},
			{"feed/planetpython-rss10-first3.xml", "RDF/RSS 1.0 feed", `<item[ >]`, 3},
		} {
			markdown, body := convert(t, tc.name, "feed")
			counted(t, tc.name+" items", countOf(`(?m)^## `, markdown), countOf(tc.item, body), tc.table)
			if !strings.Contains(markdown, tc.shape) {
				t.Errorf("%s: want the %q header, got %.300q", tc.name, tc.shape, markdown)
			}
		}
	})
	t.Run("jats", func(t *testing.T) {
		markdown, body := convert(t, "jats/PMC7092803.nxml", "jats")
		_, references, found := strings.Cut(markdown, "## References\n\n")
		if !found {
			t.Fatalf("no References section: %.500q", markdown)
		}
		refs := countOf(`(?m)^- `, strings.Split(references, "\n\n")[0])
		counted(t, "references", refs, countOf(`<ref[ >]`, body), 17)
		sections := regexp.MustCompile(`<sec[^>]*>\s*(?:<label>[^<]*</label>\s*)?<title>([^<]+)</title>`)
		headed := 0
		for _, title := range sections.FindAllStringSubmatch(body, -1) {
			if strings.Contains(markdown, "# "+title[1]) {
				headed++
			}
		}
		counted(t, "section headings in order", headed, len(sections.FindAllString(body, -1)), 9)
		// Every other title (figure captions, appendix groups) is kept as text.
		for _, title := range regexp.MustCompile(`<title>([^<]+)</title>`).FindAllStringSubmatch(body, -1) {
			if !strings.Contains(markdown, title[1]) {
				t.Errorf("title %q is lost", title[1])
			}
		}
	})
	// Europe PMC's fullTextXML (live capture, trimmed) nests the ref-list and
	// the fn-group inside body sections, with no <back>. Watched FAILING with
	// walk_one not rendering ref-list or fn: References and Footnotes were
	// headings with nothing under them (0 of 17 refs live).
	t.Run("jats Europe PMC in-body ref-list and footnotes", func(t *testing.T) {
		markdown, body := convert(t, "jats/PMC7092803-europepmc.xml", "jats")
		heading := regexp.MustCompile(`(?m)^#+ References$`)
		if n := len(heading.FindAllString(markdown, -1)); n != 1 {
			t.Fatalf("want one References heading, got %d: %.500q", n, markdown)
		}
		references := markdown[heading.FindStringIndex(markdown)[1]:]
		refs := countOf(`(?m)^- `, strings.Split(strings.TrimLeft(references, "\n"), "\n\n")[0])
		counted(t, "references", refs, countOf(`<ref[ >]`, body), 17)
		footnotes := regexp.MustCompile(`(?m)^#+ Footnotes$`).FindStringIndex(markdown)
		if footnotes == nil {
			t.Fatalf("no Footnotes heading: %.500q", markdown)
		}
		group := regexp.MustCompile(`(?s)<fn-group>(.*?)</fn-group>`).FindStringSubmatch(body)
		if group == nil {
			t.Fatal("fixture carries no fn-group")
		}
		items := regexp.MustCompile(`(?s)<fn [^>]*>(.*?)</fn>`).FindAllStringSubmatch(group[1], -1)
		rendered := 0
		for _, item := range items {
			text := strings.Join(strings.Fields(regexp.MustCompile(`<[^>]+>`).ReplaceAllString(item[1], "")), " ")
			if strings.Contains(markdown[footnotes[1]:], "- "+text) {
				rendered++
			} else {
				t.Errorf("footnote %.80q is not rendered under Footnotes", text)
			}
		}
		counted(t, "footnotes", rendered, len(items), 2)
	})
	t.Run("fb2", func(t *testing.T) {
		markdown, body := convert(t, "fb2/minihelp-en.fb2", "fb2")
		paragraphs := regexp.MustCompile(`<p>([^<]+)</p>`).FindAllStringSubmatch(body, -1)
		kept := 0
		for _, p := range paragraphs {
			if strings.Contains(markdown, strings.Join(strings.Fields(p[1]), " ")) {
				kept++
			}
		}
		counted(t, "paragraphs (recall 1.0)", kept, len(paragraphs), 3)
	})
	t.Run("ipynb", func(t *testing.T) {
		markdown, body := convert(t, "ipynb/pdsh-errors.ipynb", "ipynb")
		var notebook struct {
			Cells []struct {
				Type    string          `json:"cell_type"`
				Source  json.RawMessage `json:"source"`
				Outputs []struct {
					Type  string `json:"output_type"`
					Ename string `json:"ename"`
				} `json:"outputs"`
			} `json:"cells"`
		}
		if err := json.Unmarshal([]byte(body), &notebook); err != nil {
			t.Fatal(err)
		}
		lines, keptLines, errs, keptErrs := 0, 0, 0, 0
		for _, cell := range notebook.Cells {
			var parts []string
			if err := json.Unmarshal(cell.Source, &parts); err != nil {
				var one string
				if err := json.Unmarshal(cell.Source, &one); err != nil {
					t.Fatal(err)
				}
				parts = strings.SplitAfter(one, "\n")
			}
			for _, line := range parts {
				// The bake-off counts a source line longer than 8 characters.
				if line = strings.TrimRight(line, "\n"); len(strings.TrimSpace(line)) > 8 {
					lines++
					if strings.Contains(markdown, line) {
						keptLines++
					}
				}
			}
			for _, output := range cell.Outputs {
				if output.Type == "error" {
					errs++
					if strings.Contains(markdown, "```text\n"+output.Ename+":") {
						keptErrs++
					}
				}
			}
		}
		counted(t, "cell source lines", keptLines, lines, 55)
		counted(t, "error outputs", keptErrs, errs, 4)
	})
	t.Run("captions", func(t *testing.T) {
		for _, tc := range []struct {
			name, kind  string
			cues, lines int
		}{
			{"subs/mediachrome-mashup.vtt", "vtt", 8, 8},
			{"subs/gaupol-extended.srt", "srt", 10, 14},
			{"subs/pysrt-bom-utf16le.srt", "srt", 7, 14},
		} {
			markdown, body := convert(t, tc.name, tc.kind)
			oracle := strings.Count(body, "-->")
			if strings.HasPrefix(body, "\xff\xfe") {
				oracle = strings.Count(body, "-\x00-\x00>\x00")
			}
			named := 0
			if match := regexp.MustCompile(`^Captions: (\d+) cues`).FindStringSubmatch(markdown); match != nil {
				named, _ = strconv.Atoi(match[1])
			}
			counted(t, tc.name+" cues", named, oracle, tc.cues)
			if got := captionLines(markdown); got != tc.lines {
				t.Errorf("%s: caption lines %d, bake-off line recall %d", tc.name, got, tc.lines)
			}
			if strings.Contains(markdown, "<") || strings.Contains(markdown, "\x00") {
				t.Errorf("%s: a tag or a NUL leaked: %.300q", tc.name, markdown)
			}
		}
	})
	t.Run("ris", func(t *testing.T) {
		markdown, body := convert(t, "ris/boinc-eon.ris", "ris")
		counted(t, "records", countOf(`(?m)^## `, markdown), countOf(`(?m)^TY  - `, body), 6)
		if !strings.Contains(markdown, "untested on messy exports") {
			t.Errorf("the messy-export follow-up is not named: %.300q", markdown)
		}
	})
	t.Run("bibtex", func(t *testing.T) {
		markdown, body := convert(t, "bibtex/plotly-book.bib", "bibtex")
		// The table: 102 of 103 entries parsed, 1 failed block (a bad macro),
		// every key seen; the failed block is named, never dropped silently.
		counted(t, "entries", countOf(`(?m)^- key: `, markdown), countOf(`(?m)^@\w+\{`, body)-1, 102)
		if !strings.Contains(markdown, "1 blocks could not be parsed") {
			t.Errorf("the failed block is not named: %.300q", markdown[max(0, len(markdown)-400):])
		}
	})
	t.Run("mhtml and webarchive", func(t *testing.T) {
		for _, tc := range []struct{ name, kind string }{
			{"mhtml/lcurl-api.mhtml", "mhtml"},
			{"webarchive/lcurl-api.webarchive", "webarchive"},
		} {
			markdown, _ := convert(t, tc.name, tc.kind)
			if !strings.Contains(markdown, "Create HTTP multipart/formdata object.") ||
				!strings.Contains(markdown, "Archived resources, not inlined") {
				t.Errorf("%s: want the page text and its named resources, got %.400q", tc.name, markdown)
			}
		}
	})
	t.Run("epub", func(t *testing.T) {
		markdown, _ := convert(t, "epub/formatting.epub", "epub")
		if got := countOf(`(?m)^#{1,6} `, markdown); got < 32 {
			t.Errorf("headings %d, bake-off markitdown 32/32", got)
		}
	})
	t.Run("eml", func(t *testing.T) {
		markdown, _ := convert(t, "eml/release-notice.eml", "eml")
		for _, want := range []string{
			"# Release 2.4 is out", "- From: Release Desk", "ships the caption parser", "— the release desk",
			"changelog.pdf (application/pdf, ",
		} {
			if !strings.Contains(markdown, want) {
				t.Errorf("eml lacks %q: %q", want, markdown)
			}
		}
		if strings.Contains(markdown, "JVBERi0") {
			t.Errorf("an attachment's bytes were inlined: %q", markdown)
		}
	})
}

// TestFormatParsersRefuseByName: an entity bomb is refused by name and a
// broken feed ends in a named failure — never an empty document.
func TestFormatParsersRefuseByName(t *testing.T) {
	converter := pinnedPythonConverter(t)
	dir := t.TempDir()
	bomb := `<?xml version="1.0"?><!DOCTYPE article [<!ENTITY a "lol"><!ENTITY b "&a;&a;&a;&a;&a;&a;&a;&a;">` +
		`<!ENTITY c "&b;&b;&b;&b;&b;&b;&b;&b;">]><article dtd-version="1.2"><body><p>&c;</p></body></article>`
	for _, tc := range []struct{ kind, body, want string }{
		{"jats", bomb, "entity bomb"},
		{"feed", strings.Replace(bomb, "article", "rss", 2), "entity bomb"},
		{"feed", "<?xml version=\"1.0\"?><rss version=\"2.0\"><chan", "broken or empty feed"},
	} {
		document := filepath.Join(dir, "input."+tc.kind)
		if err := os.WriteFile(document, []byte(tc.body), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := converter.Convert(context.Background(), Request{Path: document, Kind: tc.kind})
		if !errors.Is(err, ErrConverterFailed) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: want a failure naming %q, got markdown %q, err %v", tc.kind, tc.want, got.Markdown, err)
		}
	}
}
