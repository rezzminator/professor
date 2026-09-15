package inject

import (
	"regexp"
	"strings"
	"unicode"
)

var (
	claudeMenuPattern = regexp.MustCompile(`❯[[:space:]]*[0-9]+\.[[:space:]]`)
	codexMenuPattern  = regexp.MustCompile(`›[[:space:]]*[0-9]+\.[[:space:]]`)
	numberedOption    = regexp.MustCompile(`^[[:space:]]*›?[[:space:]]*[0-9]+\.[[:space:]]`)
	busyPattern       = regexp.MustCompile(`(?i)esc to interrupt|\([0-9]+s ·|· [0-9]+s|[0-9]+ tokens`)
	// The receipt Claude Code prints once a compaction has actually happened.
	// It is the only positive evidence a pane carries that the turn a --then
	// waiter was sent to ride out was a compaction AND that it finished.
	// NAMED GAP: the Codex receipt spelling is unconfirmed, so a Codex
	// compaction falls back to the turn-boundary path in waitForSettledTurn
	// rather than being silently treated as proven.
	compactReceipt    = regexp.MustCompile(`(?i)compacted \(|(?:context|conversation) compacted`)
	menuHintPattern   = regexp.MustCompile(`(?i)enter to (confirm|continue|select)|esc to (cancel|go back)`)
	ansiPattern       = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	oscPattern        = regexp.MustCompile("\x1b\\][^\x07]*(\x07|\x1b\\\\)")
	claudeAgentRow    = regexp.MustCompile(`^❯[[:space:]]+●[[:space:]]+[^[:space:]]+[[:space:]]{2,}[^[:space:]]`)
	compactPattern    = regexp.MustCompile(`^[[:space:]]*/compact([[:space:]]|$)`)
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

// IsBusy mirrors chat.sh's live spinner-detail test.
func IsBusy(capture string) bool {
	return busyPattern.MatchString(capture)
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
	for _, character := range rest {
		// A genuine non-ASCII draft is still text. Exclude whitespace and
		// format controls so contextual zero-width placeholders stay empty.
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

func isDimPlaceholder(styledLine string) bool {
	if !strings.Contains(styledLine, "\x1b[2m") {
		return false
	}
	// chat.sh treats the whole dim SGR-2 span as contextual placeholder text.
	withoutDim := styledLine
	for {
		start := strings.Index(withoutDim, "\x1b[2m")
		if start < 0 {
			break
		}
		rest := withoutDim[start+len("\x1b[2m"):]
		end := strings.Index(rest, "\x1b[")
		if end < 0 {
			withoutDim = withoutDim[:start]
			break
		}
		withoutDim = withoutDim[:start] + rest[end:]
	}
	withoutDim = stripTerminalControl(withoutDim)
	return !hasDraft(withoutDim)
}

func lastComposerLine(capture string) string {
	last := ""
	for _, line := range strings.Split(capture, "\n") {
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
			last = line
			continue
		}
		if strings.HasPrefix(visible, "›") {
			last = line
		}
	}
	return last
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
func deliveryProven(before, after, message string, queued, fileBacked bool) bool {
	pastComposer := withoutLastComposerLine(after)
	if messageVisible(pastComposer, message) ||
		(fileBacked && HasPastePlaceholder(pastComposer)) {
		return true
	}
	if queued {
		return queueProofPattern.MatchString(after) &&
			!queueProofPattern.MatchString(before)
	}
	return !IsBusy(before) && IsBusy(after)
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
