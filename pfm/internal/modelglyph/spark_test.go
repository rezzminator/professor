package modelglyph

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestSparkLevelsAreSixOneCellScanLines(t *testing.T) {
	want := [...]string{"_", "⎽", "⎼", "⎻", "⎺", "¯"}
	if SparkLevels != want {
		t.Fatalf("SparkLevels = %q, want %q", SparkLevels, want)
	}
	for _, level := range SparkLevels {
		if width := ansi.StringWidth(level); width != 1 {
			t.Errorf("level %q is %d cells wide, want 1", level, width)
		}
	}
}
