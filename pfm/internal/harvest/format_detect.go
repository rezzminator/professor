package harvest

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// formatClass is what the harvester does with a body, decided from its
// magic bytes — the extension is a hint for text only.
type formatClass int

const (
	// formatUnknown: text, or no signature this file knows; the existing
	// classification (content type, extension, IsPlainText) decides.
	formatUnknown formatClass = iota
	// formatDocument: a document kind the converter reads (pdf, docx …).
	formatDocument
	// formatText: a text document routed under its own kind — the formats
	// converter.py's dispatch table (_CONVERTERS) owns, and kindCode.
	formatText
	// formatCompressed: one compressed document (format_compress.go).
	formatCompressed
	// formatFileOnly: audio, video, images, fonts, executables, archives —
	// a file to download, never parsed.
	formatFileOnly
	// formatDropped: a document format the harvester does not support.
	formatDropped
	// formatRefused: a body that cannot be read at all — a decompression
	// bomb, a corrupt or undecodable compressed stream; reason says why.
	formatRefused
)

// kindCode is a config or source file returned as a fenced block.
const kindCode = "code"

// The web archives converter.py reads as documents: the page inside is HTML,
// the archive itself is not, so they route as their own kind by bytes alone.
const (
	kindMHTML      = "mhtml"
	kindWebArchive = "webarchive"
)

type formatFinding struct {
	class formatClass
	kind  string // the routing kind, for formatDocument/formatText/formatCompressed
	label string // the detected type, named in every failure
	// reason is a formatRefused body's sentence; tooLarge marks the cap.
	reason   string
	tooLarge bool
}

func finding(class formatClass, label string) formatFinding {
	return formatFinding{class: class, label: label}
}

// document is a formatDocument finding routed to converter.py under kind,
// a key of its dispatch table (_CONVERTERS).
func document(kind, label string) formatFinding {
	return formatFinding{class: formatDocument, kind: kind, label: label}
}

type magicSignature struct {
	offset int
	magic  string
	found  formatFinding
}

// magicSignatures are checked in order; the first match wins. Labels carry
// the MIME type so a caller can search for either.
var magicSignatures = []magicSignature{
	{0, "%PDF-", formatFinding{class: formatDocument, kind: kindPDF, label: "PDF (application/pdf)"}},
	{0, "\x1f\x8b", formatFinding{class: formatCompressed, kind: kindGzip, label: "gzip"}},
	{0, "BZh", formatFinding{class: formatCompressed, kind: kindBzip2, label: "bzip2"}},
	{0, "\x28\xb5\x2f\xfd", formatFinding{class: formatCompressed, kind: kindZstd, label: "zstd"}},
	{0, "\xfd7zXZ\x00", formatFinding{class: formatCompressed, kind: kindXZ, label: "xz"}},
	{0, "{\\rtf", document("rtf", "RTF document (application/rtf)")},
	{0, "ITSF", finding(formatDropped, "Microsoft CHM help file (application/vnd.ms-htmlhelp)")},
	{0, "\xc5\xd0\xd3\xc6", finding(formatDropped, "EPS image (application/postscript)")},
	{0, "AT&TFORM", finding(formatDropped, "DjVu document (image/vnd.djvu)")},
	{60, "BOOKMOBI", finding(formatDropped, "Kindle MOBI/AZW3 book (application/x-mobipocket-ebook)")},
	{0, "7z\xbc\xaf\x27\x1c", finding(formatFileOnly, "7z archive (application/x-7z-compressed)")},
	{0, "Rar!\x1a\x07", finding(formatFileOnly, "rar archive (application/vnd.rar)")},
	{257, "ustar", finding(formatFileOnly, "tar archive (application/x-tar)")},
	{0, "MSCF", finding(formatFileOnly, "cab archive (application/vnd.ms-cab-compressed)")},
	{0, "!<arch>\n", finding(formatFileOnly, "ar archive (application/x-archive)")},
	{0, "xar!", finding(formatFileOnly, "xar archive (application/x-xar)")},
	{0x8001, "CD001", finding(formatFileOnly, "ISO disk image (application/x-iso9660-image)")},
	{0, "MZ", finding(formatFileOnly, "Windows executable (application/vnd.microsoft.portable-executable)")},
	{0, "\x7fELF", finding(formatFileOnly, "ELF executable (application/x-executable)")},
	{0, "\xfe\xed\xfa\xce", finding(formatFileOnly, "Mach-O executable (application/x-mach-binary)")},
	{0, "\xfe\xed\xfa\xcf", finding(formatFileOnly, "Mach-O executable (application/x-mach-binary)")},
	{0, "\xce\xfa\xed\xfe", finding(formatFileOnly, "Mach-O executable (application/x-mach-binary)")},
	{0, "\xcf\xfa\xed\xfe", finding(formatFileOnly, "Mach-O executable (application/x-mach-binary)")},
	{
		0,
		"\xca\xfe\xba\xbe",
		finding(formatFileOnly, "Mach-O universal executable or Java class (application/x-mach-binary)"),
	},
	{0, "\x00asm", finding(formatFileOnly, "WebAssembly executable (application/wasm)")},
	{0, "wOFF", finding(formatFileOnly, "WOFF font (font/woff)")},
	{0, "wOF2", finding(formatFileOnly, "WOFF2 font (font/woff2)")},
	{0, "OTTO", finding(formatFileOnly, "OpenType font (font/otf)")},
	{0, "ttcf", finding(formatFileOnly, "TrueType font collection (font/collection)")},
	{0, "\x00\x01\x00\x00\x00", finding(formatFileOnly, "TrueType font (font/ttf)")},
	{0, "SQLite format 3\x00", finding(formatFileOnly, "SQLite database (application/vnd.sqlite3)")},
	{0, "\xff\xd8\xff", finding(formatFileOnly, "JPEG image (image/jpeg)")},
	{0, "\x89PNG\r\n\x1a\n", finding(formatFileOnly, "PNG image (image/png)")},
	{0, "GIF87a", finding(formatFileOnly, "GIF image (image/gif)")},
	{0, "GIF89a", finding(formatFileOnly, "GIF image (image/gif)")},
	{0, "II*\x00", finding(formatFileOnly, "TIFF image (image/tiff)")},
	{0, "MM\x00*", finding(formatFileOnly, "TIFF image (image/tiff)")},
	{0, "8BPS", finding(formatFileOnly, "Photoshop image (image/vnd.adobe.photoshop)")},
	{0, "\x00\x00\x01\x00", finding(formatFileOnly, "icon image (image/x-icon)")},
	{0, "ID3", finding(formatFileOnly, "MP3 audio (audio/mpeg)")},
	{0, "fLaC", finding(formatFileOnly, "FLAC audio (audio/flac)")},
	{0, "OggS", finding(formatFileOnly, "Ogg audio/video (application/ogg)")},
	{0, "MThd", finding(formatFileOnly, "MIDI audio (audio/midi)")},
	{0, "#!AMR", finding(formatFileOnly, "AMR audio (audio/amr)")},
	{0, "\x1a\x45\xdf\xa3", finding(formatFileOnly, "WebM/Matroska video (video/webm)")},
	{0, "FLV\x01", finding(formatFileOnly, "Flash video (video/x-flv)")},
	{0, "\x00\x00\x01\xba", finding(formatFileOnly, "MPEG video (video/mpeg)")},
	{0, "\x00\x00\x01\xb3", finding(formatFileOnly, "MPEG video (video/mpeg)")},
}

// riffForms and ftypBrands name the container formats whose type sits
// after a generic header.
var riffForms = map[string]formatFinding{
	"WAVE":    finding(formatFileOnly, "WAV audio (audio/wave)"),
	"AVI\x20": finding(formatFileOnly, "AVI video (video/x-msvideo)"),
	"WEBP":    finding(formatFileOnly, "WebP image (image/webp)"),
}

var ftypBrands = map[string]formatFinding{
	"heic": finding(formatFileOnly, "HEIC image (image/heic)"),
	"heix": finding(formatFileOnly, "HEIC image (image/heic)"),
	"hevc": finding(formatFileOnly, "HEIC image sequence (image/heic-sequence)"),
	"mif1": finding(formatFileOnly, "HEIF image (image/heif)"),
	"msf1": finding(formatFileOnly, "HEIF image sequence (image/heif-sequence)"),
	"avif": finding(formatFileOnly, "AVIF image (image/avif)"),
	"M4A ": finding(formatFileOnly, "M4A audio (audio/mp4)"),
	"M4B ": finding(formatFileOnly, "M4B audiobook (audio/mp4)"),
	"qt  ": finding(formatFileOnly, "QuickTime video (video/quicktime)"),
}

// detectFormat names a body's type from its bytes. name (a path or URL) is
// consulted only for text: a code/config language and a few text formats.
func detectFormat(name string, body []byte) formatFinding {
	if len(body) == 0 {
		return formatFinding{}
	}
	head := body[:min(len(body), 512)]
	// A signature of three bytes or fewer ("MZ", "BZh", "ID3") also starts
	// ordinary words; it counts only on a head that is not clean text.
	textual := bytes.IndexByte(head, 0) < 0 && utf8.Valid(trimPartialRune(head))
	for _, sig := range magicSignatures {
		if len(sig.magic) <= 3 && textual {
			continue
		}
		if len(body) >= sig.offset+len(sig.magic) && string(body[sig.offset:sig.offset+len(sig.magic)]) == sig.magic {
			return sig.found
		}
	}
	switch {
	case len(head) >= 12 && string(head[:4]) == "RIFF":
		if found, ok := riffForms[string(head[8:12])]; ok {
			return found
		}
		return finding(formatFileOnly, "RIFF media (application/octet-stream)")
	case len(head) >= 12 && string(head[:4]) == "FORM" && (string(head[8:12]) == "AIFF" || string(head[8:12]) == "AIFC"):
		return finding(formatFileOnly, "AIFF audio (audio/aiff)")
	case len(head) >= 12 && string(head[4:8]) == "ftyp":
		if found, ok := ftypBrands[string(head[8:12])]; ok {
			return found
		}
		return finding(formatFileOnly, "MP4 video (video/mp4)")
	case len(head) >= 2 && head[0] == 0xff && (head[1]&0xe0) == 0xe0 && !utf8.Valid(head):
		return finding(formatFileOnly, "MPEG audio (audio/mpeg)")
	case bytes.HasPrefix(head, []byte("PK\x03\x04")), bytes.HasPrefix(head, []byte("PK\x05\x06")):
		return zipFormat(body)
	case bytes.HasPrefix(head, []byte("\xd0\xcf\x11\xe0")):
		return oleFormat(body)
	case bytes.HasPrefix(head, []byte("bplist")):
		if bytes.Contains(body[:min(len(body), 4096)], []byte("WebMainResource")) {
			return formatFinding{
				class: formatDocument, kind: kindWebArchive, label: "Safari web archive (application/x-webarchive)",
			}
		}
		return finding(formatFileOnly, "binary property list (application/x-bplist)")
	case bytes.HasPrefix(head, []byte("%!PS")):
		firstLine, _, _ := bytes.Cut(head, []byte("\n"))
		if bytes.Contains(firstLine, []byte("EPSF")) {
			return finding(formatDropped, "EPS image (application/postscript)")
		}
		return finding(formatDropped, "PostScript document (application/postscript)")
	}
	sample := body[:min(len(body), 8192)]
	if bytes.IndexByte(sample, 0) >= 0 || !utf8.Valid(trimPartialRune(sample)) {
		detected, _, _ := strings.Cut(http.DetectContentType(head), ";")
		if strings.HasPrefix(detected, "text/") {
			return formatFinding{} // UTF-16 or another text encoding
		}
		return finding(formatFileOnly, "binary data ("+detected+")")
	}
	return textFormat(name, string(sample))
}

// trimPartialRune drops a multi-byte rune cut by the sample boundary.
func trimPartialRune(sample []byte) []byte {
	for i := 0; i < 3 && len(sample) > 0 && !utf8.Valid(sample); i++ {
		sample = sample[:len(sample)-1]
	}
	return sample
}

var ooxmlMainTypes = []struct {
	marker string
	found  formatFinding
}{
	{
		"wordprocessingml.document.main",
		formatFinding{class: formatDocument, kind: kindDOCX, label: "Word document (DOCX)"},
	},
	{"spreadsheetml.sheet.main", formatFinding{class: formatDocument, kind: kindXLSX, label: "Excel workbook (XLSX)"}},
	{
		"presentationml.presentation.main",
		formatFinding{class: formatDocument, kind: kindPPTX, label: "PowerPoint presentation (PPTX)"},
	},
	{"ms-word.document.macroEnabled.main", document("docm", "macro-enabled Word document (DOCM)")},
	{"wordprocessingml.template.main", document("dotx", "Word template (DOTX)")},
	{"ms-word.template.macroEnabledTemplate.main", document("dotm", "macro-enabled Word template (DOTM)")},
	{"ms-excel.sheet.macroEnabled.main", document("xlsm", "macro-enabled Excel workbook (XLSM)")},
	{"spreadsheetml.template.main", document("xltx", "Excel template (XLTX)")},
	{"ms-excel.template.macroEnabled.main", document("xltm", "macro-enabled Excel template (XLTM)")},
	{"ms-excel.sheet.binary.macroEnabled.main", finding(formatDropped, "binary Excel workbook (XLSB)")},
	{
		"ms-powerpoint.presentation.macroEnabled.main",
		document("pptm", "macro-enabled PowerPoint presentation (PPTM)"),
	},
	{"presentationml.template.main", document("potx", "PowerPoint template (POTX)")},
	{"ms-powerpoint.template.macroEnabled.main", document("potm", "macro-enabled PowerPoint template (POTM)")},
	{"presentationml.slideshow.main", document("ppsx", "PowerPoint slideshow (PPSX)")},
	{"ms-powerpoint.slideshow.macroEnabled.main", document("ppsm", "macro-enabled PowerPoint slideshow (PPSM)")},
}

// odfMimetypes route an OpenDocument file, its template (the "-template"
// mimetype) included, to the kind whose parser reads it.
var odfMimetypes = map[string]formatFinding{
	"application/vnd.oasis.opendocument.text":         document("odt", "OpenDocument text (ODT)"),
	"application/vnd.oasis.opendocument.spreadsheet":  document("ods", "OpenDocument spreadsheet (ODS)"),
	"application/vnd.oasis.opendocument.presentation": document("odp", "OpenDocument presentation (ODP)"),
}

// zipFormat tells the ZIP-based formats apart by their inner member: the
// OOXML [Content_Types].xml main part, the ODF/EPUB mimetype member, the
// iWork Index/ tree; any other zip is an archive.
func zipFormat(body []byte) formatFinding {
	archive := finding(formatFileOnly, "zip archive (application/zip)")
	reader, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		// A truncated zip has no central directory; the first local header
		// still names an EPUB's leading mimetype member.
		if LooksLikeEpub(body) {
			return formatFinding{class: formatDocument, kind: kindEPUB, label: "EPUB book (application/epub+zip)"}
		}
		return archive
	}
	for _, file := range reader.File {
		switch {
		case file.Name == "mimetype":
			mimetype := strings.TrimSpace(zipMember(file, 256))
			if mimetype == epubContentMarker {
				return formatFinding{class: formatDocument, kind: kindEPUB, label: "EPUB book (application/epub+zip)"}
			}
			if found, ok := odfMimetypes[strings.TrimSuffix(mimetype, "-template")]; ok {
				return found
			}
			if strings.HasPrefix(mimetype, "application/vnd.oasis.opendocument.") {
				return finding(formatDropped, "OpenDocument file ("+mimetype+")")
			}
		case file.Name == "[Content_Types].xml":
			types := zipMember(file, 1<<20)
			for _, main := range ooxmlMainTypes {
				if strings.Contains(types, main.marker) {
					return main.found
				}
			}
			return finding(formatDropped, "Open Packaging file of an unknown type")
		case file.Name == "Index/Document.iwa", file.Name == "index.apxl", file.Name == "index.xml.gz",
			strings.HasPrefix(file.Name, "Index/") && strings.HasSuffix(file.Name, ".iwa"):
			return finding(formatDropped, "Apple iWork document (Pages/Numbers/Keynote)")
		}
	}
	return archive
}

func zipMember(file *zip.File, limit int64) string {
	reader, err := file.Open()
	if err != nil {
		return ""
	}
	defer func() { _ = reader.Close() }()
	data, _ := io.ReadAll(io.LimitReader(reader, limit))
	return string(data)
}

// oleStreams tell the compound-file formats apart by a directory entry's
// UTF-16LE stream name, checked in order.
var oleStreams = []struct {
	stream string
	found  formatFinding
}{
	{"EncryptedPackage", document("encrypted", "password-protected Office document (application/x-ole-storage)")},
	{"__substg1.0_", finding(formatDropped, "Outlook message (application/vnd.ms-outlook)")},
	{"PowerPoint Document", finding(formatDropped, "PowerPoint 97-2003 presentation (application/vnd.ms-powerpoint)")},
	{"WordDocument", document("doc", "Word document (application/msword)")},
	{"Workbook", document("xls", "Excel 97-2003 workbook (application/vnd.ms-excel)")},
	{"Book\x00", document("xls", "Excel 5.0/95 workbook (application/vnd.ms-excel)")},
}

func oleFormat(body []byte) formatFinding {
	for _, entry := range oleStreams {
		if bytes.Contains(body, utf16LE(entry.stream)) {
			return entry.found
		}
	}
	return finding(formatFileOnly, "unidentified compound file (application/x-ole-storage)")
}

func utf16LE(value string) []byte {
	out := make([]byte, 0, 2*len(value))
	for _, unit := range utf16.Encode([]rune(value)) {
		out = append(out, byte(unit), byte(unit>>8))
	}
	return out
}

// codeLanguages are the config and code extensions returned fenced. Server
// page extensions (.php, .asp, .jsp) are left out: a URL ending in one is a
// page. A body that looks like HTML is never fenced.
var codeLanguages = func() map[string]string {
	byLanguage := map[string][]string{
		"yaml":       {".yaml", ".yml"},
		"toml":       {".toml"},
		"ini":        {".ini", ".cfg", ".conf"},
		"properties": {".properties"},
		languageXML:  {".xml", ".xsd", ".xsl", ".xslt"},
		"python":     {".py"},
		"go":         {".go"},
		"javascript": {
			".js",
			".mjs",
			".cjs",
		},
		"typescript":     {".ts"},
		"tsx":            {".tsx"},
		"jsx":            {".jsx"},
		"java":           {".java"},
		"kotlin":         {".kt"},
		"scala":          {".scala"},
		"c":              {".c", ".h"},
		"cpp":            {".cc", ".cpp", ".hpp"},
		"csharp":         {".cs"},
		"rust":           {".rs"},
		"ruby":           {".rb"},
		"swift":          {".swift"},
		"bash":           {".sh", ".bash"},
		"zsh":            {".zsh"},
		"powershell":     {".ps1"},
		"sql":            {".sql"},
		"lua":            {".lua"},
		"perl":           {".pl"},
		"r":              {".r"},
		"julia":          {".jl"},
		"dart":           {".dart"},
		"elixir":         {".ex", ".exs"},
		"erlang":         {".erl"},
		"haskell":        {".hs"},
		"clojure":        {".clj"},
		"css":            {".css"},
		"scss":           {".scss"},
		"groovy":         {".gradle", ".groovy"},
		"protobuf":       {".proto"},
		"graphql":        {".graphql"},
		"hcl":            {".tf"},
		languageMakefile: {".mk"},
		"cmake":          {".cmake"},
	}
	byExtension := map[string]string{}
	for language, extensions := range byLanguage {
		for _, extension := range extensions {
			byExtension[extension] = language
		}
	}
	return byExtension
}()

const (
	languageXML      = "xml"
	languageMakefile = "makefile"
)

var codeFileNames = map[string]string{
	"dockerfile":     "dockerfile",
	"makefile":       languageMakefile,
	"cmakelists.txt": "cmake",
}

// codeLanguage is the fence language of a config/code source, or "".
func codeLanguage(name string) string {
	base := strings.ToLower(filepath.Base(strings.TrimRight(stripLocationQuery(name), "/")))
	if language, ok := codeFileNames[base]; ok {
		return language
	}
	return codeLanguages[filepath.Ext(base)]
}

var (
	xmlPrologRe = regexp.MustCompile(`(?s)^(?:\s|<\?xml.*?\?>|<!--.*?-->|<!DOCTYPE[^>\[]*(?:\[.*?\])?\s*>)*`)
	xmlRootRe   = regexp.MustCompile(`^<([A-Za-z_][\w.:-]*)`)
	srtCueRe    = regexp.MustCompile(`^\d+\r?\n-?\d{1,2}:\d{2}:\d{2}[,.]\d{3} --> `)
	bibtexRe    = regexp.MustCompile(
		`(?i)^@(article|book|booklet|inbook|incollection|inproceedings|conference|manual|mastersthesis|misc|online|phdthesis|proceedings|techreport|unpublished|string|preamble|comment)\s*[{(]`,
	)
	mailHeaderRe = regexp.MustCompile(`(?im)^(from|to|subject|date|message-id|received|mime-version|return-path):`)
	// mailFirstLineRe: a mail or MHTML file starts with a header line.
	mailFirstLineRe = regexp.MustCompile(`^[A-Za-z-]+:`)
)

// textFormat names a text body: a document format converter.py's dispatch
// owns, a fenced config/code file, a text format parsed later, or unknown.
func textFormat(name, sample string) formatFinding {
	text := strings.TrimPrefix(sample, "\ufeff")
	trimmed := strings.TrimLeft(text, " \t\r\n")
	ext := strings.ToLower(filepath.Ext(strings.TrimRight(stripLocationQuery(name), "/")))
	routed := func(kind, label string) formatFinding {
		return formatFinding{class: formatText, kind: kind, label: label}
	}
	switch {
	case strings.HasPrefix(trimmed, "WEBVTT"):
		return routed("vtt", "WebVTT captions (text/vtt)")
	case srtCueRe.MatchString(trimmed):
		return routed("srt", "SRT captions (application/x-subrip)")
	case strings.HasPrefix(trimmed, "TY  - "):
		return routed("ris", "RIS citations (application/x-research-info-systems)")
	case bibtexRe.MatchString(trimmed) || (ext == ".bib" && strings.HasPrefix(trimmed, "@")):
		return routed("bibtex", "BibTeX citations (application/x-bibtex)")
	case ext == ".ipynb" || (strings.HasPrefix(trimmed, "{") && strings.Contains(text, `"nbformat"`) && strings.Contains(text, `"cells"`)):
		return routed("ipynb", "Jupyter notebook (application/x-ipynb+json)")
	}
	if headers, _, found := strings.Cut(
		strings.ReplaceAll(trimmed, "\r\n", "\n"),
		"\n\n",
	); found &&
		mailHeaderRe.MatchString(headers) &&
		mailFirstLineRe.MatchString(headers) {
		lower := strings.ToLower(headers)
		if strings.Contains(lower, "multipart/related") || ext == ".mht" || ext == ".mhtml" {
			return formatFinding{class: formatDocument, kind: kindMHTML, label: "MHTML web archive (multipart/related)"}
		}
		if len(mailHeaderRe.FindAllString(headers, -1)) >= 3 || ext == ".eml" {
			return routed("eml", "email message (message/rfc822)")
		}
	}
	if strings.HasPrefix(trimmed, "<") {
		rest := xmlPrologRe.FindString(trimmed)
		root := ""
		if match := xmlRootRe.FindStringSubmatch(trimmed[len(rest):]); match != nil {
			root = strings.ToLower(match[1])
		}
		doctype := strings.ToUpper(rest)
		switch {
		case root == "rss" || root == "rdf:rdf" || (root == "feed" && strings.Contains(trimmed, "http://www.w3.org/2005/Atom")):
			return routed("feed", "RSS/Atom/RDF feed (application/rss+xml)")
		case root == "fictionbook":
			return routed("fb2", "FictionBook (application/x-fictionbook+xml)")
		// Europe PMC's fullTextXML carries no DOCTYPE and no dtd-version; its
		// JATS front matter (<journal-meta>, <article-meta>) names it, which
		// an HTML5 <article> never holds.
		case root == "article" && (strings.Contains(doctype, "JATS") || strings.Contains(doctype, "NLM") ||
			strings.Contains(trimmed[:min(len(trimmed), 2048)], "dtd-version=") || ext == ".nxml" ||
			strings.Contains(trimmed, "<article-meta") || strings.Contains(trimmed, "<journal-meta")):
			return routed("jats", "JATS article (application/jats+xml)")
		case root == kindHTML || root == "svg" || looksHTML(trimmed[len(rest):min(len(trimmed), len(rest)+512)]):
			return formatFinding{}
		case strings.HasPrefix(trimmed, "<?xml") || codeLanguage(name) == languageXML:
			return routed(kindCode, "XML (application/xml)")
		}
		return formatFinding{}
	}
	if language := codeLanguage(name); language != "" && !looksHTML(sample) {
		return routed(kindCode, language+" source")
	}
	return formatFinding{}
}

// fenceCode returns a text body as a fenced block in its language, the
// fence longer than any backtick run inside it.
func fenceCode(language, body string) string {
	longest, run := 0, 0
	for _, r := range body {
		if r == '`' {
			run++
			longest = max(longest, run)
			continue
		}
		run = 0
	}
	fence := strings.Repeat("`", max(3, longest+1))
	return fence + language + "\n" + strings.TrimRight(strings.TrimPrefix(body, "\ufeff"), "\r\n") + "\n" + fence + "\n"
}
