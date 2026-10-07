package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/rezzminator/professor/pfm/internal/compose"
)

func TestRowBadgePartsAreTheOneSourceOfEveryBadge(t *testing.T) {
	row := compose.Row{
		Kind: compose.LiveCodex, ServerCount: 2, SplitCount: 3, Account: 2,
		C1H: true, Attached: true, Here: true, Killed: true,
	}
	parts := rowBadgeParts(row)
	var texts []string
	kinds := map[string]badgeKind{}
	for _, part := range parts {
		texts = append(texts, part.text)
		kinds[part.text] = part.kind
	}
	for _, want := range []string{"⬢", "⚠2srv", "⊞3", "⚡", "⇄", "←here", "·hidden"} {
		if !strings.Contains(strings.Join(texts, "|"), want) {
			t.Errorf("badge %q missing from %v", want, texts)
		}
	}
	if kinds["⚠2srv"] != badgeWarn || kinds["·hidden"] != badgeDim || kinds["⬢"] != badgePlain {
		t.Errorf("badge kinds = %v", kinds)
	}
	model := Model{}
	if got, want := ansi.Strip(model.rowBadges(row)), strings.Join(texts, " "); got != want {
		t.Errorf("the plain twin prints %q, want the parts joined %q", got, want)
	}
}

func TestLaunchUnreadReplacesTheMedalAndHidesTheCacheBolt(t *testing.T) {
	parts := rowBadgeParts(compose.Row{Kind: compose.ResumeClaude, Account: 3, C1H: true, LaunchUnread: true})
	if len(parts) != 1 || parts[0].text != "⚠" || parts[0].kind != badgeWarn {
		t.Fatalf("parts = %#v, want a single warn ⚠", parts)
	}
}

func TestAgentAndOpenCodeCarryTheirEngineBadge(t *testing.T) {
	if parts := rowBadgeParts(compose.Row{Kind: compose.Agent}); len(parts) != 1 || parts[0].text != "⚙ agent" {
		t.Errorf("agent parts = %#v", parts)
	}
	if parts := rowBadgeParts(compose.Row{Kind: compose.ResumeOpenCode}); len(parts) != 1 || parts[0].text != "◇" {
		t.Errorf("opencode parts = %#v", parts)
	}
	if got := len(rowBadgeParts(compose.Row{Kind: compose.ResumeClaude})); got != 0 {
		t.Errorf("a bare resumable Claude chat carries no badge, got %d", got)
	}
}

func TestRowMarkerNamesEveryKind(t *testing.T) {
	cases := map[compose.Kind]string{
		compose.LiveClaude: "●", compose.LiveCodex: "●", compose.LiveSplit: "●", compose.LiveOpenCode: "●",
		compose.Booting: "◐", compose.Agent: "⚙",
		compose.ResumeClaude: "↻", compose.ResumeCodex: "↻", compose.ResumeOpenCode: "↻",
		compose.NewClaude: "✦", compose.NewCodex: "✦", compose.NewOpenCode: "✦",
		compose.ProfessorUpdate: "⬆", compose.ProfessorUpdateFailed: "·",
	}
	for kind, want := range cases {
		if got := rowMarker(kind); got != want {
			t.Errorf("rowMarker(%v) = %q, want %q", kind, got, want)
		}
	}
}
