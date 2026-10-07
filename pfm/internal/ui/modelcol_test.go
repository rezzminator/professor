package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/rezzminator/professor/pfm/internal/compose"
)

func TestModelCellIsAlwaysExactlyItsWidth(t *testing.T) {
	for _, row := range []compose.Row{
		{},
		{Model: "claude-opus-4-7"},
		{Model: "claude-opus-4-7", Effort: "high"},
		{Model: "gpt-6-astra", Effort: "low"},
		{Model: "claude-sonnet-5-5", Effort: "turbo"},
		{Effort: "high"},
	} {
		if got := spansWidth(modelCell(row, "")); got != deckModelW {
			t.Errorf(
				"modelCell(%+v) is %d cells, want %d: %q",
				row,
				got,
				deckModelW,
				ansi.Strip(joinSpans(modelCell(row, ""))),
			)
		}
	}
}

func TestModelCellShowsWhatIsKnownAndNothingElse(t *testing.T) {
	read := func(row compose.Row) string { return ansi.Strip(joinSpans(modelCell(row, ""))) }
	if got := read(compose.Row{Model: "claude-opus-4-7", Effort: "high"}); got != "◆🚄" {
		t.Errorf("a known model and effort read %q", got)
	}
	if got := read(compose.Row{Model: "claude-haiku-5"}); got != "○  " {
		t.Errorf("a model with no known effort shows its symbol alone, got %q", got)
	}
	if got := read(compose.Row{Effort: "high"}); strings.TrimSpace(got) != "" {
		t.Errorf("an effort with no model is not shown: %q", got)
	}
	if got := read(compose.Row{}); strings.TrimSpace(got) != "" {
		t.Errorf("an unread row is blank: %q", got)
	}
}

func TestShortModelDropsTheVendorPrefix(t *testing.T) {
	if got := shortModel("claude-opus-4-7"); got != "opus-4-7" {
		t.Errorf("shortModel = %q", got)
	}
	if got := shortModel("gpt-6-astra"); got != "gpt-6-astra" {
		t.Errorf("a model without the prefix is unchanged, got %q", got)
	}
}
