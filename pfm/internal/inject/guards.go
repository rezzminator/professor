package inject

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

var (
	claudeMenuPattern = regexp.MustCompile(`❯[\s\v]*\d+\.[\s\v]`)
	codexMenuPattern  = regexp.MustCompile(`›[\s\v]*\d+\.[\s\v]`)
	numberedOption    = regexp.MustCompile(`^[\s\v]*›?[\s\v]*\d+\.[\s\v]`)
	busyPattern       = regexp.MustCompile(`(?i)esc to interrupt|\(\d+s ·|· \d+s|\d+ tokens`)
	// claudeBusyPattern is a running Claude turn, read only from the shapes the
	// engine itself draws: "esc to interrupt"; the spinner's parenthesised
	// timer, minutes included ("(12s ·", "(1m 50s ·"); a column-0 spinner glyph
	// with a verb still running ("✢ Tempering…"); and an ellipsis followed by
	// elapsed time ("  Spawning … · 51s"). busyPattern's bare `\d+ tokens` and
	// `· \d+s` arms are left out on purpose: transcript prose and the rows of
	// background agents behind an idle input box say exactly that. The finished
	// row ("✻ Worked for 12s") and the idle agent-wait row ("✻ Waiting for 1
	// background agent to finish") match none of it.
	claudeBusyPattern = regexp.MustCompile(
		`(?im)esc to interrupt|\((?:\d+[hm] )*\d+s ·|^[✻✢✶✳✽·*] +\S[^\n]*…|…\s*·\s*(?:\d+[hm] )*\d+s`,
	)
	// exitDialogPattern is the selected row of Claude Code's background-work
	// exit confirmation ("❯ 1. Exit and stop tasks"). The marker has to sit on
	// the Exit row: a human who moved it to "Stay" gets that choice respected.
	exitDialogPattern = regexp.MustCompile(`❯[\s\v]*\d+\.[\s\v]*Exit`)
	// The receipt Claude Code prints once a compaction has actually happened.
	// It is the only positive evidence a pane carries that the turn a --then
	// waiter was sent to ride out was a compaction AND that it finished.
	// NAMED GAP: the Codex receipt spelling is unconfirmed, so a Codex
	// compaction falls back to the turn-boundary path in waitForSettledTurn
	// rather than being silently treated as proven.
	compactReceipt  = regexp.MustCompile(`(?i)compacted \(|(?:context|conversation) compacted`)
	menuHintPattern = regexp.MustCompile(`(?i)enter to (confirm|continue|select)|esc to (cancel|go back)`)
	ansiPattern     = regexp.MustCompile(`\x1b\[[0-?]*[\x20-\x2f]*[\x40-\x7e]`)
	oscPattern      = regexp.MustCompile("\x1b\\][^\x07]*(\x07|\x1b\\\\)")
	// A row of Claude's agents panel under the cursor: a status bullet, then the
	// bare main row, or a name (a count such as "(+3)" may follow) and a
	// two-space gap before its status.
	claudeAgentRow = regexp.MustCompile(
		`^❯[[:space:]]+[●⏺◯○][[:space:]]+(main[[:space:]]*$|[^[:space:]].*?[^[:space:]][[:space:]]{2,}[^[:space:]])`,
	)
	compactPattern    = regexp.MustCompile(`^[\s\v]*/compact([\s\v]|$)`)
	queueProofPattern = regexp.MustCompile(
		`(?i)press up to edit queued messages|queued messages?|pending messages?|message (will be|was) (queued|submitted)|submitted after (the )?next tool call`,
	)
)

// isCompactCommand mirrors chat.sh's `grep -qE '^[[:space:]]*/compact([[:space:]]|$)'`
// test, used both for the primary message and for every then steer.
func isCompactCommand(message string) bool {
	return compactPattern.MatchString(message)
}

func isHarnessCommand(message string) bool {
	return strings.HasPrefix(strings.TrimLeftFunc(message, unicode.IsSpace), "/")
}

// ExitDialogOpen reports Claude Code's background-work confirmation on
// screen with its Exit row selected — the dialog /exit opens over a running
// background shell, scheduled task or sub-agent, where Enter confirms the
// exit. Both closers of a chat answer it: reload's worker and the kill-exit
// finisher.
func ExitDialogOpen(capture string) bool {
	return exitDialogPattern.MatchString(capture)
}

// IsBusy mirrors chat.sh's live spinner-detail test.
//
// It is the CLAUDE/CODEX rule and only that. Every caller that knows which
// engine it is looking at calls IsBusyFor instead; this stays for the callers
// that genuinely do not (a raw pane whose engine never resolved), where the
// historical rule is still the least wrong answer.
func IsBusy(capture string) bool {
	return busyPattern.MatchString(capture)
}

// openCodeBusyFooterLines is how much of the tail the OpenCode busy test sees.
// The hint lives on the LAST rendered line of the pane; three lines of slack
// covers a wrapped footer without letting a turn that ended long ago keep the
// pane "busy" from scrollback forever.
const openCodeBusyFooterLines = 3

// openCodeBusyHint is the footer OpenCode 1.18.18 renders WHILE a turn runs:
// the keybind label for session_interrupt followed by its hint word. Note the
// missing "to" — `esc interrupt`, not Claude's `esc to interrupt` — which is
// why busyPattern cannot see it.
const openCodeBusyHint = "esc interrupt"

// IsBusyFor is the engine-aware busy test.
//
// OpenCode needs its own rule in BOTH directions. busyPattern's `\d+ tokens`
// arm matches OpenCode's permanently-rendered sidebar token counter, so every
// OpenCode pane — idle or not — reads as busy under the Claude/Codex rule;
// and OpenCode's own running-turn footer says `esc interrupt`, which none of
// busyPattern's arms match. One rule for three engines was wrong in both
// directions at once here.
//
// Claude needs its own rule too. busyPattern's `\d+ tokens` and `· \d+s` arms
// read transcript prose ("wrote 24,768 tokens") and background-agent rows
// under an idle input box as a running turn, while a 2.1.283 spinner past one
// minute ("(1m 50s · ↓ 9.3k tokens)") matches none of its arms. A Claude turn
// is busy only on the shapes the engine itself draws (claudeBusyPattern).
func IsBusyFor(engine pfmengine.ID, capture string) bool {
	if engine == pfmengine.Claude {
		return claudeBusyPattern.MatchString(capture)
	}
	if engine != pfmengine.OpenCode {
		return IsBusy(capture)
	}
	tail := lastNonEmptyLines(capture, openCodeBusyFooterLines)
	for _, line := range strings.Split(tail, "\n") {
		if strings.Contains(stripTerminalControl(line), openCodeBusyHint) {
			return true
		}
	}
	return false
}

// SelectorLine returns the selected numbered option for a real open menu.
// A lone Codex "› 1. draft" remains a legal composer line.
func SelectorLine(capture string) string {
	line := lastComposerLine(capture)
	if line == "" {
		return ""
	}
	clean := stripTerminalControl(strings.TrimSuffix(line, "\r"))
	clean = strings.TrimSpace(clean)
	if strings.HasPrefix(clean, "❯") {
		if claudeMenuPattern.MatchString(clean) {
			return clean
		}
		return ""
	}
	if !codexMenuPattern.MatchString(clean) {
		return ""
	}
	options := 0
	for _, line := range strings.Split(capture, "\n") {
		if numberedOption.MatchString(line) {
			options++
		}
	}
	if menuHintPattern.MatchString(capture) || options >= 2 {
		return clean
	}
	return ""
}

func hasDraft(line string) bool {
	if line == "" {
		return false
	}
	line = stripTerminalControl(strings.TrimSuffix(line, "\r"))
	rest := strings.TrimSpace(line)
	rest = strings.TrimPrefix(rest, "❯")
	rest = strings.TrimPrefix(rest, "›")
	rest = strings.TrimSpace(rest)
	rest = strings.ReplaceAll(rest, "Press up to edit queued messages", "")
	// A lone ASCII spinner frame is the TUI's idle animation, not a draft.
	if trimmed := strings.TrimSpace(rest); len(trimmed) == 1 && strings.ContainsAny(trimmed, `|/-\`) {
		return false
	}
	for _, character := range rest {
		// A genuine non-ASCII draft is still text. Exclude whitespace and
		// format controls so contextual zero-width placeholders stay empty,
		// and the Braille Patterns block: Codex sparkles its idle composer
		// with those glyphs in 24-bit greys (not SGR dim) under a truecolor
		// terminal, and nobody types braille as a message.
		if character >= 0x2800 && character <= 0x28FF {
			continue
		}
		if unicode.IsGraphic(character) && !unicode.IsSpace(character) {
			return true
		}
	}
	return false
}

// HasPastePlaceholder reports whether value contains the collapsed-paste
// placeholder Claude Code and Codex render in place of a large literal paste
// (e.g. "[Pasted text #3 +72 lines]"). Exported so internal/reload can prove
// a large --then prompt landed even when its own tail text never renders.
func HasPastePlaceholder(value string) bool {
	lower := strings.ToLower(value)
	return strings.Contains(lower, "[pasted text") ||
		strings.Contains(lower, "[pasted content")
}

// ComposerIsDimPlaceholder reports whether the composer in a STYLED capture
// (tmux capture-pane -e) holds only dim hint text — Claude Code's prompt
// suggestion, Codex's placeholder — and so is empty underneath. Exported so
// internal/reload reads a composer the way the inject mash guard does.
func ComposerIsDimPlaceholder(styledCapture string) bool {
	return isDimPlaceholder(lastComposerLine(styledCapture))
}

// LastComposerLine is the active composer row of a capture — the last line
// that STARTS with ❯ or ›, never a status row carrying the glyph mid-line (a
// "› stashed" marker) nor Claude's focused agent-panel row. Exported so
// internal/reload reads the same row the inject guards do.
func LastComposerLine(capture string) string {
	return lastComposerLine(capture)
}

// ComposerDraftText is the active composer's draft, whitespace-normalised;
// empty when the composer is empty or not drawn. Shared by internal/reload and
// the kill finisher, so both read the same draft off the same row.
func ComposerDraftText(capture string) string {
	return strings.Join(strings.Fields(composerDraft(lastComposerLine(capture))), " ")
}

// composerDraft is the text after a composer line's last ❯/› marker.
func composerDraft(line string) string {
	cut := -1
	for index, character := range line {
		if character == '❯' || character == '›' {
			cut = index + utf8.RuneLen(character)
		}
	}
	if cut < 0 {
		return ""
	}
	return line[cut:]
}

// ComposerEmpty reports whether the composer holds no draft — no text after
// its marker, or only dim hint text — and returns the plain draft it read.
// styled is read only for a non-empty draft; a styled capture that fails is an
// error, never an empty composer. A composer not drawn reads empty here: a
// caller that must prove the box is up checks LastComposerLine first.
func ComposerEmpty(capture string, styled func() (string, error)) (bool, string, error) {
	draft := ComposerDraftText(capture)
	if draft == "" {
		return true, "", nil
	}
	styledCapture, err := styled()
	if err != nil {
		return false, draft, fmt.Errorf("read composer styling (draft %q): %w", draft, err)
	}
	return ComposerIsDimPlaceholder(styledCapture), draft, nil
}

func isDimPlaceholder(styledLine string) bool {
	if !strings.Contains(styledLine, "\x1b[2m") && !strings.Contains(styledLine, ";2m") {
		return false
	}
	// Walk the SGR sequences and drop every character painted while dim is
	// on. Dim is an attribute, not a span: it starts with a parameter 2
	// (alone or in a list such as 1;2) and ends only at a full reset (0 or
	// empty) or the explicit 22 — a colour change like 39 in between keeps
	// it. Codex interleaves exactly such colour resets inside its dim
	// placeholder, so a "strip to the next escape" reading kept the hint
	// text and called it a draft.
	var visible strings.Builder
	dim := false
	rest := styledLine
	for rest != "" {
		match := ansiPattern.FindStringIndex(rest)
		if match == nil {
			if !dim {
				visible.WriteString(rest)
			}
			break
		}
		if !dim {
			visible.WriteString(rest[:match[0]])
		}
		sequence := rest[match[0]:match[1]]
		if strings.HasSuffix(sequence, "m") {
			params := strings.TrimSuffix(strings.TrimPrefix(sequence, "\x1b["), "m")
			if params == "" {
				dim = false
			}
			fields := strings.Split(params, ";")
			for index := 0; index < len(fields); index++ {
				switch fields[index] {
				case "0", "22":
					dim = false
				case "2":
					dim = true
				case "38", "48", "58":
					// Extended colour: "5;N" or "2;R;G;B" are its arguments,
					// never attributes — the 2 of truecolour is not dim.
					if index+1 < len(fields) && fields[index+1] == "5" {
						index += 2
					} else if index+1 < len(fields) && fields[index+1] == "2" {
						index += 4
					}
				}
			}
		}
		rest = rest[match[1]:]
	}
	return !hasDraft(stripTerminalControl(visible.String()))
}

func lastComposerLine(capture string) string {
	lines := strings.Split(capture, "\n")
	if row := lastComposerRow(lines); row >= 0 {
		return lines[row]
	}
	return ""
}

// lastComposerRow is the index of lastComposerLine's row in lines, -1 when
// no row starts with a composer marker.
func lastComposerRow(lines []string) int {
	last := -1
	for index, line := range lines {
		clean := stripTerminalControl(strings.TrimSuffix(line, "\r"))
		visible := strings.TrimLeftFunc(clean, unicode.IsSpace)
		if strings.HasPrefix(visible, "❯") {
			// Claude renders live agent activity as a bullet row beneath the
			// composer. Require the complete row shape, including the two-space
			// name/status boundary, so a genuine draft beginning with a bullet
			// is not broadly exempted.
			if claudeAgentRow.MatchString(visible) {
				continue
			}
			last = index
			continue
		}
		if strings.HasPrefix(visible, "›") {
			last = index
		}
	}
	return last
}

// ComposerBoxEmpty reports whether the rows beneath the active composer row,
// down to the input box's closing rule, hold no text: a draft led by a newline
// leaves the marker row empty and its text a row lower, where ComposerEmpty
// never looks. False with no closing rule — the box has no known end, so its
// rows cannot be proven empty — and with no composer row at all. Claude Code
// draws the box; Codex does not. A row there is empty only when it shows no
// visible character at all: hasDraft's marker-row allowances (a lone spinner
// frame, Codex's braille sparkle) are a typed "-" or text on these rows.
func ComposerBoxEmpty(capture string) bool {
	lines := strings.Split(capture, "\n")
	row := lastComposerRow(lines)
	if row < 0 {
		return false
	}
	for _, line := range lines[row+1:] {
		clean := stripTerminalControl(strings.TrimSuffix(line, "\r"))
		if ComposerBoxRule(clean) {
			return true
		}
		if strings.IndexFunc(clean, func(character rune) bool {
			return unicode.IsGraphic(character) && !unicode.IsSpace(character)
		}) >= 0 {
			return false
		}
	}
	return false
}

// ComposerBoxRule reports whether a captured line is one of the input box's
// horizontal rules — visible content that is nothing but box-drawing glyphs.
// Matching the CLASS (U+2500-U+257F) rather than one theme's glyph keeps a
// restyled border from silently reopening the wrap bug.
func ComposerBoxRule(line string) bool {
	drawn := false
	for _, character := range line {
		switch {
		case unicode.IsSpace(character):
		case character >= 0x2500 && character <= 0x257F:
			drawn = true
		default:
			return false
		}
	}
	return drawn
}

func stripTerminalControl(value string) string {
	return oscPattern.ReplaceAllString(ansiPattern.ReplaceAllString(value, ""), "")
}

func lastNonEmptyLines(capture string, limit int) string {
	lines := strings.Split(capture, "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimRightFunc(line, unicode.IsSpace)
		if line != "" {
			filtered = append(filtered, line)
		}
	}
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[len(filtered)-limit:]
	}
	return strings.Join(filtered, "\n")
}

// deliveryProven distinguishes a cleared composer from evidence that the turn
// moved into the transcript/engine. A busy delivery needs queue-specific
// evidence; the spinner was already present before we typed and proves only
// the older turn.
func deliveryProven(engine pfmengine.ID, before, after, message string, queued, fileBacked bool) bool {
	pastComposer := withoutLastComposerLine(after)
	if messageVisible(pastComposer, message) ||
		(fileBacked && HasPastePlaceholder(pastComposer)) {
		return true
	}
	if queued {
		return queueProofPattern.MatchString(after) &&
			!queueProofPattern.MatchString(before)
	}
	return !IsBusyFor(engine, before) && IsBusyFor(engine, after)
}

func proofExpectation(queued bool) string {
	if queued {
		return "queue indicator"
	}
	return "engine processing state"
}

func withoutLastComposerLine(capture string) string {
	composer := lastComposerLine(capture)
	if composer == "" {
		return capture
	}
	lines := strings.Split(capture, "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		if lines[index] == composer {
			lines = append(lines[:index], lines[index+1:]...)
			break
		}
	}
	return strings.Join(lines, "\n")
}

func messageVisible(capture, message string) bool {
	capture = normalizeSpace(capture)
	message = normalizeSpace(message)
	if capture == "" || message == "" {
		return false
	}
	if len([]rune(message)) <= 80 {
		return strings.Contains(capture, message)
	}
	return strings.Contains(capture, headRunes(message, 40)) ||
		strings.Contains(capture, tailRunes(message, 40))
}

// CompactionReceipt reports whether the pane is showing the receipt a finished
// compaction leaves behind. Presence alone proves only that SOME compaction
// ran; waitForSettledTurn is what establishes that it was this turn's.
func CompactionReceipt(capture string) bool {
	return compactReceipt.MatchString(capture)
}

// countCompactionReceipts counts the receipt LINES in a capture — the same
// evidence CompactionReceipt tests for, as a quantity. Presence answers "a
// receipt is on screen", which a stale receipt scrolling back into view
// satisfies just as well as a fresh one; a count taken over the same window
// twice answers "another compaction finished since", which only a receipt
// that was actually printed can make true (waitForSettledTurn).
func countCompactionReceipts(capture string) int {
	count := 0
	for _, line := range strings.Split(capture, "\n") {
		if compactReceipt.MatchString(line) {
			count++
		}
	}
	return count
}
