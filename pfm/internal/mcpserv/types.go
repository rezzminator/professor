// Package mcpserv exposes the pfm chat family over stdio MCP.
package mcpserv

import (
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// LSInput selects the fleet view returned by chat_ls.
type LSInput struct {
	All     bool   `json:"all,omitempty" jsonschema:"include killed and background rows from every repository; the payload limit below still applies (unlike pfm chat ls --all, which lists live chats only)"`
	Killed  bool   `json:"killed,omitempty" jsonschema:"return killed rows only"`
	Project string `json:"project,omitempty" jsonschema:"case-insensitive substring filter on a row's project or directory"`
	Limit   int    `json:"limit,omitempty" jsonschema:"maximum rows returned, default 200 and maximum 1000; total and truncated always report the full match count"`
}

// ChatRow is one structured live or resumable fleet row.
type ChatRow struct {
	Session        string       `json:"session"`
	ID             string       `json:"id"`
	Engine         pfmengine.ID `json:"engine"`
	State          string       `json:"state"`
	Dir            string       `json:"dir"`
	Project        string       `json:"project"`
	Name           string       `json:"name"`
	Account        int          `json:"account,omitempty"`
	Kind           string       `json:"kind"`
	Killed         bool         `json:"killed,omitempty"`
	Socket         string       `json:"socket,omitempty"`
	Pane           string       `json:"pane,omitempty"`
	transcriptPath string
}

// LSOutput is chat_ls's structured response.
type LSOutput struct {
	Rows  []ChatRow `json:"rows"`
	Count int       `json:"count"`
	// Matched is how many rows the view and the project filter selected,
	// before Limit cut the payload down. Count < Matched is the ONLY way a
	// caller can tell a short answer from an empty fleet, so Truncated names
	// it outright rather than leaving it to be inferred from two numbers.
	Matched     int  `json:"matched"`
	Truncated   bool `json:"truncated,omitempty"`
	KilledCount int  `json:"killed_count"`
	// Filter echoes the project filter that was applied, so a caller reading
	// an empty result knows whether it filtered itself down to nothing.
	Filter string `json:"filter,omitempty"`
	// Scope is the repository listed, or why every repository was: "all
	// repos" under all, "all repos — caller cwd unknown" when the caller did
	// not resolve, so an unscoped answer is never silent. Elsewhere counts
	// the rows a repository scope left out.
	Scope     string `json:"scope"`
	Elsewhere int    `json:"elsewhere,omitempty"`
}

// ResolveInput selects one chat.sh resolution namespace.
type ResolveInput struct {
	Kind string `json:"kind" jsonschema:"resolution namespace: label, session, or cxwin"`
	Name string `json:"name" jsonschema:"exact label, session, or Codex window name"`
}

// ResolveOutput mirrors chat.sh return codes 0, 1, and 2.
type ResolveOutput struct {
	Status     string `json:"status"`
	Code       int    `json:"code"`
	SocketPath string `json:"socket_path,omitempty"`
	Pane       string `json:"pane,omitempty"`
	Candidates string `json:"candidates,omitempty"`
}

// InjectInput requests guarded live delivery.
type InjectInput struct {
	Target   string   `json:"target" jsonschema:"live session, Claude label, Codex thread name, self, or tmux pane"`
	Message  string   `json:"message" jsonschema:"message to type and submit"`
	ForceNow bool     `json:"force_now,omitempty" jsonschema:"interrupt a busy target with Escape before delivery"`
	Then     []string `json:"then,omitempty" jsonschema:"follow-up steers delivered by a detached waiter after the primary turn settles to idle; in order, one settled turn apart. No steer may itself start with /compact — /compact itself is refused as a message here"`
}

// InjectOutput is a stable MCP representation of inject.Result.
type InjectOutput struct {
	Status        string `json:"status"`
	Code          int    `json:"code"`
	Message       string `json:"message"`
	SocketPath    string `json:"socket_path,omitempty"`
	Pane          string `json:"pane,omitempty"`
	Proof         string `json:"proof,omitempty"`
	Busy          bool   `json:"busy,omitempty"`
	Interrupted   bool   `json:"interrupted,omitempty"`
	DraftStashed  bool   `json:"draft_stashed,omitempty"`
	Typed         bool   `json:"typed,omitempty"`
	SubmitRetries int    `json:"submit_retries,omitempty"`
	Steers        int    `json:"steers,omitempty"`
	SteerLog      string `json:"steer_log,omitempty"`
	Unsigned      bool   `json:"unsigned,omitempty"`
	AutoFilePath  string `json:"auto_file_path,omitempty"`
	LiteralChunks int    `json:"literal_chunks,omitempty"`
}

// KeysInput requests tmux keypresses for one resolved live chat. Keys are
// key names unless Literal is explicitly true; this mirrors `pfm chat keys`.
type KeysInput struct {
	Target  string   `json:"target" jsonschema:"live session, Claude label, Codex thread name, self, or tmux pane"`
	Keys    []string `json:"keys" jsonschema:"tmux key names to press"`
	Literal bool     `json:"literal,omitempty" jsonschema:"type each key as literal text instead of pressing it"`
	DelayMS int      `json:"delay_ms,omitempty" jsonschema:"pause between keys in milliseconds; default 120"`
	Capture bool     `json:"capture,omitempty" jsonschema:"return the pane after the keys land"`
}

// KeysOutput is the structured receipt for chat_keys.
type KeysOutput struct {
	Status     string   `json:"status"`
	Code       int      `json:"code"`
	SocketPath string   `json:"socket_path,omitempty"`
	Pane       string   `json:"pane,omitempty"`
	Count      int      `json:"count"`
	Keys       []string `json:"keys"`
	Text       string   `json:"text,omitempty"`
}

// CaptureInput requests a pane snapshot of the whole retained scrollback,
// bounded by the caller after the capture.
type CaptureInput struct {
	Target    string `json:"target" jsonschema:"live session, Claude label, Codex thread name, self, or tmux pane"`
	TailLines *int   `json:"tail_lines,omitempty" jsonschema:"return at most this many non-empty lines of the scrollback; default 40"`
	MaxBytes  *int   `json:"max_bytes,omitempty" jsonschema:"maximum returned text bytes, default 262144 and maximum 4194304"`
}

// CaptureOutput is chat_capture's structured response.
type CaptureOutput struct {
	Status     string `json:"status"`
	Code       int    `json:"code"`
	SocketPath string `json:"socket_path,omitempty"`
	Pane       string `json:"pane,omitempty"`
	Text       string `json:"text,omitempty"`
	Message    string `json:"message,omitempty"`
	Bytes      int    `json:"bytes,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
}

// WhoamiInput takes no arguments: Codex identity arrives in the reserved MCP
// request metadata, while stdio callers retain environment/ancestry recovery.
type WhoamiInput struct{}

// WhoamiOutput is chat.sh whoami's answer plus the engine identity behind it.
type WhoamiOutput struct {
	Status     string `json:"status"`
	Session    string `json:"session,omitempty"`
	SocketPath string `json:"socket_path,omitempty"`
	SocketName string `json:"socket_name,omitempty"`
	Pane       string `json:"pane,omitempty"`
	Engine     string `json:"engine,omitempty"`
	ID         string `json:"id,omitempty"`
	Source     string `json:"source,omitempty"`
	Recovered  bool   `json:"recovered,omitempty"`
	Message    string `json:"message,omitempty"`
}

// FindInput searches indexed transcript files.
type FindInput struct {
	Excerpt     string `json:"excerpt" jsonschema:"literal name or prompt excerpt to find; a multi-line excerpt is split into needles and candidates are ranked by how many they hit"`
	Limit       int    `json:"limit,omitempty" jsonschema:"maximum candidates, default 10 and maximum 50"`
	IncludeSelf bool   `json:"include_self,omitempty" jsonschema:"also match the asking session's own transcript, which is excluded by default where the server knows the asking session (self_id then names it)"`
}

// FindCandidate is one confirmed indexed transcript match.
type FindCandidate struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	Engine    string `json:"engine"`
	Name      string `json:"name"`
	Dir       string `json:"dir"`
	Date      string `json:"date"`
	Excerpt   string `json:"excerpt,omitempty"`
	Confirmed bool   `json:"confirmed"`
	Hits      int    `json:"hits"`
}

// FindOutput is chat_find's structured response.
type FindOutput struct {
	Candidates []FindCandidate `json:"candidates"`
	Count      int             `json:"count"`
	Needles    []string        `json:"needles,omitempty"`
	SelfID     string          `json:"self_id,omitempty"`
}

// ReadInput requests bounded recent transcript turns.
type ReadInput struct {
	Source   string `json:"source" jsonschema:"indexed Claude/Codex id or exact indexed path"`
	LastN    int    `json:"last_n,omitempty" jsonschema:"maximum recent turns, default 20 and maximum 200"`
	MaxBytes int    `json:"max_bytes,omitempty" jsonschema:"maximum returned text bytes, default 65536 and maximum 1048576"`
}

// DigestInput requests transcript.py's one-line-per-event digest of one
// transcript. Every optional field maps onto one `transcript.py show` flag.
type DigestInput struct {
	Source     string `json:"source" jsonschema:"chat_find id, Claude or Codex session or agent id (or prefix), transcript path, or a Claude chat's title"`
	Lines      string `json:"lines,omitempty" jsonschema:"transcript line range FROM-TO, FROM- or one line number; the events those records produced"`
	Since      string `json:"since,omitempty" jsonschema:"window start: HH:MM[:SS] UTC, an ISO time, +5m from the transcript start, -15m from its end"`
	Until      string `json:"until,omitempty" jsonschema:"window end, same forms as since"`
	Grep       string `json:"grep,omitempty" jsonschema:"keep events whose full text (input, result, prose) matches this regular expression"`
	IgnoreCase bool   `json:"ignore_case,omitempty" jsonschema:"match grep case-insensitively"`
	Only       string `json:"only,omitempty" jsonschema:"event kinds, comma-separated: prompt, reply, call, note, final; error alone keeps only failed calls"`
	Tool       string `json:"tool,omitempty" jsonschema:"keep only calls of these tool names, comma-separated"`
	Results    string `json:"results,omitempty" jsonschema:"call results shown: brief (default), none, full or tail:N lines"`
	First      int    `json:"first,omitempty" jsonschema:"keep the first N events after every other filter"`
	Last       int    `json:"last,omitempty" jsonschema:"keep the last N events after every other filter"`
	Text       int    `json:"text,omitempty" jsonschema:"characters kept of a prompt or reply, default 500"`
	MaxBytes   int    `json:"max_bytes,omitempty" jsonschema:"maximum returned text bytes, cut at a line end; default 65536 and maximum 1048576"`
}

// DigestOutput is chat_digest's bounded digest text.
type DigestOutput struct {
	Text       string `json:"text"`
	Bytes      int    `json:"bytes"`
	TotalBytes int    `json:"total_bytes"`
	Truncated  bool   `json:"truncated"`
}

// Turn is one visible user, assistant, tool, or summary transcript record.
// A tool call carries the tool's name in Tool and its condensed input as Text
// — the transcript records no prose for it, and a turn with an empty Text
// reads as a chat that said nothing rather than one that called a tool.
type Turn struct {
	Role      string `json:"role"`
	Text      string `json:"text"`
	Tool      string `json:"tool,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
}

// ReadOutput is chat_read's bounded extraction.
type ReadOutput struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	Engine    string `json:"engine"`
	Turns     []Turn `json:"turns"`
	Count     int    `json:"count"`
	Truncated bool   `json:"truncated"`
	Bytes     int    `json:"bytes"`
}

// LastInput selects the newest assistant answer for a chat.
type LastInput struct {
	Target string `json:"target" jsonschema:"chat id, name, session, or tmux target"`
}

type LastOutput struct {
	Target string `json:"target"`
	Text   string `json:"text"`
}

// StatusInput selects one chat for headless status inspection.
type StatusInput struct {
	Target  string `json:"target" jsonschema:"chat id, name, session, or tmux target"`
	Summary bool   `json:"summary,omitempty" jsonschema:"summarize the last human exchange; off by default"`
	Ask     bool   `json:"ask,omitempty" jsonschema:"answer the chat's current status from its live pane capture plus its last exchange; off by default, never cached"`
	Engine  string `json:"engine,omitempty" jsonschema:"summary engine override: claude or codex"`
	Model   string `json:"model,omitempty" jsonschema:"summary model override"`
}

type StatusOutput struct {
	Name          string       `json:"name"`
	State         string       `json:"state"`
	IdleSeconds   int64        `json:"idle_seconds"`
	Engine        pfmengine.ID `json:"engine"`
	Model         string       `json:"model,omitempty"`
	CWD           string       `json:"cwd,omitempty"`
	SessionID     string       `json:"session_id,omitempty"`
	Socket        string       `json:"socket,omitempty"`
	ContextPct    float64      `json:"context_pct,omitempty"`
	Last          string       `json:"last,omitempty"`
	Error         string       `json:"error,omitempty"`
	Summary       string       `json:"summary,omitempty"`
	SummaryCached bool         `json:"summary_cached,omitempty"`
	Ask           string       `json:"ask,omitempty"`
}

// TargetInput is shared by chat actions whose CLI form takes one target.
type TargetInput struct {
	Target string `json:"target" jsonschema:"chat id, name, session, or tmux target"`
}

type NameInput struct {
	Target string `json:"target"`
	Name   string `json:"name"`
}

type NewInput struct {
	Name      string `json:"name,omitempty" jsonschema:"the chat's name in pfm ls and the target chat_inject, chat_status and chat_kill take; inside a workbench, empty takes its next {name}:{n}"`
	Engine    string `json:"engine,omitempty" jsonschema:"cc/claude, cx/codex or ox/opencode; the caller's engine when empty"`
	CWD       string `json:"cwd,omitempty" jsonschema:"working directory, relative to the caller's; the caller's directory when empty"`
	Account   int    `json:"account,omitempty" jsonschema:"account number to launch on; pfm picks when 0"`
	Cache     string `json:"cache,omitempty" jsonschema:"prompt cache for this launch: 1h or 5m"`
	Model     string `json:"model,omitempty" jsonschema:"model alias or full id, e.g. claude-sonnet-5-5; the engine's default when empty"`
	Effort    string `json:"effort,omitempty" jsonschema:"reasoning effort, e.g. high or xhigh; the engine's default when empty"`
	AgentRole string `json:"agentRole,omitempty" jsonschema:"registered agent the chat runs as, e.g. flights-foreman: its role prompt joins the fleet prompt; not on OpenCode"`
	Prompt    string `json:"prompt,omitempty" jsonschema:"first message; the chat opens idle without one"`
	Await     bool   `json:"await,omitempty" jsonschema:"wait for the first answer and return it instead of the launch message"`
	Timeout   *int   `json:"timeout,omitempty" jsonschema:"with await: seconds to wait, 0 waits forever; 600 when unset"`
	Settle    int    `json:"settle,omitempty" jsonschema:"with await: seconds of quiet that end the answer; 3 when unset"`
	Progress  bool   `json:"progress,omitempty" jsonschema:"with await: the chat's turns go to stderr while waiting"`
	Attach    bool   `json:"attach,omitempty" jsonschema:"attach a terminal to the new chat; not with await"`
}

type ActionOutput struct {
	Status  string `json:"status"`
	Code    int    `json:"code"`
	Message string `json:"message,omitempty"`
}

type KillInput struct {
	Target string `json:"target"`
	Exit   bool   `json:"exit,omitempty"`
}

// SaveInput is the one verb whose "target" is a FILE, not a chat. Every other
// target in this package addresses a chat, so the field is described here and
// validated in chatSave: a bare word used to create a file of that name in the
// server's working directory and append a whole transcript to it.
type SaveInput struct {
	Target     string `json:"target" jsonschema:"FILE PATH to append the snapshot to (not a chat) — must contain a directory separator, e.g. ./notes/session.md"`
	Transcript string `json:"transcript,omitempty" jsonschema:"path of the transcript .jsonl to dump; defaults to the calling chat's own transcript"`
}

// IssueInput is one agent complaint filed against Professor itself. Reporter
// identity is never accepted here — servicedesk captures it the same
// way chat_inject captures a sender, so a model can complain but never say
// who is complaining.
type IssueInput struct {
	Title    string `json:"title" jsonschema:"short one-line issue title"`
	Detail   string `json:"detail" jsonschema:"full issue body: what went wrong, what was expected, and the surface it happened on"`
	Severity string `json:"severity,omitempty" jsonschema:"low, medium, or high; defaults to medium"`
	Area     string `json:"area,omitempty" jsonschema:"free text naming the command, agent, file, or surface this is about"`
}

// IssueOutput is servicedesk's receipt: the filed issue's stable id.
type IssueOutput struct {
	Status string `json:"status"`
	ID     int64  `json:"id,omitempty"`
}
