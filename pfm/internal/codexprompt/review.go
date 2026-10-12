package codexprompt

import (
	"slices"
	"strings"
)

// Claude's /code-review is a BUILT-IN skill, so swapCommands — which only
// knows the command roster it discovered — passes it through untouched. A
// Codex seat that reads the untouched spelling has no such command and falls
// through to Codex's own /review, which reviews the whole branch diff against
// the base branch at the seat's own tier: one flight paid 103M tokens over 38
// such threads where a review scoped to the task's own files costs ~2.6M. So
// every door that hands Claude-authored prompt text to Codex rewrites the
// invocation into the shell command below.
//
// The scope lives in the PROMPT because codex-cli (0.154.0) refuses
// --uncommitted, --base and --commit alongside a prompt ("cannot be used with
// '[PROMPT]'") — a scoped review is prompt-only.
const (
	codeReviewCommand = "/code-review"
	// codeReviewDefaultModel is pfm's own review model for its composed prompt.
	codeReviewDefaultModel = "gpt-6.1-sol"
	codeReviewPrompt       = "Review the uncommitted changes only in your task's own files (name them, space " +
		"separated). Ignore every other path. Report correctness bugs only, most severe first."
	// codeReviewSlot is the second source form: written in place of a level, it
	// says the caller decides both the scope and the effort at run time. The
	// flight lander is its one caller — it sizes the effort from the diff it is
	// about to review, so a level baked at compile time would review a whole
	// flight at whatever tier the prompt happened to spell.
	codeReviewSlot = "{effort}"
	// codeReviewFlightPrompt is the slot form's scope: the whole flight's diff,
	// which spans every task's files, against codeReviewPrompt's one task.
	codeReviewFlightPrompt = "Review the uncommitted changes only in the flight's files (name them, space " +
		"separated). Ignore every other path. Report correctness bugs only, most severe first."
	// codeReviewBareEffort is what a bare /code-review — no level written
	// after it — runs at: the cheapest, which is what the fleet's own prompts
	// ask for wherever they do name a level.
	codeReviewBareEffort = "low"
	// codeReviewTopEffort is the highest effort codex-cli accepts.
	codeReviewTopEffort = "xhigh"
	// codexMediumEffort and codexHighEffort are the two codex-cli efforts
	// between the bare and the top one.
	codexMediumEffort = "medium"
	codexHighEffort   = "high"
)

// codeReviewEfforts maps the level written after /code-review onto Codex's
// model_reasoning_effort. Codex has no twin for Claude's two deepest levels —
// max, and ultra's cloud multi-agent review — so both take xhigh, the highest
// effort codex-cli accepts. A level left out of this map would rewrite to the
// CHEAPEST review and leave its word stranded in the sentence, so every level
// the Claude skill accepts has a row here.
var codeReviewEfforts = map[string]string{
	codeReviewBareEffort: codeReviewBareEffort,
	codexMediumEffort:    codexMediumEffort,
	codexHighEffort:      codexHighEffort,
	codeReviewTopEffort:  codeReviewTopEffort,
	"max":                codeReviewTopEffort,
	"ultra":              codeReviewTopEffort,
}

// codeReviewFlags is the closed set of flags the Claude skill accepts after
// its level. None has a `codex review` twin, and a leftover flag word sitting
// after the prompt argument is an argv codex-cli rejects outright, so a flag
// from this set is consumed with the invocation it belongs to. A word outside
// the set is left in the text where a reader can see it, never silently eaten.
var codeReviewFlags = []string{"--fix", "--comment", "--post", "--no-post"}

// RewriteCodeReview replaces every /code-review invocation in text with the
// Codex shell review command, leaving the prose around it exactly as it was.
// The model is Professor's review model, independent of engine-sync's
// neutral model defaults.
func RewriteCodeReview(text string) string {
	if !strings.Contains(text, codeReviewCommand) {
		return text
	}
	var out strings.Builder
	for index := 0; index < len(text); {
		width, effort, ok := codeReviewInvocationAt(text, index)
		if !ok {
			out.WriteByte(text[index])
			index++
			continue
		}
		out.WriteString(codexReviewShellCommand(codeReviewDefaultModel, effort))
		index += width
	}
	return out.String()
}

// codeReviewInvocationAt reports whether a /code-review invocation starts at
// index, how wide it is (level and trailing flags included) and the Codex
// effort it maps to. The boundary law is swapCommands': a command never
// follows a word character, a path separator or a dash, and is never followed
// by one — so /code-review-x is text, and so is a path or URL that merely ends
// in code-review.
func codeReviewInvocationAt(text string, index int) (int, string, bool) {
	if !strings.HasPrefix(text[index:], codeReviewCommand) {
		return 0, "", false
	}
	if index > 0 && isCommandPrefixChar(text[index-1]) {
		return 0, "", false
	}
	width := len(codeReviewCommand)
	if index+width < len(text) && isCommandSuffixChar(text[index+width]) {
		return 0, "", false
	}
	effort := codeReviewBareEffort
	if strings.HasPrefix(text[index+width:], " "+codeReviewSlot) {
		// The slot is not a word codeReviewWord can read — it opens with a
		// brace — and it is not a level, so it never enters codeReviewEfforts.
		effort = codeReviewSlot
		width += 1 + len(codeReviewSlot)
	} else if level, found := codeReviewWord(text[index+width:]); found {
		if mapped, known := codeReviewEfforts[level]; known {
			effort = mapped
			width += 1 + len(level)
		}
	}
	for {
		flag, found := codeReviewWord(text[index+width:])
		if !found || !slices.Contains(codeReviewFlags, flag) {
			break
		}
		width += 1 + len(flag)
	}
	return width, effort, true
}

// codeReviewWord returns the whole word that follows a single space — the
// level or a flag. It is the WHOLE word, never a prefix of one: "lowish" must
// not read as the level "low" and leave "ish" behind in the sentence.
func codeReviewWord(rest string) (string, bool) {
	if !strings.HasPrefix(rest, " ") {
		return "", false
	}
	word := rest[1:]
	end := 0
	for end < len(word) && (isASCIIWord(word[end]) || word[end] == '-') {
		end++
	}
	if end == 0 {
		return "", false
	}
	return word[:end], true
}

// codexReviewShellCommand is the one spelling of the replacement: a
// prompt-only `codex review` pinned to the review model and effort. The effort
// carries the scope: the slot form emits the flight-scoped prompt and keeps the
// slot in model_reasoning_effort, so the reader fills both at run time.
func codexReviewShellCommand(model, effort string) string {
	prompt := codeReviewPrompt
	if effort == codeReviewSlot {
		prompt = codeReviewFlightPrompt
	}
	return `codex review -c model="` + model + `" -c review_model="` + model +
		`" -c model_reasoning_effort="` + effort + `" "` + prompt + `"`
}

func isCommandPrefixChar(value byte) bool {
	return isASCIIWord(value) || value == '/' || value == '.' || value == '-'
}

// isCommandSuffixChar: a slash command is followed by whitespace, end-of-line
// or punctuation — never by another "/", so a path that merely starts with a
// command name is never rewritten.
func isCommandSuffixChar(value byte) bool {
	return isASCIIWord(value) || value == ':' || value == '-' || value == '/'
}

func isASCIIWord(value byte) bool {
	return value == '_' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
}
