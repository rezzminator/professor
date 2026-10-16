package themesync

import (
	"strings"
	"testing"
)

func TestTmuxOptionsRenderStylesAndUserOptions(t *testing.T) {
	palette := tokyoNight(t)
	options := palette.tmuxOptions(Dark, 3)
	byName := map[string]string{}
	for _, option := range options {
		byName[option.Name] = option.Value
	}
	if len(options) != len(byName) || len(options) != 21 {
		t.Fatalf("got %d options (%d unique), want 21", len(options), len(byName))
	}
	format := "#{?#{==:#{@pfm-state},blocked},#f7768e,#{?#{==:#{@pfm-state},working},#e0af68," +
		"#{?#{==:#{@pfm-state},done},#9ece6a,#565f89}}}"
	want := map[string]string{
		"status-style":                "bg=#1f2335,fg=#c0caf5",
		"window-status-style":         "fg=#565f89",
		"window-status-current-style": "fg=" + format + ",bold",
		"pane-border-style":           "fg=#292e42",
		"pane-active-border-style":    "fg=" + format,
		"message-style":               "bg=#292e42,fg=#c0caf5",
		"message-command-style":       "bg=#1f2335,fg=#7aa2f7",
		"mode-style":                  "bg=#283457,fg=#c0caf5",
		"@pfm-theme":                  "tokyo-night",
		"@pfm-theme-generation":       "3",
		"@pfm-c-bg":                   "#1a1b26",
		"@pfm-c-accent":               "#7aa2f7",
		"@pfm-state-blocked":          "#f7768e",
		"@pfm-state-idle":             "#565f89",
		"@pfm-state-colour":           format,
	}
	for name, value := range want {
		if byName[name] != value {
			t.Errorf("%s = %q, want %q", name, byName[name], value)
		}
	}
	for _, option := range options {
		if strings.ContainsAny(option.Value, "\n\x00") {
			t.Errorf("%s value holds a control character: %q", option.Name, option.Value)
		}
	}
}

func TestTmuxPriorOptionNameAndSentinel(t *testing.T) {
	if got := tmuxPriorName("status-style"); got != "@pfm-theme-prior-status-style" {
		t.Fatalf("tmuxPriorName = %q", got)
	}
	if got := tmuxPriorValue(""); got != "v:" {
		t.Fatalf("an empty prior must still be a non-empty sentinel, got %q", got)
	}
	if value, ok := tmuxPriorParse("v:bg=red"); !ok || value != "bg=red" {
		t.Fatalf("tmuxPriorParse = %q, %v", value, ok)
	}
	if _, ok := tmuxPriorParse(""); ok {
		t.Fatal("an empty read is not a captured prior")
	}
}
