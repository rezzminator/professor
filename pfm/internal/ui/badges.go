package ui

import (
	"fmt"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// badgeKind is how a badge is painted: the deck row paints it as a span, the
// plain twin renders it through rowBadges and strips the colour.
type badgeKind uint8

const (
	badgePlain badgeKind = iota
	badgeWarn
	badgeDim
)

// badgePart is one badge of a row, before any colour is applied.
type badgePart struct {
	text string
	kind badgeKind
}

// rowMarker is the kind glyph shared by the deck row and the plain twin.
func rowMarker(kind compose.Kind) string {
	switch kind {
	case compose.LiveClaude, compose.LiveCodex, compose.LiveOpenCode, compose.LiveSplit:
		return "●"
	case compose.Booting:
		return "◐"
	case compose.Agent:
		return "⚙"
	case compose.ResumeClaude, compose.ResumeCodex, compose.ResumeOpenCode:
		return "↻"
	case compose.NewClaude, compose.NewCodex, compose.NewOpenCode:
		return "✦"
	case compose.ProfessorUpdate:
		return "⬆"
	default:
		return "·"
	}
}

// rowBadgeParts is the one place that decides which badges a row carries — the
// K3 rule for it. The deck row and the plain twin both read it.
func rowBadgeParts(row compose.Row) []badgePart {
	parts := make([]badgePart, 0, 7)
	switch row.Kind {
	case compose.LiveCodex, compose.ResumeCodex, compose.NewCodex:
		parts = append(parts, badgePart{text: "⬢"})
	case compose.LiveOpenCode, compose.ResumeOpenCode, compose.NewOpenCode:
		parts = append(parts, badgePart{text: "◇"})
	case compose.Agent:
		parts = append(parts, badgePart{text: "⚙ agent"})
	}
	if row.ServerCount > 1 {
		parts = append(parts, badgePart{text: fmt.Sprintf("⚠%dsrv", row.ServerCount), kind: badgeWarn})
	}
	if row.SplitCount > 1 {
		parts = append(parts, badgePart{text: fmt.Sprintf("⊞%d", row.SplitCount)})
	}
	switch {
	case row.LaunchUnread:
		parts = append(parts, badgePart{text: "⚠", kind: badgeWarn})
	case len(row.Accounts) != 0:
		for _, account := range row.Accounts {
			parts = append(parts, badgePart{text: accountMedal(account)})
		}
	case row.Account != 0:
		if compose.EngineForKind(row.Kind) == pfmengine.Codex {
			parts = append(parts, badgePart{text: codexAccountMedal(row.Account)})
		} else {
			parts = append(parts, badgePart{text: accountMedal(row.Account)})
		}
	}
	if row.C1H && !row.LaunchUnread {
		parts = append(parts, badgePart{text: "⚡"})
	}
	if row.Attached {
		parts = append(parts, badgePart{text: "⇄"})
	}
	if row.Here {
		parts = append(parts, badgePart{text: "←here"})
	}
	if row.Killed {
		parts = append(parts, badgePart{text: "·hidden", kind: badgeDim})
	}
	return parts
}

// rowBadges is the badge string the plain twin prints: parts joined by a space,
// warn and dim parts coloured the way the old row painted them.
func (model Model) rowBadges(row compose.Row) string {
	parts := rowBadgeParts(row)
	rendered := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part.kind {
		case badgeWarn:
			rendered = append(rendered, warnStyle.Render(part.text))
		case badgeDim:
			rendered = append(rendered, dimStyle.Render(part.text))
		default:
			rendered = append(rendered, part.text)
		}
	}
	return strings.Join(rendered, " ")
}
