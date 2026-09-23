package harvestpy

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// realPythonConverter runs the EMBEDDED converter.py under the host's own
// python3. Every case below reaches a branch of convert() that touches no
// third-party library, so it needs no provisioned environment; a host without
// python3 is a NAMED skip, never a quiet pass.
func realPythonConverter(t *testing.T) *Converter {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("named gap: python3 is unavailable on this host; the converter failure contract did not run")
	}
	script := filepath.Join(t.TempDir(), "converter.py")
	if err := os.WriteFile(script, ConverterSource(), 0o600); err != nil {
		t.Fatal(err)
	}
	converter := NewConverter(Runtime{Python: python, Script: script})
	t.Cleanup(func() { _ = converter.Close() })
	return converter
}

// TestConverterExceptionIsAFailureNotAnEmptyConversion is L2-F3: converter.py
// caught every conversion exception, blanked the markdown and answered
// ok:true, so the Go side reported a crashed docling/pymupdf/markitdown
// pipeline as "this document converted to nothing" — an error rendered as
// absence, with the stderr tail dropped on the floor.
func TestConverterExceptionIsAFailureNotAnEmptyConversion(t *testing.T) {
	converter := realPythonConverter(t)
	document := filepath.Join(t.TempDir(), "paper.zzz")
	if err := os.WriteFile(document, []byte("body"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := converter.Convert(context.Background(), Request{Path: document, Kind: "zzz"})
	if err == nil {
		t.Fatal("a raising conversion returned no error")
	}
	if !errors.Is(err, ErrConverterFailed) {
		t.Fatalf("Convert() error = %v, want ErrConverterFailed", err)
	}
	if errors.Is(err, ErrConverterEmpty) || strings.Contains(err.Error(), "EMPTY-text") {
		t.Fatalf("a crashed conversion still reads as an EMPTY document: %v", err)
	}
	if !strings.Contains(err.Error(), "ValueError") {
		t.Fatalf("the exception class is missing from the error: %v", err)
	}
	if !strings.Contains(err.Error(), "stderr:") {
		t.Fatalf("the stderr tail sibling branches splice in is missing: %v", err)
	}
}

// TestConverterFailureCarriesTheBasenameNotTheFullPath: the library exception
// text carries the document's absolute path (FileNotFoundError does), and the
// message reaches a tool answer an agent reads. Only the basename may survive.
func TestConverterFailureCarriesTheBasenameNotTheFullPath(t *testing.T) {
	converter := realPythonConverter(t)
	directory := t.TempDir()
	missing := filepath.Join(directory, "confidential-report.json")
	_, err := converter.Convert(context.Background(), Request{Path: missing, Kind: "json"})
	if err == nil {
		t.Fatal("converting a missing document returned no error")
	}
	if !errors.Is(err, ErrConverterFailed) {
		t.Fatalf("Convert() error = %v, want ErrConverterFailed", err)
	}
	if !strings.Contains(err.Error(), "FileNotFoundError") {
		t.Fatalf("the exception class is missing from the error: %v", err)
	}
	if strings.Contains(err.Error(), directory) {
		t.Fatalf("the document's full path leaked into the error: %v", err)
	}
	if !strings.Contains(err.Error(), "confidential-report.json") {
		t.Fatalf("the basename was stripped too — nothing names the document: %v", err)
	}
}

// TestConverterEmptyDocumentStaysADistinctOutcome: the other half of L2-F3 —
// a document that genuinely converts to nothing must NOT be reported as a
// crash. The worker here is a fake speaking the pipe protocol, so the outcome
// is the Go mapping alone.
func TestConverterEmptyDocumentStaysADistinctOutcome(t *testing.T) {
	converter := fakeLineConverter(t, `{"ok":true,"markdown":"","kind":"html"}`)
	_, err := converter.Convert(context.Background(), Request{Path: "doc.html", Kind: "html"})
	if !errors.Is(err, ErrConverterEmpty) {
		t.Fatalf("Convert() error = %v, want ErrConverterEmpty", err)
	}
	if errors.Is(err, ErrConverterFailed) {
		t.Fatalf("an empty document was reported as a converter failure: %v", err)
	}
}

// TestConverterOKFalseWithoutAClassStillNamesTheFailure: an older provisioned
// worker answers ok:false with no error_class. The outcome is still a named
// converter failure, never a nil error and never a silent empty result.
func TestConverterOKFalseWithoutAClassStillNamesTheFailure(t *testing.T) {
	converter := fakeLineConverter(t, `{"ok":false}`)
	_, err := converter.Convert(context.Background(), Request{Path: "doc.html", Kind: "html"})
	if !errors.Is(err, ErrConverterFailed) {
		t.Fatalf("Convert() error = %v, want ErrConverterFailed", err)
	}
	if !strings.Contains(err.Error(), "worker returned ok=false without error") {
		t.Fatalf("an ok:false with no message says nothing about why: %v", err)
	}
}

// TestConverterWorkerRunsInItsOwnProcessGroup is L2-F31: the conversion worker
// started WITHOUT ProcessGroup and was stopped with Kill(), so a converter
// dependency that shells out orphaned its children — the browser worker has
// had both since it shipped.
func TestConverterWorkerRunsInItsOwnProcessGroup(t *testing.T) {
	runner := lineRunner(t, `{"ok":true,"markdown":"# converted"}`)
	script := filepath.Join(t.TempDir(), "converter.py")
	if err := os.WriteFile(script, []byte("# fake\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	converter := NewConverter(Runtime{Python: "fake-python", Script: script, Runner: runner})
	if _, err := converter.Convert(context.Background(), Request{Path: "doc.html", Kind: "html"}); err != nil {
		t.Fatal(err)
	}
	starts := runner.Starts()
	if len(starts) != 1 {
		t.Fatalf("Start calls = %d, want 1: %+v", len(starts), starts)
	}
	if !starts[0].Opts.ProcessGroup {
		t.Fatal("the conversion worker started outside its own process group — a shelling dependency orphans children")
	}
	if err := converter.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	calls := runner.LifecycleCalls()
	if len(calls) != 2 || calls[0].Action != "kill-group" || calls[1].Action != "wait" {
		t.Fatalf("lifecycle calls = %+v, want kill-group, wait", calls)
	}
}

// TestConverterFallsBackToDirectKillWhenGroupKillFails mirrors the browser
// worker's ladder on the now-shared stop path: a refused group signal is
// reported AND followed by the direct kill, never swallowed.
func TestConverterFallsBackToDirectKillWhenGroupKillFails(t *testing.T) {
	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()
	t.Cleanup(func() {
		_ = stdinReader.Close()
		_ = stdoutWriter.Close()
	})
	runner := &deps.FakeRunner{}
	runner.ScriptInteractive([]string{"fake-python"}, deps.InteractiveScript{
		Pid:      7102,
		Stdin:    stdinWriter,
		Stdout:   stdoutReader,
		GroupErr: errors.New("group kill denied"),
	})
	script := filepath.Join(t.TempDir(), "converter.py")
	if err := os.WriteFile(script, []byte("# fake\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	converter := NewConverter(Runtime{Python: "fake-python", Script: script, Runner: runner})
	if _, err := converter.ensureWorkerLocked(); err != nil {
		t.Fatalf("ensureWorkerLocked() error = %v", err)
	}
	err := converter.Close()
	if err == nil || !strings.Contains(err.Error(), "kill converter worker process group") {
		t.Fatalf("Close() error = %v, want a contextual group-kill error", err)
	}
	calls := runner.LifecycleCalls()
	if len(calls) != 3 || calls[0].Action != "kill-group" || calls[1].Action != "kill" || calls[2].Action != "wait" {
		t.Fatalf("lifecycle calls = %+v, want kill-group, kill, wait", calls)
	}
}

// TestConverterRequestCapsStderrTheSameWayTheBrowserWorkerDoes is the
// converter sibling of browserworker.go's requestInteractive: on a write
// failure, requestInteractive tails worker stderr through stderrTail before
// splicing it into the returned error, but converter.request only ran
// strings.TrimSpace — an unbounded sidecar stderr (a stack trace, a path, a
// credentialed URL docling logged) reached the tool answer whole instead of
// capped at stderrTail's bound.
func TestConverterRequestCapsStderrTheSameWayTheBrowserWorkerDoes(t *testing.T) {
	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()
	t.Cleanup(func() {
		_ = stdoutWriter.Close()
	})
	// Closing the READ half before Start makes every future write to
	// stdinWriter fail immediately (io.ErrClosedPipe) — a write failure
	// without needing a real process on the other end.
	if err := stdinReader.Close(); err != nil {
		t.Fatal(err)
	}
	runner := &deps.FakeRunner{}
	runner.ScriptInteractive([]string{"fake-python"}, deps.InteractiveScript{
		Pid: 7201, Stdin: stdinWriter, Stdout: stdoutReader,
	})
	script := filepath.Join(t.TempDir(), "converter.py")
	if err := os.WriteFile(script, []byte("# fake\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	converter := NewConverter(Runtime{Python: "fake-python", Script: script, Runner: runner})
	worker, err := converter.ensureWorkerLocked()
	if err != nil {
		t.Fatalf("ensureWorkerLocked() error = %v", err)
	}
	// Simulate an oversized sidecar stderr the way a real docling stack trace
	// would accumulate one, byte by byte, in the buffer obs.Process.Stderr
	// wires the child's stderr pipe to. 2000 bytes is well past stderrTail's
	// 500-byte cap.
	const overLongBytes = 2000
	overLong := strings.Repeat("X", overLongBytes)
	if _, err := worker.stderr.Write([]byte(overLong)); err != nil {
		t.Fatal(err)
	}
	_, tail, err := converter.request(context.Background(), []byte(`{"op":"convert"}`))
	if err == nil {
		t.Fatal("a write on a closed stdin pipe returned no error")
	}
	if len(tail) >= overLongBytes {
		t.Fatalf("returned stderr tail is %d bytes, want capped well below the %d-byte input: %q",
			len(tail), overLongBytes, tail)
	}
	if strings.Contains(err.Error(), overLong) {
		t.Fatalf("the full uncapped stderr reached the error: %v", err)
	}
	if !strings.Contains(err.Error(), "…") {
		t.Fatalf("the error's stderr was not marked as truncated: %v", err)
	}
}

// fakeLineConverter answers every request with one fixed protocol line, from a
// scripted deps.Runner — no Python, no real process.
func fakeLineConverter(t *testing.T, response string) *Converter {
	t.Helper()
	script := filepath.Join(t.TempDir(), "converter.py")
	if err := os.WriteFile(script, []byte("# fake\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	converter := NewConverter(Runtime{
		Python: "fake-python",
		Script: script,
		Runner: lineRunner(t, response),
	})
	t.Cleanup(func() { _ = converter.Close() })
	return converter
}

// TestHTMLConversionKeepsBlockStructure: pages cut from the harvests that lost
// their structure — a wikitable after a heading that follows a citation link,
// MDN headings whose text sits in a permalink anchor with a spec table and a
// See also list, a GitHub README's div-wrapped headings and link lists, a
// docsify anchor heading after a code block, an npm README whose last sections
// are "See <bare URL>" paragraphs (link-dense, yet content: pruning them also
// stripped their headings as trailing titles), elements the reader never sees
// (a `hidden` error box, aria-hidden, display:none, visibility:hidden,
// template, noscript) beside visible text, and a GitHub discussion timeline
// whose every comment body sits in a role="presentation" table and was cut at
// its first link, its nested replies deleted by the "next-" class discard.
// A comment paragraph that is one prose link ("here is the repo for the
// replication of the issue") is the comment, not a link farm: inside the main
// content no block is pruned by link density.
// Every heading stays a "#" line, every table row a "|" line, every bullet a
// "- " line; a site nav bar outside the main content is still dropped. It needs the pinned
// interpreter (HARVESTPY_CORPUS_PYTHON): trafilatura runs for real.
func TestHTMLConversionKeepsBlockStructure(t *testing.T) {
	python := os.Getenv("HARVESTPY_CORPUS_PYTHON")
	if python == "" {
		t.Skip("HARVESTPY_CORPUS_PYTHON is not set; block-structure fixtures need the pinned interpreter")
	}
	t.Setenv("PYTHONDONTWRITEBYTECODE", "1")
	cases := []struct {
		fixture              string
		heads, rows, bullets []string
		code, absent, text   []string
	}{
		{
			fixture: "wikitable.html",
			heads:   []string{"## Prize", "## List of laureates", "## 50-year secrecy rule"},
			rows:    []string{"Wilhelm Röntgen", "Hendrik Lorentz", "Marie Curie", "Lord Rayleigh", "Robert Koch"},
		},
		{
			fixture: "mdn.html",
			heads: []string{
				"## Description",
				"### Array indices",
				"#### Normalization of the length property",
				"## Specifications",
				"## See also",
			},
			rows:    []string{"ECMAScript® 2027 Language Specification"},
			bullets: []string{"Indexed collections", "TypedArray", "ArrayBuffer"},
		},
		{
			fixture: "awesome.html",
			heads:   []string{"# Awesome Python", "## Categories", "## Science", "### Type Checkers", "## Podcasts"},
			bullets: []string{
				"HTTP Clients",
				"Web Scraping",
				"Email",
				"ORM",
				"Caching",
				"Core",
				"numpy",
				"scipy",
				"Symbolic Mathematics",
				"sympy",
				"mypy",
				"pyright",
				"Talk Python To Me",
				"Python Bytes",
			},
		},
		{
			fixture: "docsify.html",
			heads:   []string{"# Quick start", "## Initialize", "## Writing content"},
			code:    []string{"npm i docsify-cli -g", "docsify init ./docs"},
		},
		{
			fixture: "sitenav.html",
			heads:   []string{"# Field notes on river gauges", "## Related reading"},
			bullets: []string{"Stilling wells", "Staff gauges"},
			absent:  []string{"Careers", "Pricing", "Contact us"},
		},
		{
			fixture: "npm-readme.html",
			heads:   []string{"## Usage", "## Documentation", "## API"},
			code:    []string{"import { useState } from 'react';"},
			text: []string{
				"See [https://react.dev/](https://react.dev/)",
				"See [https://react.dev/reference/react](https://react.dev/reference/react)",
			},
			absent: []string{"Pricing", "Advisories"},
		},
		{
			fixture: "hidden.html",
			text: []string{
				"The upstream staff gauge was reset after the spring flood moved its datum by four centimetres.",
				"Readings from the stilling well agree with the staff gauge to within two millimetres.",
				"Appendix: the full calibration table is kept with the station records.",
				"The next inspection is scheduled for the first dry week of autumn.",
			},
			absent: []string{
				"Uh oh!",
				"There was an error while loading",
				"Decorative banner",
				"Collapsed transcript",
				"Placeholder tooltip",
				"Template row",
				"Enable JavaScript",
			},
		},
		{
			// trafilatura routes a comment-list section to its comment handler:
			// the comment keeps its link and its inline code, and its code block
			// stays fenced.
			fixture: "blog-comments.html",
			code:    []string{"datum_offset = 0.040", `units = "m"`},
			text: []string{
				"The stilling well and the corrected staff gauge now agree to within two millimetres," +
					" which is inside the tolerance the network asks of a manual station.",
				"We had the same drift last year; the" +
					" [survey guide from the regional office](https://example.org/gauge-survey-guide)" +
					" walks through the benchmark tie-in, and the logger reads the offset from the `datum_offset`" +
					" field before it writes each record.",
				"After that change the logger config looked like this:",
				"Did you also re-level the stilling well intake, or only the staff gauge?",
			},
			absent: []string{"Pricing", "Careers", "Contact us"},
		},
		{
			fixture: "github-timeline.html",
			text: []string{
				"Hey folks, exciting news. [The Next.js App Router is now stable](https://nextjs.org/blog/next-13-4)!",
				"We've made a new section in Discussions [specifically for the App Router]" +
					"(https://github.com/user-20/next.js/discussions/categories/app-router)" +
					" where you can open new discussions and continue the conversation.",
				"Hey [@user-04](https://github.com/user-04),",
				"So for bugs not resolved in the stable release of App Router that were added in this discussion," +
					" should those be split into their own issues now?",
				"Bugs should be reported on GitHub issues following the issue template with a reproduction provided yeah 👍",
				"Many thanks!",
				"[here is the repo for the replication of the issue](https://github.com/user-31/app-dir-revalidate-repro)",
				"In the future this will be expanded to be more granular than per-page deciding static rendering" +
					" or dynamic rendering.",
			},
			absent: []string{"Uh oh!", "There was an error while loading", "|---|"},
		},
	}
	converter := testConverter(t, python)
	t.Cleanup(func() { _ = converter.Close() })
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			result, err := converter.Convert(
				context.Background(),
				Request{Path: filepath.Join("testdata", "blocks", tc.fixture), Kind: "html"},
			)
			if err != nil {
				t.Fatalf("convert: %v", err)
			}
			lines := strings.Split(result.Markdown, "\n")
			has := func(match func(string) bool) bool {
				for _, line := range lines {
					if match(line) {
						return true
					}
				}
				return false
			}
			for _, head := range tc.heads {
				if !has(func(line string) bool { return line == head }) {
					t.Errorf("heading %q is not its own line", head)
				}
			}
			for _, row := range tc.rows {
				if !has(func(line string) bool { return strings.HasPrefix(line, "|") && strings.Contains(line, row) }) {
					t.Errorf("table row holding %q is missing", row)
				}
			}
			for _, bullet := range tc.bullets {
				if !has(func(line string) bool {
					return strings.HasPrefix(strings.TrimLeft(line, " "), "- ") && strings.Contains(line, bullet)
				}) {
					t.Errorf("bullet %q is missing", bullet)
				}
			}
			for _, code := range tc.code {
				if !has(func(line string) bool { return line == code }) {
					t.Errorf("code line %q is not its own line", code)
				}
			}
			for _, line := range tc.text {
				if !has(func(got string) bool { return got == line }) {
					t.Errorf("paragraph %q is missing", line)
				}
			}
			for _, word := range tc.absent {
				if strings.Contains(result.Markdown, word) {
					t.Errorf("%q leaked into the content", word)
				}
			}
			if t.Failed() {
				t.Logf("markdown:\n%s", result.Markdown)
			}
		})
	}
}

// TestHTMLFullDOMConversionDropsHiddenElements: the recall gate's full-page
// fallback writes the whole DOM, boilerplate included, yet never an element the
// reader cannot see — a `hidden` error box or a <template>'s inert markup is not
// on the page. hidden="until-found" is text a find-in-page reveals: kept. It
// needs the pinned interpreter (HARVESTPY_CORPUS_PYTHON): markitdown runs for real.
func TestHTMLFullDOMConversionDropsHiddenElements(t *testing.T) {
	python := os.Getenv("HARVESTPY_CORPUS_PYTHON")
	if python == "" {
		t.Skip("HARVESTPY_CORPUS_PYTHON is not set; the full-page fixture needs the pinned interpreter")
	}
	t.Setenv("PYTHONDONTWRITEBYTECODE", "1")
	converter := testConverter(t, python)
	t.Cleanup(func() { _ = converter.Close() })
	result, err := converter.Convert(
		context.Background(),
		Request{Path: filepath.Join("testdata", "blocks", "hidden.html"), Kind: "html", FullDOM: true},
	)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	for _, text := range []string{
		"The upstream staff gauge was reset after the spring flood moved its datum by four centimetres.",
		"Readings from the stilling well agree with the staff gauge to within two millimetres.",
		"Appendix: the full calibration table is kept with the station records.",
		"The next inspection is scheduled for the first dry week of autumn.",
	} {
		if !strings.Contains(result.Markdown, text) {
			t.Errorf("visible paragraph %q is missing", text)
		}
	}
	for _, word := range []string{"Uh oh!", "There was an error while loading", "Template row"} {
		if strings.Contains(result.Markdown, word) {
			t.Errorf("hidden text %q leaked into the full-page conversion", word)
		}
	}
	if t.Failed() {
		t.Logf("markdown:\n%s", result.Markdown)
	}
}
