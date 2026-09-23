package harvestpy

import (
	"context"
	"regexp"
	"strings"
	"testing"
)

var officeWord = regexp.MustCompile(`[\p{L}\p{N}]+`)

// wordRecall is the share of the oracle's words (a multiset) the converter's
// markdown carries.
func wordRecall(oracle, got string) (float64, int) {
	have := map[string]int{}
	for _, word := range officeWord.FindAllString(strings.ToLower(got), -1) {
		have[word]++
	}
	words := officeWord.FindAllString(strings.ToLower(oracle), -1)
	found := 0
	for _, word := range words {
		if have[word] > 0 {
			have[word]--
			found++
		}
	}
	if len(words) == 0 {
		return 0, 0
	}
	return float64(found) / float64(len(words)), len(words)
}

// TestOfficeFormatsMatchTheBakeOff: each Office reader the office bake-off
// chose reads the bake-off's own real file (scrubbed of author metadata) and
// carries the words the bake-off's winning run returned (the oracle is that
// run's output, testdata/formats/office/<file>.bakeoff.md); every loss the
// bake-off measured is named in the markdown. bakeoff is the winner's recall
// against the reference in the bake-off's tables.md. Watched FAILING with
// these kinds in _NOT_YET_PARSED ("detected but not parsed yet").
func TestOfficeFormatsMatchTheBakeOff(t *testing.T) {
	converter := pinnedPythonConverter(t)
	cases := []struct {
		file, kind string
		bakeoff    float64
		losses     []string
	}{
		{"multiscript.doc", "doc", 0.992, []string{"plain text only", "no tables"}},
		{"Chart679.xls", "xls", 1.0, []string{"number and date formats are not applied"}},
		{"multiscript.rtf", "rtf", 0.99, []string{"tables are lost", "flattened to plain text"}},
		{"multiscript.odt", "odt", 0.992, nil},
		{"multiscript.ods", "ods", 0.99, []string{"number and date formats are not applied"}},
		{"KEY02.odp", "odp", 1.0, nil},
		{"SimpleMacro.docm", "docm", 1.0, []string{"macros are not read"}},
		{"SimpleMacro.xlsm", "xlsm", 1.0, []string{"macros are not read"}},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			path, _ := formatFixture(t, "office/"+tc.file)
			_, oracle := formatFixture(t, "office/"+tc.file+".bakeoff.md")
			got, err := converter.Convert(context.Background(), Request{Path: path, Kind: tc.kind})
			if err != nil {
				t.Fatalf("%s as %s: %v", tc.file, tc.kind, err)
			}
			recall, words := wordRecall(oracle, got.Markdown)
			t.Logf(
				"%s: %d chars, word recall %.3f of the bake-off winner's %d words (bake-off recall vs reference %.3f)",
				tc.file,
				len(got.Markdown),
				recall,
				words,
				tc.bakeoff,
			)
			if recall < 0.98 {
				t.Errorf("%s: word recall %.3f against the bake-off winner's output, want >= 0.98", tc.file, recall)
			}
			for _, loss := range tc.losses {
				if !strings.Contains(got.Markdown, loss) {
					t.Errorf("%s: the loss %q is not named in the markdown", tc.file, loss)
				}
			}
		})
	}
}

// TestOfficeFormatsNameWhatTheyCannotRead: a padded ODS reads (LibreOffice's
// ~1,048,000 repeated rows hung docling and odfdo for 180 s in the bake-off),
// a truncated RTF is named, Word 95 and a password-protected file end in a
// named failure — never an empty document, never binary characters.
func TestOfficeFormatsNameWhatTheyCannotRead(t *testing.T) {
	converter := pinnedPythonConverter(t)
	convert := func(t *testing.T, file, kind string) (Result, error) {
		t.Helper()
		path, _ := formatFixture(t, "office/"+file)
		return converter.Convert(context.Background(), Request{Path: path, Kind: kind})
	}
	t.Run("padded ods is clamped", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 60e9)
		defer cancel()
		path, _ := formatFixture(t, "office/fdo67682-2.ods")
		got, err := converter.Convert(ctx, Request{Path: path, Kind: "ods"})
		if err != nil {
			t.Fatalf("padded ods: %v", err)
		}
		if !strings.Contains(got.Markdown, "LibreOffice's sheet padding") || !strings.Contains(got.Markdown, "| ") {
			t.Errorf("padded ods: no table or no clamp note:\n%.600s", got.Markdown)
		}
		t.Logf("fdo67682-2.ods: %d chars (bake-off: docling and odfdo hung past 180 s)", len(got.Markdown))
	})
	t.Run("truncated rtf is named", func(t *testing.T) {
		got, err := convert(t, "corrupt-truncated.rtf", "rtf")
		if err != nil {
			t.Fatalf("truncated rtf: %v", err)
		}
		if !strings.Contains(got.Markdown, "TRUNCATED") {
			t.Errorf("truncated rtf: the cut is not named:\n%.400s", got.Markdown)
		}
	})
	for _, tc := range []struct{ name, file, kind, want string }{
		{"word 95", "Word95.doc", "doc", "Word 95 (or older)"},
		{"encrypted doc", "PasswordProtected.doc", "doc", "password-protected"},
		{"encrypted ooxml", "protected_passtika.xlsx", "encrypted", "password-protected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := convert(t, tc.file, tc.kind)
			if err == nil {
				t.Fatalf("%s: converted to %d chars, want a named failure", tc.file, len(got.Markdown))
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%s: %v, want it to name %q", tc.file, err, tc.want)
			}
		})
	}
}
