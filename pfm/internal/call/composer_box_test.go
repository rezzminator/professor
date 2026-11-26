package call

import "testing"

func TestComposerBoxEmptyRejectsBoxDrawingDraftRows(t *testing.T) {
	for _, glyph := range []string{"─", "│"} {
		t.Run(glyph, func(t *testing.T) {
			frame := "────────\n❯ \n  " + glyph + "\n  unfinished draft\n────────\n"
			if ComposerBoxEmpty(frame) {
				t.Fatalf("box-drawing draft row %q was read as the closing rule", glyph)
			}
		})
	}
	if !ComposerBoxEmpty("❯ \n─\n") || !ComposerBoxEmpty("────────\n❯ \n────────\n") {
		t.Fatal("existing no-top-rule and empty-box frames must remain empty")
	}
}
