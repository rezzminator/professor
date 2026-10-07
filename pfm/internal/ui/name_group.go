package ui

import (
	"strings"
	"unicode"

	"github.com/rezzminator/professor/pfm/internal/compose"
)

type nameGroup struct {
	name  string
	count int
}

// nameGroupPrefix reads a GROUP:NAME declaration off a chat name.
//
// The shape is exact on purpose: a non-empty prefix with NO whitespace in it,
// a colon, and a non-empty remainder that does not start with whitespace.
// "P:BUILDER" declares a group; "fix: the bug" is a sentence with a colon in
// it and declares nothing, and neither does "wave 3: rework".
//
// The strictness became load-bearing when a single member started opening a
// panel. Under the old two-member threshold a prose colon was mostly harmless
// — it took two of them to invent a group — so the rule could afford to be
// loose. It cannot now: every stray colon would become a header.
func nameGroupPrefix(name string) (string, bool) {
	prefix, rest, found := strings.Cut(cleanField(name), ":")
	if !found || prefix == "" || rest == "" {
		return "", false
	}
	if strings.ContainsFunc(prefix, unicode.IsSpace) {
		return "", false
	}
	if unicode.IsSpace(rune(rest[0])) {
		return "", false
	}
	return prefix, true
}

// isNameGroupRow admits every kind a GROUP:NAME can fold into a panel: live,
// Agent, Booting, and every resumable kind — a resumable SOLO:BUILD groups
// with its live namesakes exactly like a live row would.
func isNameGroupRow(kind compose.Kind) bool {
	return kind.IsAddressable() ||
		kind == compose.ResumeClaude || kind == compose.ResumeCodex || kind == compose.ResumeOpenCode
}
