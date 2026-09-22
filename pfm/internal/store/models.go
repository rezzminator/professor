package store

import "github.com/rezzminator/professor/pfm/internal/engine"

// OpenCodeSession is one indexed OpenCode session, read from OpenCode's own SQLite
// store (opencode.db). It is the OpenCode twin of Transcript/Rollout: every
// field is derived data, rebuildable by re-reading that store.
type OpenCodeSession struct {
	ID    string
	Title string
	// Directory is the session's working directory; ProjectDir the project
	// worktree it belongs to. They differ for a session started in a
	// subdirectory.
	Directory  string
	ProjectDir string
	// ParentID is non-empty for subagent sessions — children spawned by an
	// agent turn. They never earn picker rows of their own.
	ParentID    string
	Agent       string
	Model       string
	FirstPrompt string
	PromptCount int64
	// AssistantCount is the number of assistant messages the session holds.
	// PromptCount alone cannot tell an answered session from one the user
	// opened and never got a reply in — a session with prompts but zero
	// assistant messages is exactly as empty as a Claude transcript with no
	// visible turns, and the picker's suppression law treats it that way.
	AssistantCount int64
	TokensInput    int64
	TokensOutput   int64
	// CostMillicents keeps the session's cumulative cost in integer
	// millicents — SQLite has no decimal and float money drifts.
	CostMillicents int64
	TimeCreatedMS  int64
	TimeUpdatedMS  int64
	// TimeArchivedMS is nonzero when the user archived the session inside
	// OpenCode; archived sessions stay out of the default listing.
	TimeArchivedMS int64
}

// Transcript is one indexed Claude transcript.
type Transcript struct {
	UUID         string
	Path         string
	Size         int64
	MTimeNS      int64
	ActivityNS   int64
	ParsedOffset int64
	CWD          string
	CustomTitle  string
	AITitle      string
	FirstPrompt  string
	LastPrompt   string
	PromptCount  int64
	IsBG         bool
	// ContinuedIn is the session Claude Code moved this conversation into —
	// the `continued-in` record it appends when a chat is sent to the
	// background and resumes under a new session id. It is the raw recorded
	// value and round-trips through the indexer untouched.
	ContinuedIn string
	// Superseded is read-only and derived by the store: ContinuedIn names a
	// transcript that is itself indexed, so this one is an earlier segment of
	// that chat, not a chat of its own. A successor that was never indexed
	// leaves the predecessor standing, so no conversation drops out of view.
	Superseded bool
}

// EffectiveActivityNS is the latest meaningful user-prompt timestamp. Older
// rows and timestamp-less legacy fixtures fall back to the physical file time.
func (transcript Transcript) EffectiveActivityNS() int64 {
	if transcript.ActivityNS > 0 {
		return transcript.ActivityNS
	}
	return transcript.MTimeNS
}

// Rollout is one indexed Codex rollout.
type Rollout struct {
	ID           string
	Path         string
	Size         int64
	MTimeNS      int64
	ParsedOffset int64
	CWD          string
	UserThread   bool
	SessionID    string
	ParentThread string
	LineageRoot  string
	FirstPrompt  string
	PromptCount  int64
	// IsBG marks machine work — a workflow lane or agent runner started this
	// conversation, not a person. It is the Codex twin of Transcript.IsBG and
	// keeps such rows out of the default listing.
	IsBG bool
}

// CxName mirrors one Codex thread's name and where pfm learned it.
type CxName struct {
	ID         string
	ThreadName string
	// Source records which half of Codex's own bookkeeping supplied
	// ThreadName: the state store's threads.name (CxNameSourceStore) or a
	// session_index.jsonl rename record (CxNameSourceSessionIndex).
	Source string
	// RenamedAt is the epoch-nanosecond time session_index.jsonl recorded for
	// this rename, or 0 when no rename time is known. That covers every store
	// name — the state store keeps no rename clock at all — and a
	// session_index entry written before Codex began stamping updated_at.
	RenamedAt int64
}

// CxNameSourceStore and CxNameSourceSessionIndex are the two CxName.Source
// values. CxNameSourceSessionIndex is also the column default: a name
// written before this provenance existed reads back as an undated
// session_index entry, which is the safer assumption of the two — it can
// never outrank a dated rename, matching how such a row already behaved.
const (
	CxNameSourceStore        = "store"
	CxNameSourceSessionIndex = "session_index"
)

// Killed records a kill for a Claude or Codex chat. A nil prompt baseline is
// permanent; a /clear kill carries the prompt count at that event and lifts
// after the transcript grows.
//
// The row lives in the fleet's shared store, keyed by uuid and nothing else.
type Killed struct {
	ID string
	// Engine is DERIVED at read time, not stored: engine.Claude when the id
	// resolves to an indexed transcript, engine.Codex when it resolves to a rollout or a Codex
	// lineage root, and empty when neither table knows it. An empty engine
	// means "killed whatever the engine" — it never lifts a kill.
	Engine engine.ID
	// KilledAt is the shared row's killed_at, or 0 for a kill that reached only
	// the carrier file, which records no time.
	KilledAt int64
	// BaselinePrompts is the /clear auto-unkill ratchet in at_payload.
	BaselinePrompts *int64
}
