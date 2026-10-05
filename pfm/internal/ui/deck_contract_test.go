package ui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/rezzminator/professor/pfm/internal/theme"
)

// The fence's lane scripts (infra/fence/lanes: tui_open, tui_selected, tui_cache,
// tui_account, F.08) and the attach jail drive the real picker through a
// terminal and read its frame as text. These are the strings they scrape; a
// redesign that moves one breaks them silently, far from this package — so each
// is pinned here, at the widths the lanes use and at a width with the dossier.

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

func TestFrameKeepsTheStringsTheLaneScriptsScrape(t *testing.T) {
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
	for _, width := range []int{100, 160} {
		model := selectChat(t, deckModel(width, 30), "P:BUILDER")
		want := []string{"◖▶ open◗", "◖⚡ reboot◗", "◖✖ kill◗"}
		for step, label := range want {
			if !strings.Contains(strings.Join(frameLines(model), "\n"), label) {
				t.Fatalf("width %d step %d: the selected row lacks %q", width, step, label)
			}
			model, _ = applyKey(t, model, specialKey(tea.KeyRight))
		}
	}
}

func TestHeaderAccountAndCacheAreReadableByTheLaneRegexes(t *testing.T) {
	model := deckModel(100, 30)
	line := strings.Join(frameLines(model), "\n")
	if got := regexp.MustCompile(`account (\d+) ·`).FindStringSubmatch(line); len(got) != 2 || got[1] != "2" {
		t.Errorf("tui_account reads %v from the frame", got)
	}
	if got := regexp.MustCompile(`⚡ 1h|🪫 5m`).FindString(line); got != "⚡ 1h" {
		t.Errorf("tui_cache reads %q from the frame", got)
	}
	model, _ = applyKey(t, model, controlKey('e'))
	if got := regexp.MustCompile(`⚡ 1h|🪫 5m`).FindString(strings.Join(frameLines(model), "\n")); got != "🪫 5m" {
		t.Errorf("⌃E must flip the header's cache, got %q", got)
	}
}

func TestGroupedCursorRowKeepsItsIndentedChevron(t *testing.T) {
	model := selectChat(t, deckModel(100, 30), "P:AUDIT")
	if got := scrapedSelection(model); got != "● P:AUDIT" {
		t.Errorf("a name-group member is still read as marker and name, got %q", got)
	}
}

// F.09 holds the live pane to the golden's shape: a masthead, the Chats context
// line with the three counts, both footers, and exactly the pane's lines.
func TestFrameKeepsTheShapeLaneF09Pins(t *testing.T) {
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

// T36 reads the palette's header background out of the live escapes: it must be
// on the frame in every palette, and the other palette's must not be.
func TestFrameCarriesThePalettesHeaderBackground(t *testing.T) {
	t.Cleanup(func() { configureStyles(theme.Load("default")) })
	sgr := func(hex string) string {
		colour := rgbFromHex(hex)
		return fmt.Sprintf("48;2;%d;%d;%d", colour.R, colour.G, colour.B)
	}
	frames := map[string]string{}
	for _, name := range []string{"default", "tokyo-night"} {
		snapshot := deckFleet(80, 24)
		snapshot.Theme = name
		model := NewModel(snapshot)
		model.nowNS = fixtureNowNS
		frames[name] = model.View().Content
	}
	def, tokyo := theme.Load("default").HeaderBg, theme.Load("tokyo-night").HeaderBg
	if !strings.Contains(frames["default"], sgr(def)) {
		t.Errorf("the default frame lacks its header background %s", def)
	}
	if !strings.Contains(frames["tokyo-night"], sgr(tokyo)) {
		t.Errorf("the tokyo-night frame lacks its header background %s", tokyo)
	}
	if strings.Contains(frames["tokyo-night"], sgr(def)) {
		t.Errorf("the tokyo-night frame still paints the default header background %s", def)
	}
}
