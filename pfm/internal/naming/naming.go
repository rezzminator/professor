package naming

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

// KillPrefix is the label prefix that kills a chat from the default listing.
// Renaming a chat to "_KILL worker 3" is a kill anyone can write from inside
// the chat itself, with no store row and no picker keystroke — and renaming it
// back is the unkill. The match is case-insensitive so "_kill" works too.
const (
	KillPrefix = "_KILL"
	// legacy label prefix, read-compat only.
	legacyKillPrefix = "_HIDE"
)

// LabelKilled reports whether a display name carries KillPrefix.
func LabelKilled(name string) bool {
	for _, prefix := range []string{KillPrefix, legacyKillPrefix} {
		if len(name) >= len(prefix) && strings.EqualFold(name[:len(prefix)], prefix) {
			return true
		}
	}
	return false
}

// CodexThread is the naming-relevant half of one Codex rollout row. The
// package takes this instead of store.Rollout so that store — which must
// answer the same "is this label killed" question about a lineage before it
// spends a candidate slot on it — can depend on naming rather than the
// reverse.
type CodexThread struct {
	ID           string
	SessionID    string
	ParentThread string
	Path         string
	FirstPrompt  string
}

// CodexNameIndex builds the two lookups a live Codex pane can be named
// through: by the rollout file it holds, and by the thread id it was
// identified by. A resumed or paginated session writes no rollout file and has
// only the second, so the id index also carries every thread the Codex session
// index names but no rollout row covers.
func CodexNameIndex(
	threads []CodexThread,
	cxNames map[string]string,
) (byPath, byID map[string]string) {
	byPath = make(map[string]string, len(threads))
	byID = make(map[string]string, len(threads)+len(cxNames))
	for _, thread := range threads {
		name := CxName(
			thread.ID,
			thread.SessionID,
			thread.ParentThread,
			cxNames,
			thread.FirstPrompt,
		)
		byPath[filepath.Clean(thread.Path)] = name
		if name != "" {
			byID[thread.ID] = name
		}
	}
	for id := range cxNames {
		if byID[id] != "" {
			continue
		}
		if name := CxName(id, "", "", cxNames, ""); name != "" {
			byID[id] = name
		}
	}
	return byPath, byID
}

// DisplayName applies transcript naming precedence. The caller supplies the
// last custom title, last AI title, and first real prompt found by the indexer.
func DisplayName(customTitle, aiTitle, firstPrompt string) string {
	if customTitle != "" {
		return customTitle
	}
	if aiTitle != "" {
		return aiTitle
	}
	return firstPrompt
}

// Unnamed is the name a live row gets when every naming source is empty. It
// is a sentinel, not a name: a renderer shows it, but nothing may address a
// chat by it — two unnamed chats are not one chat.
const Unnamed = "(unnamed)"

// LiveFallback applies the live-row naming chain. Claude sockets own a
// generated tmux session name, so their meaningful live fallback is the pane
// title; other socket types use their tmux session name.
func LiveFallback(
	indexed string,
	paneTitle string,
	sessionName string,
	lastPrompt string,
	isCCSock bool,
) string {
	if indexed != "" {
		return indexed
	}
	if isCCSock {
		if paneTitle != "" && paneTitle != "Claude Code" {
			return paneTitle
		}
	} else if sessionName != "" {
		return sessionName
	}
	if lastPrompt != "" {
		return lastPrompt
	}
	return Unnamed
}

// IsJunkPrompt reports whether prompt starts with one of the injected-record
// prefixes matched by ^(<[a-z]|Caveat:|\[Request).
func IsJunkPrompt(prompt string) bool {
	if strings.HasPrefix(prompt, "Caveat:") || strings.HasPrefix(prompt, "[Request") {
		return true
	}
	return len(prompt) >= 2 &&
		prompt[0] == '<' &&
		prompt[1] >= 'a' &&
		prompt[1] <= 'z'
}

// FlattenPromptText extracts a user message's content. JSON strings become
// text directly; arrays contribute only top-level blocks whose type is text.
// Tabs and newlines are collapsed to one space, matching the transcript
// metadata records consumed by the rest of pfm.
func FlattenPromptText(content json.RawMessage) string {
	var text string
	if err := json.Unmarshal(content, &text); err == nil {
		return squashTabsAndNewlines(text)
	}

	var rawBlocks []json.RawMessage
	if err := json.Unmarshal(content, &rawBlocks); err != nil {
		return ""
	}

	type textBlock struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	texts := make([]string, 0, len(rawBlocks))
	for _, rawBlock := range rawBlocks {
		var block textBlock
		if err := json.Unmarshal(rawBlock, &block); err != nil {
			continue
		}
		// "text" is the Claude transcript block; "input_text" is the Codex
		// response_item user block — the only record current Codex writes a
		// submitted prompt into.
		if block.Type != "text" && block.Type != "input_text" {
			continue
		}
		texts = append(texts, block.Text)
	}
	return squashTabsAndNewlines(strings.Join(texts, " "))
}

// CxName follows Codex thread lineage before falling back to the first user
// message. It is the only naming function that clips its result.
func CxName(
	ownID string,
	sessionID string,
	parentThread string,
	names map[string]string,
	firstUserMessage string,
) string {
	for _, id := range []string{ownID, sessionID, parentThread} {
		if id == "" {
			continue
		}
		if name := names[id]; name != "" {
			return clipRunes(squashTabsAndNewlines(name), 60)
		}
	}
	return clipRunes(squashTabsAndNewlines(firstUserMessage), 60)
}

// CodexRowName is the name one Codex conversation is listed under, given its
// newest lineage member and the lineage root.
//
// The newest member's own one-hop walk (own → session → parent) wins first: a
// rename ON the live conversation must outrank an older name upstream of it.
// Only when THAT walk finds nothing does the true lineage root get a look — a
// ≥3-hop chain named only at its root sits beyond the one-hop walk's reach
// (newest's own SessionID reaches only its immediate parent, never further),
// so without this fallback such a chain fell all the way to the newest
// member's first prompt. The first call passes "" instead of firstPrompt so an
// empty result unambiguously means "nothing named", not "fell back to the
// prompt already" — that fallback belongs to the SECOND call only.
//
// compose (the row it displays) and store (the candidate it spends a cached
// first-frame slot on) must agree about this name to the byte, so both call
// this one function rather than each walking the lineage themselves.
func CodexRowName(
	newestID, sessionID, parentThread, rootID, firstPrompt string,
	names map[string]string,
) string {
	name := CxName(newestID, sessionID, parentThread, names, "")
	if name == "" {
		name = CxName(rootID, "", "", names, firstPrompt)
	}
	return name
}

func clipRunes(value string, limit int) string {
	count := 0
	for index := range value {
		if count == limit {
			return value[:index]
		}
		count++
	}
	return value
}

func squashTabsAndNewlines(value string) string {
	first := strings.IndexAny(value, "\t\n")
	if first < 0 {
		return value
	}

	var flattened strings.Builder
	flattened.Grow(len(value))
	flattened.WriteString(value[:first])
	inRun := false
	for index := first; index < len(value); index++ {
		switch value[index] {
		case '\t', '\n':
			if !inRun {
				flattened.WriteByte(' ')
				inRun = true
			}
		default:
			flattened.WriteByte(value[index])
			inRun = false
		}
	}
	return flattened.String()
}
