package harvest

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

// formatZip builds a zip whose members are the given name → body pairs, in
// order — the container shape of OOXML, ODF, EPUB and iWork files.
func formatZip(t *testing.T, members ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for i := 0; i+1 < len(members); i += 2 {
		entry, err := writer.Create(members[i])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(members[i+1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func formatContentTypes(main string) string {
	return `<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
		`<Override PartName="/main.xml" ContentType="application/` + main + `+xml"/></Types>`
}

// formatOLE is a compound-file header followed by a directory entry naming
// the stream that tells a Word, Excel, PowerPoint and Outlook file apart.
func formatOLE(stream string) []byte {
	body := []byte("\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1")
	body = append(body, make([]byte, 504)...)
	for _, unit := range utf16.Encode([]rune(stream)) {
		body = append(body, byte(unit), byte(unit>>8))
	}
	return append(body, 0, 0)
}

func formatGzip(t *testing.T, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	if _, err := writer.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func formatLocal(t *testing.T, converter Converter, files map[string][]byte) (*Harvester, string) {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(root, name), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return mustNew(t, Options{CacheDir: t.TempDir(), LocalRoots: []string{root}, Converter: converter}), root
}

// TestFormatMagicRoutesDocumentsNotExtensions: a body is routed by its
// bytes. Watched FAILING before format_detect.go: an .xlsx named .xls, a
// .pptx served as octet-stream and an EPUB named .zip all came back "zip".
func TestFormatMagicRoutesDocumentsNotExtensions(t *testing.T) {
	xlsx := formatZip(
		t,
		"[Content_Types].xml",
		formatContentTypes("vnd.openxmlformats-officedocument.spreadsheetml.sheet.main"),
		"xl/workbook.xml",
		"<workbook/>",
	)
	pptx := formatZip(
		t,
		"[Content_Types].xml",
		formatContentTypes("vnd.openxmlformats-officedocument.presentationml.presentation.main"),
		"ppt/presentation.xml",
		"<p/>",
	)
	epub := formatZip(t, "mimetype", "application/epub+zip", "META-INF/container.xml", "<container/>")
	for _, tc := range []struct {
		name, contentType string
		body              []byte
		want              string
	}{
		{"report.xls", "", xlsx, kindXLSX},
		{"https://203.0.113.10/deck.bin", "application/octet-stream", pptx, kindPPTX},
		{"book.zip", "", epub, kindEPUB},
		// An archive or compression extension on bytes that carry no
		// archive signature is not an archive: the bytes decide (live FM1:
		// an empty .bz2 reached the converter as "tar").
		{"notes.txt.bz2", "", nil, kindHTML},
		{"notes.gz", "", []byte("plain notes, not gzip\n"), kindTXT},
		{"bundle.zip", "", []byte("not a zip\n"), kindTXT},
		// Europe PMC's fullTextXML serves JATS as application/xml with no
		// DOCTYPE and no dtd-version (captured live, FM2c): the article's
		// front matter names it (live FM2: stored as html).
		{
			"https://www.ebi.ac.uk/europepmc/webservices/rest/PMC7092803/fullTextXML",
			"application/xml",
			[]byte(`<?xml version="1.0" encoding="UTF-8"?><article xml:lang="en" article-type="research-article">` +
				`<front><journal-meta><journal-id journal-id-type="pmc-domain-id">3892</journal-id></journal-meta>` +
				`<article-meta><article-id pub-id-type="pmcid">PMC7092803</article-id></article-meta></front>` +
				`<body><sec><title>Methods</title><p>Text.</p></sec></body></article>`),
			"jats",
		},
	} {
		if got := classifyFetchedKind(tc.name, tc.contentType, tc.body); got != tc.want {
			t.Errorf("classifyFetchedKind(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestFormatConfigAndCodeAreFenced: config and code files come back as a
// fenced block with their language; plain text passes unfenced. Watched
// FAILING before format_detect.go (plain text, the XML through HTML).
func TestFormatConfigAndCodeAreFenced(t *testing.T) {
	files := map[string][]byte{
		"config.yaml":    []byte("name: fleet\nsize: 3\n"),
		"pyproject.toml": []byte("[project]\nname = \"demo\"\n"),
		"app.ini":        []byte("[server]\nport = 8080\n"),
		"layout.xml":     []byte("<?xml version=\"1.0\"?>\n<config><port>8080</port></config>\n"),
		"main.py":        []byte("def main():\n    print(\"```\")\n"),
		"notes.txt":      []byte("just some notes\n"),
	}
	h, root := formatLocal(t, tagStripConverter(), files)
	for name, want := range map[string]string{
		"config.yaml":    "```yaml\nname: fleet\nsize: 3\n```",
		"pyproject.toml": "```toml\n[project]",
		"app.ini":        "```ini\n[server]",
		"layout.xml":     "```xml\n<?xml version=\"1.0\"?>\n<config><port>8080</port></config>",
		"main.py":        "````python\ndef main():\n    print(\"```\")\n````",
		"notes.txt":      "just some notes",
	} {
		got := h.FetchPublic(context.Background(), filepath.Join(root, name), FetchOptions{Refresh: true})
		if got.Error != "" || !strings.Contains(got.Content, want) {
			t.Errorf("%s: want %q, got Error=%q Content=%q", name, want, got.Error, got.Content)
		}
		if name == "notes.txt" && strings.Contains(got.Content, "```") {
			t.Errorf("plain text was fenced: %q", got.Content)
		}
	}
}

// formatRestCases are bodies the harvester does not convert: the dropped
// formats and the files-only kinds (legacy PowerPoint and Outlook included). Each must
// end in a named failure carrying its detected type.
var formatRestCases = []struct {
	name  string
	body  func(t *testing.T) []byte
	label string
}{
	{"help.chm", func(*testing.T) []byte { return []byte("ITSF\x03\x00\x00\x00\x60\x00\x00\x00") }, "CHM"},
	{"figure.ps", func(*testing.T) []byte { return []byte("%!PS-Adobe-3.0\n%%Pages: 1\nshowpage\n") }, "PostScript"},
	{
		"figure.eps",
		func(*testing.T) []byte { return []byte("%!PS-Adobe-3.0 EPSF-3.0\n%%BoundingBox: 0 0 1 1\n") },
		"EPS",
	},
	{
		"novel.mobi",
		func(*testing.T) []byte { return append(make([]byte, 60), []byte("BOOKMOBI\x00\x00")...) },
		"Kindle",
	},
	{"scan.djvu", func(*testing.T) []byte { return []byte("AT&TFORM\x00\x00\x00\x10DJVUINFO") }, "DjVu"},
	{"mail.msg", func(*testing.T) []byte { return formatOLE("__substg1.0_0037001F") }, "Outlook"},
	{"deck.ppt", func(*testing.T) []byte { return formatOLE("PowerPoint Document") }, "PowerPoint 97-2003"},
	{"memo.pages", func(t *testing.T) []byte { return formatZip(t, "Index/Document.iwa", "\x00") }, "Apple iWork"},
	{"song.mp3", func(*testing.T) []byte { return []byte("ID3\x03\x00\x00\x00\x00\x00\x00\xff\xfb\x90\x00") }, "MP3"},
	{"song.flac", func(*testing.T) []byte { return []byte("fLaC\x00\x00\x00\x22\x10\x00") }, "FLAC"},
	{
		"photo.heic",
		func(*testing.T) []byte { return []byte("\x00\x00\x00\x18ftypheic\x00\x00\x00\x00mif1heic") },
		"HEIC",
	},
	{"clip.mp4", func(*testing.T) []byte { return []byte("\x00\x00\x00\x18ftypisom\x00\x00\x02\x00isomiso2") }, "MP4"},
	{
		"clip.webm",
		func(*testing.T) []byte { return []byte("\x1a\x45\xdf\xa3\x9f\x42\x86\x81\x01\x42\x82\x84webm") },
		"WebM",
	},
	{"type.woff2", func(*testing.T) []byte { return []byte("wOF2\x00\x01\x00\x00\x00\x00") }, "font"},
	{"tool.exe", func(*testing.T) []byte { return []byte("MZ\x90\x00\x03\x00\x00\x00\x04\x00") }, "executable"},
	{"tool", func(*testing.T) []byte { return []byte("\x7fELF\x02\x01\x01\x00\x00\x00") }, "executable"},
	{"bundle.tar", func(*testing.T) []byte {
		body := make([]byte, 1024)
		copy(body, "notes.txt")
		copy(body[257:], "ustar\x0000")
		return body
	}, "tar archive"},
	{"archive.txt.xz", func(*testing.T) []byte { return []byte("\xfd7zXZ\x00\x00\x04\xe6\xd6\xb4\x46") }, "xz"},
}

// TestFormatNamedRestCarriesTheDetectedType: every kind the harvester does
// not convert ends in a named failure with its detected type, for read's urls
// and files alike — never binary characters stored as content.
// Watched FAILING before format_detect.go (a generic MIME type, a text read,
// or no failure at all).
func TestFormatNamedRestCarriesTheDetectedType(t *testing.T) {
	files := map[string][]byte{}
	for _, tc := range formatRestCases {
		files[tc.name] = tc.body(t)
	}
	local, root := formatLocal(t, tagStripConverter(), files)
	for _, tc := range formatRestCases {
		t.Run(tc.name, func(t *testing.T) {
			body := files[tc.name]
			site := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return response(request, http.StatusOK, "application/octet-stream", string(body)), nil
			})
			page := mustNew(t, Options{
				CacheDir:    t.TempDir(),
				Client:      &http.Client{Transport: site},
				Chrome:      &http.Client{Transport: site},
				Converter:   tagStripConverter(),
				BrowserRung: browserOff(),
			})
			got := page.Fetch(context.Background(), "https://203.0.113.10/"+tc.name)
			if got.Content != "" || !strings.Contains(got.Error, tc.label) || !strings.Contains(got.Error, "download") {
				t.Errorf(
					"read (urls): want a named failure naming %q and `download_file`, got Error=%q Content=%q",
					tc.label,
					got.Error,
					got.Content,
				)
			}
			got = local.FetchPublic(context.Background(), filepath.Join(root, tc.name), FetchOptions{Refresh: true})
			if got.Content != "" || !strings.Contains(got.Error, tc.label) || strings.Contains(got.Error, root) {
				t.Errorf(
					"read (files): want a named failure naming %q, got Error=%q Content=%q",
					tc.label,
					got.Error,
					got.Content,
				)
			}
		})
	}
}

// TestFormatTextDocumentsReachTheConverterDispatch: the text formats a later
// task parses (feeds, captions, notebooks, citations, mail) and the web
// archives (MHTML, Safari webarchive) are detected by
// their bytes and handed to the converter under their own kind — the
// dispatch point in converter.py. Watched FAILING before format_detect.go
// (html, txt or json).
func TestFormatTextDocumentsReachTheConverterDispatch(t *testing.T) {
	seen := map[string]string{}
	recorder := legacyConverterFunc(func(_ context.Context, kind, source string, _ []byte) (string, error) {
		seen[filepath.Base(source)] = kind
		return "converted as " + kind, nil
	})
	files := map[string][]byte{
		"feed.xml": []byte(
			"<?xml version=\"1.0\"?>\n<rss version=\"2.0\"><channel><title>t</title></channel></rss>\n",
		),
		"atom": []byte(
			"<?xml version=\"1.0\"?><feed xmlns=\"http://www.w3.org/2005/Atom\"><title>t</title></feed>",
		),
		"paper.nxml": []byte(
			"<?xml version=\"1.0\"?>\n<!DOCTYPE article PUBLIC \"-//NLM//DTD JATS (Z39.96) Journal Archiving and Interchange DTD v1.2 20190208//EN\" \"JATS-archivearticle1.dtd\">\n<article dtd-version=\"1.2\"><front/></article>\n",
		),
		"book.fb2": []byte(
			"<?xml version=\"1.0\" encoding=\"utf-8\"?>\n<FictionBook xmlns=\"http://www.gribuser.ru/xml/fictionbook/2.0\"><body/></FictionBook>\n",
		),
		"nb.ipynb": []byte("{\"cells\": [], \"metadata\": {}, \"nbformat\": 4, \"nbformat_minor\": 5}\n"),
		"talk.vtt": []byte("WEBVTT\n\n00:00:00.000 --> 00:00:01.000\nHello\n"),
		"talk.srt": []byte("1\n00:00:00,000 --> 00:00:01,000\nHello\n"),
		// The bake-off's gaupol-extended.srt opens on a negative timestamp.
		"gaupol-extended.srt": []byte(
			"1\n-00:00:06,843 --> -00:00:02,850  X1:010 X2:710 Y1:400 Y2:460\nI always wanted to leave my country\n\n" +
				"2\n-00:00:01,471 --> 00:00:00,946  X1:010 X2:710 Y1:400 Y2:460\nI always wanted to come to France.\n",
		),
		"refs.ris": []byte("TY  - JOUR\nTI  - A title\nER  - \n"),
		"refs.bib": []byte("@article{key2020,\n  title = {A title},\n}\n"),
		"page.mhtml": []byte(
			"From: <Saved by Blink>\r\nMIME-Version: 1.0\r\nContent-Type: multipart/related; boundary=\"b\"\r\n\r\n--b\r\n",
		),
		"page.webarchive": append([]byte("bplist00\xd1\x01\x02_\x10\x0fWebMainResource"), make([]byte, 40)...),
		"note.eml": []byte(
			"From: sender@example.com\nTo: reader@example.com\nSubject: Hello\nDate: Mon, 1 Jan 2024 00:00:00 +0000\n\nBody\n",
		),
	}
	want := map[string]string{
		"feed.xml": "feed", "atom": "feed", "paper.nxml": "jats", "book.fb2": "fb2", "nb.ipynb": "ipynb",
		"talk.vtt": "vtt", "talk.srt": "srt", "gaupol-extended.srt": "srt", "refs.ris": "ris",
		"refs.bib": "bibtex", "note.eml": "eml", "page.mhtml": "mhtml", "page.webarchive": "webarchive",
	}
	h, root := formatLocal(t, recorder, files)
	for name, kind := range want {
		got := h.FetchPublic(context.Background(), filepath.Join(root, name), FetchOptions{Refresh: true})
		if got.Error != "" || seen[name] != kind {
			t.Errorf("%s: want converter kind %q, got %q (Error=%q)", name, kind, seen[name], got.Error)
		}
	}
}

// formatOfficeCases are the Office bodies converter.py's dispatch reads, each
// with the kind it arrives under: RTF, the OLE formats (Word, Excel 97 and
// 5.0/95, an encrypted OOXML package), OpenDocument with its templates, and
// the OOXML macro, template and slideshow variants.
func formatOfficeCases(t *testing.T) map[string]struct {
	body []byte
	kind string
} {
	t.Helper()
	odf := func(mimetype string) []byte {
		return formatZip(t, "mimetype", "application/vnd.oasis.opendocument."+mimetype, "content.xml", "<office/>")
	}
	ooxml := func(main string) []byte {
		return formatZip(t, "[Content_Types].xml", formatContentTypes(main), "main.xml", "<main/>")
	}
	type office = struct {
		body []byte
		kind string
	}
	return map[string]office{
		"letter.rtf":  {[]byte("{\\rtf1\\ansi hello}"), "rtf"},
		"memo.doc":    {formatOLE("WordDocument"), "doc"},
		"sheet.xls":   {formatOLE("Workbook"), "xls"},
		"old.xls":     {formatOLE("Book\x00"), "xls"},
		"locked.docx": {formatOLE("EncryptedPackage"), "encrypted"},
		"minutes.odt": {odf("text"), "odt"},
		"form.ott":    {odf("text-template"), "odt"},
		"budget.ods":  {odf("spreadsheet"), "ods"},
		"budget.ots":  {odf("spreadsheet-template"), "ods"},
		"deck.odp":    {odf("presentation"), "odp"},
		"deck.otp":    {odf("presentation-template"), "odp"},
		"macro.docm":  {ooxml("vnd.ms-word.document.macroEnabled.main"), "docm"},
		"form.dotx":   {ooxml("vnd.openxmlformats-officedocument.wordprocessingml.template.main"), "dotx"},
		"form.dotm":   {ooxml("vnd.ms-word.template.macroEnabledTemplate.main"), "dotm"},
		"macro.xlsm":  {ooxml("vnd.ms-excel.sheet.macroEnabled.main"), "xlsm"},
		"form.xltx":   {ooxml("vnd.openxmlformats-officedocument.spreadsheetml.template.main"), "xltx"},
		"form.xltm":   {ooxml("vnd.ms-excel.template.macroEnabled.main"), "xltm"},
		"macro.pptm":  {ooxml("vnd.ms-powerpoint.presentation.macroEnabled.main"), "pptm"},
		"form.potx":   {ooxml("vnd.openxmlformats-officedocument.presentationml.template.main"), "potx"},
		"form.potm":   {ooxml("vnd.ms-powerpoint.template.macroEnabled.main"), "potm"},
		"show.ppsx":   {ooxml("vnd.openxmlformats-officedocument.presentationml.slideshow.main"), "ppsx"},
		"show.ppsm":   {ooxml("vnd.ms-powerpoint.slideshow.macroEnabled.main"), "ppsm"},
	}
}

// TestFormatOfficeDocumentsReachTheConverterDispatch: every Office kind
// converter.py parses reaches it under its own kind, from read's urls and
// files alike, and the readable-formats list names them.
// Watched FAILING before FM3b: each ended in "detected, but the harvester
// does not parse it yet".
func TestFormatOfficeDocumentsReachTheConverterDispatch(t *testing.T) {
	seen := map[string]string{}
	recorder := legacyConverterFunc(func(_ context.Context, kind, source string, _ []byte) (string, error) {
		seen[filepath.Base(source)] = kind
		return "converted as " + kind, nil
	})
	cases := formatOfficeCases(t)
	files := map[string][]byte{}
	for name, tc := range cases {
		files[name] = tc.body
	}
	local, root := formatLocal(t, recorder, files)
	for name, tc := range cases {
		got := local.FetchPublic(context.Background(), filepath.Join(root, name), FetchOptions{Refresh: true})
		if got.Error != "" || seen[name] != tc.kind || !strings.Contains(got.Content, "converted as "+tc.kind) {
			t.Errorf("read (files) %s: want converter kind %q, got %q (Error=%q)",
				name, tc.kind, seen[name], got.Error)
		}
		body := tc.body
		site := roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return response(request, http.StatusOK, "application/octet-stream", string(body)), nil
		})
		page := mustNew(t, Options{
			CacheDir:    t.TempDir(),
			Client:      &http.Client{Transport: site},
			Chrome:      &http.Client{Transport: site},
			Converter:   recorder,
			BrowserRung: browserOff(),
		})
		delete(seen, name)
		got = page.Fetch(context.Background(), "https://203.0.113.10/"+name)
		if got.Error != "" || seen[name] != tc.kind || !strings.Contains(got.Content, "converted as "+tc.kind) {
			t.Errorf("read (urls) %s: want converter kind %q, got %q (Error=%q)", name, tc.kind, seen[name], got.Error)
		}
	}
	for _, format := range []string{"DOC,", "XLS,", "RTF", "ODT", "ODS", "ODP", "macro and template variants"} {
		if !strings.Contains(unsupportedFormatText("x", ""), format) {
			t.Errorf("the readable-formats list omits %q: %s", format, unsupportedFormatText("x", ""))
		}
	}
}
