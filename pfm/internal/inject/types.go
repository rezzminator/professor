// Package inject implements guarded, socket-scoped live chat delivery.
package inject

import (
	"context"
	"io"
	"time"

	"hostops/pfm/internal/clock"
	"hostops/pfm/internal/fleetdb"
	"hostops/pfm/internal/paths"
	"hostops/pfm/internal/resolve"
)

const (
	// CodeAmbiguous means more than one live target matched. It stays distinct
	// from CodeUnknown so callers never report "not found" while listing the
	// conflicting candidates in the same receipt.
	CodeAmbiguous = 2
	// CodeDead means the target resolved to a live pane that disappeared or
	// became unreadable before delivery could complete.
	CodeDead = 3
	// CodeUnknown means no live target matched the requested namespace.
	CodeUnknown = 4
	// CodeUndelivered means the target exists but the guarded transaction did
	// not put a turn into its model input.
	CodeUndelivered = 6
	// CodeBusy is the verdict a caller retries rather than escalates: the pane
	// was working, so nothing was typed. It is internal retry telemetry; the
	// CLI maps it to CodeUndelivered and never exposes rc 7.
	CodeBusy = 7
	// ClaudeInlineMax and CodexInlineMax are 10% below the earliest
	// empirically observed composer failure for each engine. Claude's smaller
	// bracketed-paste edge is 801 characters; Codex's inline and paste edge is
	// 1001. TESTPLAN.md records the authentic probe method and both
	// transports.
	//
	// On the LIVE delivery path (engine.go's transport ladder) this is the
	// inline-SendLiteral-vs-bracketed-SendPaste boundary, not an
	// inline-vs-file boundary: a message over it still reaches the pane
	// whole, through paste, proven by either a tail match or the composer's
	// own collapsed-paste placeholder — it no longer means "spill to a file
	// pointer here". On the dormant/resume path (PrepareForResume, body.go),
	// which writes directly into a transcript with no composer to paste
	// into, it is still the inline-vs-file-pointer boundary it always was.
	// Renamed from ClaudeAutoFileMax/CodexAutoFileMax when the live meaning
	// changed — do not raise these numbers on a guess; they are re-measured
	// only by the REAL-SESSION probe TESTPLAN.md's edge table describes.
	ClaudeInlineMax = 720
	CodexInlineMax  = 900
	// CommandChunkRunes stays safely below both measured literal-paste edges.
	// Slash commands bypass auto-file pointers, so every command byte reaches
	// the TUI through paced literal sends and Enter lands only after the final
	// chunk.
	CommandChunkRunes = 512
	CommandChunkGap   = 50 * time.Millisecond
	// FullScrollback asks Capture for the entire retained buffer, chat.sh's
	// `capture-pane -S -`, instead of the visible fold.
	FullScrollback = -1
)

// Target is one resolved tmux destination.
type Target struct {
	SocketPath string `json:"socket_path"`
	Pane       string `json:"pane"`
	Engine     string `json:"engine"`
	Name       string `json:"name,omitempty"`
	ID         string `json:"id,omitempty"`
	Session    string `json:"session,omitempty"`
}

// Request describes one live injection.
type Request struct {
	Target   string
	Message  string
	ForceNow bool
	Origin   string
	// FileBacked says Message came from --file. It travels through tmux's
	// bracketed-paste transport so a long Codex body is not mistaken for an
	// inline composer whose visible head/tail cannot be proven.
	FileBacked bool
	// Then carries follow-up steers delivered, in order, by a DETACHED waiter
	// once the primary turn settles busy -> idle-stable (chat.sh --then).
	Then []string
	// Chain marks this delivery as a hop of an existing steer chain, so the
	// waiter's log is appended to instead of truncated (chat.sh CHAT_THEN_CHAIN).
	Chain bool
}

// Result is a non-ambiguous delivery verdict.
type Result struct {
	Status         string `json:"status"`
	Code           int    `json:"code"`
	Message        string `json:"message"`
	SocketPath     string `json:"socket_path,omitempty"`
	Pane           string `json:"pane,omitempty"`
	Proof          string `json:"proof,omitempty"`
	ResolutionNote string `json:"resolution_note,omitempty"`
	Busy           bool   `json:"busy,omitempty"`
	Interrupted    bool   `json:"interrupted,omitempty"`
	DraftStashed   bool   `json:"draft_stashed,omitempty"`
	Typed          bool   `json:"typed,omitempty"`
	SubmitRetries  int    `json:"submit_retries,omitempty"`
	Steers         int    `json:"steers,omitempty"`
	SteerLog       string `json:"steer_log,omitempty"`
	Unsigned       bool   `json:"unsigned,omitempty"`
	AutoFilePath   string `json:"auto_file_path,omitempty"`
	LiteralChunks  int    `json:"literal_chunks,omitempty"`
}

// Sender is appended to every non-command plain-text delivery.
type Sender struct {
	Session string
	Label   string
	UUID    string
}

// Resolver is the existing chat.sh-compatible namespace resolver.
type Resolver interface {
	Resolve(context.Context, resolve.Kind, string) (resolve.Outcome, error)
}

// NameResolver projects the composed fleet roster into a live target. Code 4
// means the roster has no answer and Engine must continue through its raw pane
// fallbacks; Code 2 is authoritative roster ambiguity and stops resolution.
type NameResolver interface {
	ResolveName(context.Context, string, string) (Target, int, string, error)
}

// SenderNamer is the roster read backwards: what the fleet calls the chat at
// a live seat. A NameResolver that also implements it is asked before the
// sender's own screen is scraped, so the reply hint a delivery carries is the
// exact name ResolveName matches first. found=false is an answer (the seat is
// not in the roster, or its rows disagree); an error is a failure to look.
type SenderNamer interface {
	SenderName(context.Context, resolve.Identity) (name string, found bool, err error)
}

// SelfIdentifier answers who the SENDER is — chat.sh's self_tmux, including
// its ancestry recovery — so an inject from a codex-origin shell still signs.
type SelfIdentifier interface {
	Identify(ctx context.Context) (resolve.Identity, error)
}

// Tmux supplies every operation used in the guarded critical section.
type Tmux interface {
	Capture(
		ctx context.Context,
		socketPath, target string,
		styled bool,
		scrollback int,
	) (string, error)
	SendLiteral(ctx context.Context, socketPath, target, text string) error
	SendPaste(ctx context.Context, socketPath, target, text string) error
	SendKey(ctx context.Context, socketPath, target, key string) error
	CancelCopyMode(ctx context.Context, socketPath, target string) error
	PaneInMode(ctx context.Context, socketPath, target string) (bool, error)
	// PaneCommand verifies and identifies the foreground Claude/Codex TUI
	// before Inject types into its mid-turn composer queue. Socket spelling is
	// an address, not proof of what process currently owns the pane.
	PaneCommand(ctx context.Context, socketPath, target string) (string, error)
	CurrentSession(ctx context.Context, socketPath string) (string, error)
	// WindowName backs chat.sh's codex label fallback: a codex chat has no 🔖
	// statusline, so its human thread name is the tmux window name.
	WindowName(ctx context.Context, socketPath, target string) (string, error)
	// ClientActivity reports the most recent keystroke time among the clients
	// attached to the session that holds target (a pane id or session name).
	// ok is false when no client is attached — an unattended pane has no typist.
	ClientActivity(ctx context.Context, socketPath, target string) (last time.Time, ok bool, err error)
}

// ThenSpawner starts the detached waiter that delivers --then steers. It must
// outlive the caller: for a self-inject the waiter waits on the very turn that
// spawned it, so a synchronous wait would deadlock.
type ThenSpawner interface {
	Spawn(ctx context.Context, request SteerSpawn) error
}

// SteerSpawn is one detached follow-up delivery.
type SteerSpawn struct {
	SocketPath string
	Target     string
	// Engine is the target's engine as the spawning chat resolved it
	// (Target.Engine), handed to the waiter as `--engine` because a Codex
	// pane's turn boundary is not readable yet (ThenWait.Engine).
	Engine  string
	Steers  []string
	LogPath string
	Append  bool
	// Sender is the spawning chat's own identity, carried down because the
	// waiter runs detached and can derive none of its own.
	Sender Sender
	// SelfTarget marks the one shape where the pane being watched is ALSO the
	// pane that asked for the wait. Only then is the pane busy with a turn
	// that is not the primary's when the waiter starts, and only then must the
	// waiter let that turn finish before it can recognise the primary's. For
	// every other target the first busy IS the primary's turn, and waiting for
	// an idle that already went by would delay the steer for nothing.
	SelfTarget bool
}

// ThenWait is what the detached waiter (`pfm internal then`, hookentry.Then)
// hands Engine.DeliverThen: the pane to ride out, the steers to deliver once
// it settles, and the two facts about the pane the waiter cannot derive on
// its own.
type ThenWait struct {
	SocketPath string
	Target     string
	Steers     []string
	// SelfTarget: the pane being watched is the pane that asked for the
	// wait, so the caller's own turn must end before any turn can be the
	// primary's (SteerSpawn.SelfTarget).
	SelfTarget bool
	// Engine is Target.Engine as the spawning chat resolved it. On a Codex
	// pane the busy/compaction footer is not pinned, so an unobserved turn
	// boundary leaves the steer UNDELIVERED by name rather than typed into
	// a compaction on the steady-idle guess (DeliverThen).
	Engine string
}

// Options controls bounded retries. Zero values select chat.sh defaults.
type Options struct {
	Poll              time.Duration
	EnterSettle       time.Duration
	ProofSettle       time.Duration
	BusyTries         int
	InterruptTries    int
	StashTries        int
	SettleTries       int
	EnterTries        int
	Scrollback        int
	ProofLines        int
	LockTimeout       time.Duration
	LockPoll          time.Duration
	LockMaxHold       time.Duration
	ClaudeInlineMax   int
	CodexInlineMax    int
	CommandChunkRunes int
	CommandChunkGap   time.Duration
	LockRoot          string
	BodyRoot          string
	BodyMaxAge        time.Duration
	// Clock is the time seam every Now/Sleep in this package crosses instead
	// of the bare standard-library clock — nil defaults to clock.Real, so a
	// caller outside the test suite behaves exactly as it always has.
	Clock            clock.Clock
	Sender           *Sender
	DisableSignature bool
	// AllowUnsigned permits delivery when no sender identity could be derived.
	// It is OFF by default: an unsigned message asks its recipient to act on
	// an instruction from nobody, and the recipient's only correct response is
	// to distrust it — so the message was never worth sending. Refusing at the
	// SENDER, where the operator can still fix the identity, beats delivering
	// something the far end must ignore.
	AllowUnsigned bool
	// TypistQuiet is how long a target's composer must have gone without a
	// keystroke before a normal (non-force-now) delivery or --then steer will
	// type into it. C-s protects a PARKED draft, never a human mid-keystroke;
	// this is the guard for the latter (the 2026-09-03 self-compact that ate an operator's live draft).
	TypistQuiet time.Duration

	// --then waiter cadence, mirroring chat.sh's __then subcommand.
	ThenMin        time.Duration
	ThenBusyTries  int
	ThenIdlePoll   time.Duration
	ThenIdleTries  int
	ThenIdleStable int
	ThenSettle     time.Duration
	ThenLogRoot    string
}

// Dependencies are injectable for jailed and adversarial tests.
type Dependencies struct {
	Resolver   Resolver
	Names      NameResolver
	Tmux       Tmux
	Spawner    ThenSpawner
	Identifier SelfIdentifier
	// ClaudeBinary and CodexBinary are the configured launch commands. Tmux
	// exposes only each foreground process basename, so Engine uses these
	// values to verify a live pane without assuming the defaults.
	ClaudeBinary string
	CodexBinary  string
	// OpenCodeBinary verifies a live OpenCode pane the same way.
	OpenCodeBinary string
	AccountEmojis  []string
	// CodexSeat is the last sender-identity rung. It maps this process's
	// CODEX_THREAD_ID to the live fleet seat after ambient tmux and ancestry
	// recovery both fail. Nil means that lookup is unavailable.
	CodexSeat SelfIdentifier
	Recorder  func(context.Context, fleetdb.CommsEvent) error
	// WarningWriter receives non-fatal recorder failures. Nil uses stderr.
	WarningWriter io.Writer
	Options       Options
	// Env is the process-environment seam every host-environment read in this
	// package crosses instead — nil defaults to paths.OSEnv{}, so a caller
	// outside the test suite reads the real environment exactly as it always
	// has, and a test can inject paths.MapEnv (or hostfixture's Base.Env) to
	// jail every read.
	Env paths.Env
}
