package codexgen

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
	// codeReviewModelAlias is the Claude tier whose Codex model the review
	// runs on. The model itself is read from the tier map, never spelled a
	// second time here.
	codeReviewModelAlias = "opus"
	codeReviewPrompt     = "Review the uncommitted changes only in your task's own files (name them, space " +
		"separated). Ignore every other path. Report correctness bugs only, most severe first."
	// codeReviewSlot is the second source form: written in place of a level, it
	// says the caller decides both the scope and the effort at run time. The
	// flight gater is its one caller — it sizes the effort from the diff it is
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
)

// codeReviewEfforts maps the level written after /code-review onto Codex's
// model_reasoning_effort. Codex has no twin for Claude's two deepest levels —
// max, and ultra's cloud multi-agent review — so both take xhigh, the highest
// effort codex-cli accepts. A level left out of this map would rewrite to the
// CHEAPEST review and leave its word stranded in the sentence, so every level
// the Claude skill accepts has a row here.
var codeReviewEfforts = map[string]string{
	codeReviewBareEffort: codeReviewBareEffort,
	"medium":             "medium",
	"high":               "high",
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

// rewriteCodeReview replaces every /code-review invocation in text with the
// Codex shell review command, leaving the prose around it exactly as it was.
// modelMap is the compiler's tier map; a caller with no config of its own (the
// fleet prompt, the global-role compiler) passes nil and gets the same model a
// project build emits.
func rewriteCodeReview(text string, modelMap map[string]string) string {
	if !strings.Contains(text, codeReviewCommand) {
		return text
	}
	model := codeReviewModel(modelMap)
	var out strings.Builder
	for index := 0; index < len(text); {
		width, effort, ok := codeReviewInvocationAt(text, index)
		if !ok {
			out.WriteByte(text[index])
			index++
			continue
		}
		out.WriteString(codexReviewShellCommand(model, effort))
		index += width
	}
	return out.String()
}

// rewriteCodeReviewFrontmatter applies the rewrite to a command's frontmatter,
// which is re-emitted as YAML rather than as prose. The replacement carries
// double quotes, so a value written as a QUOTED scalar is re-emitted
// single-quoted, with each inner apostrophe doubled: dropping raw quotes into a
// quoted scalar would leave frontmatter Codex cannot parse, which takes the
// whole command down instead of cheapening one review. A plain scalar and the
// indented lines of a block scalar take the text as it is — neither gives a
// quote any meaning, and the replacement carries no ": " or " #" either.
func rewriteCodeReviewFrontmatter(text string, modelMap map[string]string) string {
	if !strings.Contains(text, codeReviewCommand) {
		return text
	}
	lines := strings.Split(text, "\n")
	for index, line := range lines {
		if !strings.Contains(line, codeReviewCommand) {
			continue
		}
		match := frontmatterField.FindStringSubmatch(line)
		if len(match) != 3 || !isQuotedYAMLScalar(match[2]) {
			lines[index] = rewriteCodeReview(line, modelMap)
			continue
		}
		value, err := unquoteFrontmatterScalar(match[2])
		if err != nil {
			// Unreachable through the compilers: parseFrontmatter decodes the
			// same scalar first and fails the file. If it ever is reached, the
			// line stays exactly as it was — re-emitting text this could not
			// decode would produce YAML nobody can read back.
			continue
		}
		lines[index] = match[1] + ": " + singleQuotedYAMLScalar(rewriteCodeReview(value, modelMap))
	}
	return strings.Join(lines, "\n")
}

func isQuotedYAMLScalar(value string) bool {
	return len(value) >= 2 && value[0] == value[len(value)-1] && (value[0] == '"' || value[0] == '\'')
}

func singleQuotedYAMLScalar(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
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

// codeReviewModel is the Codex model the review runs on: the tier map's entry
// for the review alias. A map that does not carry the alias falls back to the
// compiler's own defaults rather than emitting an empty model, which codex-cli
// would reject at parse time.
func codeReviewModel(modelMap map[string]string) string {
	if model := strings.TrimSpace(modelMap[codeReviewModelAlias]); model != "" {
		return model
	}
	return defaultConfig().ModelMap[codeReviewModelAlias]
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
