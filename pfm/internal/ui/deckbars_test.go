package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestReceiptToneSeparatesRefusalsFromLandedActions(t *testing.T) {
	palette := configuredPalette
	cases := map[string]string{
		"⌃X refused — split live window":      palette.Warn,
		"deactive failed — P:AUDIT: boom":     palette.Warn,
		"hidden — P:AUDIT":                    palette.LimitGreen,
		"deactivated — P:AUDIT — ready to go": palette.LimitGreen,
	}
	for receipt, want := range cases {
		if got := receiptTone(receipt).fg; got != want {
			t.Errorf("receiptTone(%q) = %s, want %s", receipt, got, want)
		}
	}
}

func TestRenderQueryReportsVisibleMatchesAndReceipts(t *testing.T) {
	model := deckModel(120, 30)
	if got := ansi.Strip(model.renderQuery(100)); !strings.Contains(got, "/") || !strings.Contains(got, "visible") {
		t.Errorf("an idle query line counts the visible chats: %q", got)
	}
	for _, runeValue := range "atl" {
		model, _ = applyKey(t, model, printableKey(runeValue))
	}
	if got := ansi.Strip(model.renderQuery(100)); !strings.Contains(got, "match") {
		t.Errorf("a typed query counts its matches: %q", got)
	}
	model.killStatus = "⌃X refused — nope"
	line := model.renderQuery(100)
	if !strings.Contains(ansi.Strip(line), "⌃X refused — nope") {
		t.Errorf("a receipt takes the status slot: %q", ansi.Strip(line))
	}
	if !strings.Contains(line, receiptTone(model.killStatus).render("⌃X refused — nope")) {
		t.Error("a refusal is painted in the warn tone")
	}
	if got := ansi.StringWidth(line); got != 100 {
		t.Errorf("the query line spans %d cells, want 100", got)
	}
}

func TestKeycapDimsAKeyTheRowWouldRefuse(t *testing.T) {
	on, off := keycap("⌃O", "reboot", true), keycap("⌃O", "reboot", false)
	if ansi.Strip(joinSpans(on)) != ansi.Strip(joinSpans(off)) {
		t.Error("an unavailable key keeps its wording, only its colour changes")
	}
	if on[0].paint == off[0].paint || on[1].paint == off[1].paint || !off[1].paint.dim {
		t.Errorf("an unavailable key is dimmed: on %+v off %+v", on, off)
	}
}

func footerText(first, second []span) string {
	return ansi.Strip(joinSpans(first)) + "\n" + ansi.Strip(joinSpans(second))
}

func TestChatsFooterKeysFollowTheSelectedRow(t *testing.T) {
	model := deckModel(160, 38)
	if got := footerText(model.chatsFooterKeys(false)); !strings.Contains(got, "upgrade") {
		t.Errorf("the update banner arms the upgrade: %q", got)
	}
	model = selectChat(t, model, "New Claude chat")
	if got := footerText(model.chatsFooterKeys(false)); !strings.Contains(got, "start") {
		t.Errorf("a new-chat row arms start: %q", got)
	}
	model = selectChat(t, model, "P:BUILDER")
	first, second := model.chatsFooterKeys(false)
	if got := footerText(first, second); !strings.Contains(got, "kill") || !strings.Contains(got, "reboot") {
		t.Errorf("a live chat offers kill and reboot: %q", got)
	}
	for _, part := range second {
		if strings.Contains(part.text, "reboot") && part.paint.dim {
			t.Error("reboot is live-only and a live chat has it lit")
		}
	}
	model = selectChat(t, model, "release notes")
	_, second = model.chatsFooterKeys(false)
	if got := footerText(nil, second); !strings.Contains(got, "hide") {
		t.Errorf("a resumable chat offers hide: %q", got)
	}
	for _, part := range second {
		if strings.Contains(part.text, "reboot") && !part.paint.dim {
			t.Error("reboot is dimmed on a chat that is not running")
		}
	}
	model = selectChat(t, model, "old idea")
	if _, second = model.chatsFooterKeys(false); !strings.Contains(footerText(nil, second), "unhide") {
		t.Error("a hidden chat offers unhide")
	}
}

func TestFooterKeepsTheShippedHelpOnOtherTabsAndFitsEveryWidth(t *testing.T) {
	model := deckModel(120, 30)
	for _, width := range []int{40, 80, 95, 96, 120, 200} {
		for _, tab := range []Tab{TabChats, TabStats, TabLimits, TabCosmos} {
			model.tab = tab
			lines := strings.Split(model.renderFooter(width), "\n")
			if len(lines) != 2 {
				t.Fatalf("tab %d width %d: the footer has %d lines", tab, width, len(lines))
			}
			for number, line := range lines {
				if got := ansi.StringWidth(line); got != width {
					t.Errorf("tab %d width %d line %d spans %d cells", tab, width, number, got)
				}
			}
		}
	}
	model.tab = TabChats
	if wide := ansi.Strip(model.renderFooter(120)); !strings.Contains(wide, "type to fuzzy-find") {
		t.Errorf("the wide footer teaches search: %q", wide)
	}
	if narrow := ansi.Strip(model.renderFooter(80)); strings.Contains(narrow, "type to fuzzy-find") {
		t.Errorf("the compact footer drops the search hint: %q", narrow)
	}
	model.tab = TabLimits
	if got := ansi.Strip(model.renderFooter(120)); !strings.Contains(got, "pgup/pgdown page") {
		t.Errorf("the Limits footer is unchanged: %q", got)
	}
}
