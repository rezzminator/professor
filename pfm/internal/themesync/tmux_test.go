package themesync

import (
	"strings"
	"testing"
)

func TestTmuxOptionsRenderStylesAndUserOptions(t *testing.T) {
	options := tmuxOptions(tokyoNightApplied(t), Dark, 3)
	byName := map[string]string{}
	for _, option := range options {
		byName[option.Name] = option.Value
	}
	if len(options) != len(byName) || len(options) != 30 {
		t.Fatalf("got %d options (%d unique), want 30", len(options), len(byName))
	}
	format := "#{?#{==:#{@pfm-state},blocked},#ffd23f,#{?#{==:#{@pfm-state},working},#2d9bff," +
		"#{?#{==:#{@pfm-state},done},#00ffa3,#9b8fd6}}}"
	want := map[string]string{
		"status-style":                "bg=#221634,fg=#ff9ed6",
		"window-status-style":         "fg=#7a6fb0",
		"window-status-current-style": "fg=" + format + ",bold",
		"pane-border-style":           "fg=#7a6fb0",
		"pane-active-border-style":    "fg=" + format,
		"message-style":               "bg=#4a3d6b,fg=#ff9ed6",
		"message-command-style":       "bg=#221634,fg=#c95cff",
		"mode-style":                  "bg=#3a2d6b,fg=#ff9ed6",
		"@host_accent":                "#c95cff",
		"@p_g0":                       "#221634",
		"@p_g3":                       "#0f0f1a",
		"@p_fg":                       "#ff9ed6",
		"@p_fg1":                      "#7a6fb0",
		"@p_fg2":                      "#7a6fb0",
		"@p_red":                      "#ff3d6e",
		"@p_amber":                    "#ffd23f",
		"@pfm-theme-chips":            "1",
		"@pfm-theme":                  "custom:tokyo-night",
		"@pfm-theme-generation":       "3",
		"@pfm-c-bg":                   "#0f0f1a",
		"@pfm-c-accent":               "#c95cff",
		"@pfm-state-blocked":          "#ffd23f",
		"@pfm-state-idle":             "#9b8fd6",
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
