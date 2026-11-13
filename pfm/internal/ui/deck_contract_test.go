package ui

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// The journey suite's picker steps (pfm/journey) and the attach jail drive the
// real picker through a terminal and read its frame as text. These are the
// strings they scrape; a redesign that moves one breaks them silently, far from
// this package — so each is pinned here, at two terminal widths and at a width
// with the dossier.

func frameLines(model Model) []string {
	return strings.Split(ansi.Strip(model.View().Content), "\n")
}

var selectedLine = regexp.MustCompile(`^[[:space:]│┃]*› `)

// scrapedSelection is tui_selected: the first line carrying the cursor, from the
// cursor to the first double space.
func scrapedSelection(model Model) string {
	for _, line := range frameLines(model) {
		if selectedLine.MatchString(line) {
			_, after, _ := strings.Cut(line, "› ")
			after = strings.TrimLeft(after, " ")
			before, _, _ := strings.Cut(after, "  ")
			return before
		}
	}
	return ""
}

func TestFrameKeepsTheStringsTerminalReadersScrape(t *testing.T) {
	for _, size := range [][2]int{{100, 30}, {160, 38}} {
		model := selectChat(t, deckModel(size[0], size[1]), "P:BUILDER")
		model.nowNS = fixtureNowNS
		frame := strings.Join(frameLines(model), "\n")
		for _, want := range []string{
			" tabs ", "╭─ fleet", "Chats · ", "find › ", "visible", "◖▶ open◗", "hidden", "account 2 ·", "⚡ 1h",
		} {
			if !strings.Contains(frame, want) {
				t.Errorf("%dx%d: the frame lost %q:\n%s", size[0], size[1], want, frame)
			}
		}
		if got := scrapedSelection(model); got != "● P:BUILDER" {
			t.Errorf("%dx%d: tui_selected reads %q, want the cursor row's marker and name", size[0], size[1], got)
		}
		if !strings.Contains(frame, "› ● P:BUILDER") && !strings.Contains(frame, "›   ● P:BUILDER") {
			t.Errorf("%dx%d: the cursor, marker and name must read in one run:\n%s", size[0], size[1], frame)
		}
	}
}

func TestCarouselWalksOnTheSelectedRowAtEveryWidth(t *testing.T) {
	var frames []string
	for _, width := range []int{100, 160} {
		model := selectChat(t, deckModel(width, 30), "P:BUILDER")
		for range 3 {
			frames = append(frames, scrapedSelectionLine(model))
			model, _ = applyKey(t, model, specialKey(tea.KeyRight))
		}
	}
	golden(t, "ui_carousel.txt", strings.Join(frames, "\n"))
}

// scrapedSelectionLine is the whole frame line carrying the cursor.
func scrapedSelectionLine(model Model) string {
	for _, line := range frameLines(model) {
		if selectedLine.MatchString(line) {
			return line
		}
	}
	return ""
}

func TestHeaderAccountAndCacheAreReadableByRegex(t *testing.T) {
	line := strings.Join(frameLines(deckModel(100, 30)), "\n")
	account := regexp.MustCompile(`account (\d+) ·`).FindStringSubmatch(line)
	cache := regexp.MustCompile(`⚡ 1h|🪫 5m`).FindString(line)
	if want := []string{"account 2 ·", "2"}; !slices.Equal(account, want) || cache != "⚡ 1h" {
		t.Errorf("the scrapers read account %q and cache %q, want %q and %q", account, cache, want, "⚡ 1h")
	}
}

func TestGroupedCursorRowKeepsItsIndentedChevron(t *testing.T) {
	model := selectChat(t, deckModel(100, 30), "P:AUDIT")
	if got := scrapedSelection(model); got != "● P:AUDIT" {
		t.Errorf("a name-group member is still read as marker and name, got %q", got)
	}
}

// The live pane keeps the golden's shape: a masthead, the Chats context line
// with the three counts, both footers, and exactly the pane's lines.
func TestFrameKeepsTheGoldensShape(t *testing.T) {
	model := deckModel(80, 24)
	model.nowNS = fixtureNowNS
	lines := frameLines(model)
	if len(lines) != 24 {
		t.Fatalf("the frame is %d lines for a 24-line pane", len(lines))
	}
	if !strings.HasPrefix(lines[0], " ◆ pfm ") {
		t.Errorf("the masthead line is not the pinned shape: %q", lines[0])
	}
	context := regexp.MustCompile(
		`^ Chats · .+ account [0-9]+ · (⚡ 1h|🪫 5m) · [0-9]+ rows · [0-9]+ hidden · [0-9]+ empty`)
	if !context.MatchString(lines[2]) {
		t.Errorf("the Chats context line is not the pinned shape: %q", lines[2])
	}
	if !strings.Contains(lines[1], "tab/shift+tab") || !strings.HasPrefix(lines[1], " tabs ") {
		t.Errorf("the tabs line is not the pinned shape: %q", lines[1])
	}
	counts := regexp.MustCompile(`╭─ fleet (\d+) `).FindStringSubmatch(strings.Join(lines, "\n"))
	visible := regexp.MustCompile(` (\d+)/(\d+) visible`).FindStringSubmatch(strings.Join(lines, "\n"))
	if len(counts) != 2 || len(visible) != 3 || visible[1] != counts[1] || visible[2] != counts[1] {
		t.Errorf("fleet %v and visible %v are one count", counts, visible)
	}
	for _, line := range lines[len(lines)-2:] {
		if !strings.HasPrefix(line, "  ") || strings.TrimSpace(line) == "" {
			t.Errorf("a footer line is blank or unindented: %q", line)
		}
	}
}
