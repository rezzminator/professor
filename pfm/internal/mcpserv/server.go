package mcpserv

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hostops/pfm/internal/chat"
	pfmconfig "hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/headless"
	"hostops/pfm/internal/inject"
	"hostops/pfm/internal/paths"
	"hostops/pfm/internal/resolve"
	"hostops/pfm/internal/transcript"
)

const (
	// defaultCaptureBytes bounds a whole-scrollback capture for the default
	// caller; maxCaptureBytes is the ceiling an explicit max_bytes may ask for.
	defaultCaptureBytes = 256 << 10
	maxCaptureBytes     = 4 << 20
	statusNotFound      = "not_found"
	statusAmbiguous     = "ambiguous"
)

// selfCompactDescription is a named const so the registered text and the test
// that pins it read the same string. The STOP clause is not decoration: the
// --then waiter recognises the compaction turn by watching this pane yield and
// then go busy again, and a caller that keeps working erases that boundary.
const selfCompactDescription = "Compacts THIS chat in place after its turn settles and KEEPS the session (crons, sub-agents, pane) — the only answer to \"compact yourself\" / \"self-compact at this milestone\", this tool and nothing else, never a hand-typed /compact. Call chat_self_compact{focus:\"one line\", then:\"one steer\"} — exactly ONE post-compact steer, a string never a list. Only focus and then cross the boundary — write durable state to disk FIRST. END THE TURN IMMEDIATELY after it returns, run no further tool; more work lands the steer beside the compaction. Main chat only — a sub-agent has no pane."

var chatToolNames = []string{
	"chat_capture", "chat_find", "chat_inject",
	"chat_keys", "chat_kill", "chat_last", "chat_ls", "chat_name",
	"chat_new", "chat_open", "chat_read", "chat_resolve",
	"chat_save", "chat_self_compact", "chat_status", "chat_unkill",
	"chat_whoami", "issue_servicedesk",
}

// ToolNames returns the canonical advertised chat MCP roster. The jailed
// protocol test compares it to tools/list, so a registered tool cannot vanish
// from daemon status and doctor through a second hand-maintained list.
func ToolNames() []string {
	return append([]string(nil), chatToolNames...)
}

// Service owns one MCP server and its long-lived SQLite handle.
type Service struct {
	server  *mcp.Server
	backend *backend
}

// Runtime is the already-loaded machine policy the stdio server shares with
// the command that started it. The server never re-reads machine config.
type Runtime struct {
	Paths        paths.Values
	Accounts     []pfmconfig.Account
	ConfigPath   string
	ClaudeBinary string
	CodexBinary  string
	// OpenCodeBinary is the configured OpenCode launch command; empty means
	// the registered OpenCode descriptor's default binary.
	OpenCodeBinary string
	// Chat is the typed verb layer (production: chat.Verbs over the command's
	// runtime). Verbs not yet on it still reach package main through Dispatch.
	Chat ChatVerbs
	// Names is inject's fleet roster rung (production: chat.NameResolver over
	// the command's runtime). Nil leaves inject to its raw pane fallbacks.
	Names    inject.NameResolver
	Dispatch Dispatch
	// AllowAmbientIdentity is reserved for the stdio server, whose process is
	// launched by the calling chat. A shared HTTP daemon must leave it false:
	// its environment and ancestry identify the daemon's launcher, not the MCP
	// request that happens to be using it now.
	AllowAmbientIdentity bool
}

// ChatVerbs is the slice of the chat verb layer this server calls typed. Its
// production value is chat.Verbs; tests substitute a recorder. MCP owns only
// the adaptation — input validation, payload bounds, the wire shape — never a
// second implementation of a verb.
type ChatVerbs interface {
	Last(context.Context, chat.LastRequest) (chat.LastResult, error)
	Status(context.Context, chat.StatusRequest) (headless.Status, error)
	List(context.Context, chat.ListRequest) (chat.ListResult, error)
	Find(context.Context, chat.FindRequest) ([]chat.TranscriptMatch, error)
	Read(ctx context.Context, target string, tail int) (headless.Chat, []transcript.Entry, bool, error)
}

// Dispatch is the in-process command dispatcher used by stateful chat tools.
// The callback receives the same argv beginning with "chat" that the CLI
// receives after global flags have been handled.
type Dispatch func(context.Context, []string, io.Writer, io.Writer) int

// NewConfigured creates the production service over one command runtime.
func NewConfigured(version string, warnings io.Writer, runtime Runtime) (*Service, error) {
	backend, err := newBackendConfigured(warnings, runtime)
	if err != nil {
		return nil, err
	}
	return newService(version, backend), nil
}

func newService(version string, backend *backend) *Service {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "pfm",
		Version: version,
	}, &mcp.ServerOptions{
		Instructions: "The local pfm chat fleet — cross-chat communication between independent running chats, never parent/child agent communication (a sub-agent returns its result and its parent reads it; neither holds a reason to call any chat_* verb). Routing — \"send / tell / message / reply to / inject into chat X\" is chat_inject; \"compact yourself / self-compact at this milestone\" is chat_self_compact; \"who am I / my address\" is chat_whoami; \"what chats are running\" is chat_ls; \"spawn / start a new chat\" is chat_new; \"is chat X idle, what is it doing\" is chat_status; \"what did X answer last\" is chat_last; \"find / read an old transcript\" is chat_find then chat_read; \"dump my transcript to a file\" is chat_save. Every target names a chat except chat_save's, which is a file path. end, modal, watch, stream, recover, and history stay shell-only pfm chat commands.",
	})
	service := &Service{server: server, backend: backend}
	service.register()
	return service
}

// Run serves until the transport closes.
func (service *Service) Run(ctx context.Context, transport mcp.Transport) error {
	return service.server.Run(ctx, transport)
}

// Server exposes the SDK server for in-memory protocol tests.
func (service *Service) Server() *mcp.Server {
	return service.server
}

// Close closes the long-lived store.
func (service *Service) Close() error {
	return service.backend.close()
}

func (service *Service) register() {
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true}
	mutating := &mcp.ToolAnnotations{ReadOnlyHint: false}
	mcp.AddTool(service.server, &mcp.Tool{
		Name:        "chat_ls",
		Description: "Lists live and resumable chats as rows — \"what chats are running\", \"is there a chat named X\". Call chat_ls{} or chat_ls{project:\"substring\", all:true}. Returns rows plus matched, truncated, and the filter echoed back; rows empty with matched 0 = nothing matched; a tool error = the fleet could not be read. Address a row with chat_inject by its session or name.",
		Annotations: readOnly,
	}, service.chatLS)
	mcp.AddTool(service.server, &mcp.Tool{
		Name:        "chat_resolve",
		Description: "Resolves one exact name to its tmux socket and pane — \"where does chat X live\", checking a target before chat_inject or chat_keys. Call chat_resolve{kind:\"label\", name:\"my-chat\"}. Returns status ok (code 0) with socket_path and pane; not_found (1) = no such chat; ambiguous (2) = several match, listed in candidates; a tool error = the resolver itself failed.",
		Annotations: readOnly,
	}, service.chatResolve)
	mcp.AddTool(service.server, &mcp.Tool{
		Name:        "chat_inject",
		Description: "Types and submits a message into another live chat — every \"send / tell / message / reply to / inject into chat X\" ask. Call chat_inject{target:\"my-chat\", message:\"…\"}; follow-up steers go in then. Cross-chat only — one independent chat addressing another; a sub-agent reports to its parent by returning its result and never calls this. Returns status delivered with proof; queued = target mid-turn, submits after; refused or undelivered = not sent, message says why; not_found = no such chat; a tool error = delivery itself broke.",
		Annotations: mutating,
	}, service.chatInject)
	mcp.AddTool(service.server, &mcp.Tool{
		Name:        "chat_self_compact",
		Description: selfCompactDescription,
		Annotations: mutating,
	}, service.chatSelfCompact)
	mcp.AddTool(service.server, &mcp.Tool{
		Name:        "chat_keys",
		Description: "Presses tmux keys in a live chat — \"press Escape / Enter in chat X\", accept a modal, interrupt a turn. Call chat_keys{target:\"my-chat\", keys:[\"Escape\"]}; raw text is keys:[\"y\"] with literal:true. For a whole message use chat_inject; a sub-agent never drives its parent's pane. Returns status ok with count sent; not_found = no such chat; dead = the pane vanished mid-sequence, count says how many landed; a tool error = an unknown key name, the valid ones listed.",
		Annotations: mutating,
	}, service.chatKeys)
	mcp.AddTool(service.server, &mcp.Tool{
		Name:        "chat_capture",
		Description: "Captures a live chat's screen text — \"what is on chat X's screen\", \"show me its scrollback\". Call chat_capture{target:\"my-chat\"} or chat_capture{target:\"my-chat\", tail_lines:200}. Returns status ok with text (truncated flags a cut, most recent kept); not_found = no such chat; ambiguous = several match; a tool error = the capture itself failed. For the last answer only, chat_last; for a killed or old chat, chat_read.",
		Annotations: readOnly,
	}, service.chatCapture)
	mcp.AddTool(service.server, &mcp.Tool{
		Name:        "chat_whoami",
		Description: "Reports THIS chat's own identity — \"who am I\", \"what is my address\" — the session name another chat targets with chat_inject. Call chat_whoami{}. Returns status ok with session, socket_path, pane, engine, id; not_found = this transport carries no caller identity, message names the pfm chat command to run from the chat's own shell instead. A sub-agent has no chat identity — it reports to its parent by returning.",
		Annotations: readOnly,
	}, service.chatWhoami)
	mcp.AddTool(service.server, &mcp.Tool{
		Name:        "chat_find",
		Description: "Finds indexed transcripts by a literal excerpt — \"which chat said X\", \"find the session where we discussed Y\". Call chat_find{excerpt:\"a distinctive line from it\"}. Returns ranked candidates (id, path, hits) — pass an id to chat_read. A miss is the tool error \"no session contains the excerpt\" (try a longer, more distinctive chunk); any other error = the transcript index could not be read.",
		Annotations: readOnly,
	}, service.chatFind)
	mcp.AddTool(service.server, &mcp.Tool{
		Name:        "chat_read",
		Description: "Reads the recent visible turns of an indexed transcript — \"what happened in that chat\", after chat_find or with a known id. Call chat_read{source:\"<id from chat_find>\", last_n:20}. Returns turns (role, text, timestamp) with count and truncated; turns empty with count 0 = the transcript has no visible turns yet; a tool error = no transcript by that id or path. For a LIVE chat's current answer, chat_last.",
		Annotations: readOnly,
	}, service.chatRead)
	mcp.AddTool(service.server, &mcp.Tool{
		Name:        "chat_last",
		Description: "Returns the newest assistant answer of a chat — \"what did chat X just say\", \"read its last reply\". Call chat_last{target:\"my-chat\"}. Returns text; a chat that has not answered yet and an unknown target are both tool errors whose message names which (\"returned no answer\" versus a resolve failure). For screen text, chat_capture; for older turns, chat_read.",
		Annotations: readOnly,
	}, service.chatLast)
	mcp.AddTool(service.server, &mcp.Tool{
		Name:        "chat_status",
		Description: "Inspects one chat — \"is chat X idle / busy / dead\", \"what is it doing\". Call chat_status{target:\"my-chat\"}; summary:true adds a digest of its last exchange, ask:true a live-screen answer. Returns name, state, idle_seconds (nonzero only while state is idle), context_pct and last; state dead is a result, not an error; a tool error = the target did not resolve or the status command failed.",
		Annotations: readOnly,
	}, service.chatStatus)
	mcp.AddTool(service.server, &mcp.Tool{
		Name:        "chat_new",
		Description: "Spawns a new detached, named chat — \"spawn / start a new chat\", \"open a fresh chat for X\". Call chat_new{name:\"my-chat\", prompt:\"first message\"}; born in the caller's project directory unless cwd is given. Returns status ok with the launch message; a tool error = the launch failed, message carries its stderr. A new chat is an independent peer — a helper inside THIS chat is a harness sub-agent, not a chat.",
		Annotations: mutating,
	}, service.chatNew)
	mcp.AddTool(service.server, &mcp.Tool{
		Name:        "chat_open",
		Description: "Reopens a resumable (not live) chat in a pane — \"resume / reopen chat X\". Call chat_open{target:\"my-chat\"}. Returns status ok with the open message; a tool error = no such resumable chat or the open failed, message carries its stderr. A live chat needs no opening — address it with chat_inject.",
		Annotations: mutating,
	}, service.chatOpen)
	mcp.AddTool(service.server, &mcp.Tool{
		Name:        "chat_name",
		Description: "Names or renames a live chat — \"call this chat X\", \"rename chat A to B\". Call chat_name{target:\"self\", name:\"my-chat\"}. Returns status ok; a tool error = the name was empty or multi-line, the target did not resolve, or the rename failed, message says which.",
		Annotations: mutating,
	}, service.chatName)
	mcp.AddTool(service.server, &mcp.Tool{
		Name:        "chat_kill",
		Description: "Hides a chat from the fleet — \"kill / hide / close chat X\". A live target is also ended (its pane and socket close); a resumable-only target is only hidden. Call chat_kill{target:\"my-chat\"}. Returns status ok; a tool error = the target did not resolve or the kill failed, message says which. Reverse with chat_unkill.",
		Annotations: mutating,
	}, service.chatKill)
	mcp.AddTool(service.server, &mcp.Tool{
		Name:        "chat_unkill",
		Description: "Restores a killed chat to the fleet listing — \"unkill / unhide chat X\", the reverse of chat_kill. Call chat_unkill{target:\"my-chat\"}. Returns status ok; a tool error = no killed chat by that name or the restore failed. It does not relaunch a pane — chat_open does that.",
		Annotations: mutating,
	}, service.chatUnkill)
	mcp.AddTool(service.server, &mcp.Tool{
		Name:        "chat_save",
		Description: "Appends a transcript snapshot plus environment snapshot to a FILE — \"save / dump this conversation to notes.md\". Call chat_save{target:\"./notes/session.md\"}; the calling chat's own transcript by default. target is a file path, never a chat — a bare word is refused. Returns status ok with the write message; a tool error = the path had no directory separator, the transcript was not found, or the write failed.",
		Annotations: mutating,
	}, service.chatSave)
	mcp.AddTool(service.server, &mcp.Tool{
		Name:        "issue_servicedesk",
		Description: "Files a durable complaint about Professor itself for a human to triage — a command, agent, hook, or tool that misbehaved. Call issue_servicedesk{title:\"one line\", detail:\"what went wrong, what was expected, where\", area:\"/pfm\"}. Reporter identity is captured, never supplied. Returns status ok with the issue id; a tool error = title or detail missing, unknown severity, or the write failed — nothing was filed. Not for a bug in the user's own project.",
		Annotations: mutating,
	}, service.issueServicedesk)
}

type requestScopedInjector interface {
	WithIdentity(resolve.Identity, string) *inject.Engine
}

func requestMeta(request *mcp.CallToolRequest) mcp.Meta {
	if request == nil || request.Params == nil {
		return nil
	}
	return request.Params.Meta
}

func selfTarget(target string) bool {
	return target == "self" || target == "me"
}

// noAmbientCallerRemedy explains a missing-identity refusal in terms an MCP
// caller can act on, instead of the config fact chat.sh's CLI-oriented
// wording states. The shared HTTP daemon (one process serving every chat on
// the machine) genuinely cannot derive who is calling it over this
// transport: Claude Code attaches no per-call caller identity, so there is
// nothing here to sign or resolve "self" against. A Codex chat is
// unaffected — its MCP client attaches _meta.threadId to every call, and
// that is what this daemon reads. The remedy is architectural, not
// something available to THIS call: run the equivalent `pfm chat ...`
// command from the chat's own shell, since that process IS the chat and can
// derive its own identity; or ask the operator to move this chat's MCP
// transport onto per-chat stdio (`pfm mcp chat serve`), which inherits
// ambient identity deliberately.
const noAmbientCallerRemedy = "MCP request has no _meta.threadId, and this " +
	"server is pfm's shared HTTP daemon (one process serving every chat on " +
	"the machine), so it cannot derive who is calling: Claude Code does not " +
	"attach per-call caller identity over this transport. Run the " +
	"equivalent `pfm chat ...` command from the chat's own shell instead — " +
	"that process IS the chat. A Codex chat should resolve automatically; " +
	"if it does not, its MCP client is not attaching _meta.threadId to this call."

// selfCompactNoAmbientRemedy replaces noAmbientCallerRemedy for
// chat_self_compact alone: the generic message's remedy — "run the
// equivalent `pfm chat ...` command" — never says which subcommand.
// chat_self_compact's CLI twin is `pfm chat self-compact`, which shares this
// tool's engine method (Engine.ScheduleSelfCompact) and its wait-for-the-
// caller's-own-turn-to-end contract — never a live /compact keystroke.
const selfCompactNoAmbientRemedy = "MCP request has no _meta.threadId, and " +
	"this server is pfm's shared HTTP daemon (one process serving every chat " +
	"on the machine), so it cannot derive who is calling: Claude Code does " +
	"not attach per-call caller identity over this transport. From the " +
	"chat's own shell, run `pfm chat self-compact --then '<steer>' " +
	"'<focus>'`. A Codex chat should resolve automatically; if it does not, " +
	"its MCP client is not attaching _meta.threadId to this call."

func (service *Service) selfCallerRefusal(caller callerIdentity) (bool, string) {
	if caller.valid {
		return false, ""
	}
	if caller.present {
		return true, caller.detail
	}
	if !service.backend.allowAmbientIdentity {
		return true, noAmbientCallerRemedy
	}
	return false, ""
}

// injectorForRequest binds a valid Codex _meta.threadId to a fresh injection
// engine. A malformed or unknown metadata value remains usable only for an
// explicit target; self is refused by each stateful handler below.
func (service *Service) injectorForRequest(
	ctx context.Context,
	request *mcp.CallToolRequest,
) (injectionService, callerIdentity, error) {
	caller, err := service.backend.callerForRequest(ctx, requestMeta(request))
	if err != nil {
		return nil, caller, err
	}
	if !caller.valid {
		return service.backend.injector, caller, nil
	}
	scoped, ok := service.backend.injector.(requestScopedInjector)
	if !ok {
		return nil, caller, fmt.Errorf("request-scoped MCP caller identity is unsupported by the injection engine")
	}
	return scoped.WithIdentity(caller.identity, caller.row.Name), caller, nil
}

func (service *Service) chatKeys(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input KeysInput,
) (*mcp.CallToolResult, KeysOutput, error) {
	if strings.TrimSpace(input.Target) == "" {
		return nil, KeysOutput{}, fmt.Errorf("target is required")
	}
	if len(input.Keys) == 0 {
		return nil, KeysOutput{}, fmt.Errorf("keys must contain at least one key")
	}
	if !input.Literal {
		for _, key := range input.Keys {
			if !chat.KeyValid(key) {
				return nil, KeysOutput{}, fmt.Errorf(
					"%q is not a tmux key; set literal=true to type it as text, or use one of: %s",
					key,
					chat.KeyNames(),
				)
			}
		}
	}
	delay := 120 * time.Millisecond
	if input.DelayMS != 0 {
		if input.DelayMS < 0 {
			return nil, KeysOutput{}, fmt.Errorf("delay_ms must not be negative")
		}
		delay = time.Duration(input.DelayMS) * time.Millisecond
	}
	injector, caller, err := service.injectorForRequest(ctx, request)
	if err != nil {
		return nil, KeysOutput{}, err
	}
	if refused, _ := service.selfCallerRefusal(caller); selfTarget(input.Target) && refused {
		return nil, KeysOutput{
			Status: statusNotFound,
			Code:   inject.CodeUnknown,
			Keys:   append([]string(nil), input.Keys...),
		}, nil
	}
	target, code, detail, err := injector.Resolve(ctx, input.Target)
	if err != nil {
		return nil, KeysOutput{}, err
	}
	if code != 0 {
		output := KeysOutput{
			Status: statusNotFound,
			Code:   code,
			Keys:   append([]string(nil), input.Keys...),
		}
		return nil, output, fmt.Errorf("resolve %q: %s", input.Target, detail)
	}
	tmux := inject.TmuxInjector{}
	for index, key := range input.Keys {
		if index > 0 && delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, KeysOutput{}, ctx.Err()
			case <-timer.C:
			}
		}
		var sendErr error
		if input.Literal {
			sendErr = tmux.SendLiteral(ctx, target.SocketPath, target.Pane, key)
		} else {
			sendErr = tmux.SendKey(ctx, target.SocketPath, target.Pane, key)
		}
		if sendErr != nil {
			return nil, KeysOutput{
				Status: "dead", Code: inject.CodeDead, SocketPath: target.SocketPath,
				Pane: target.Pane, Count: index, Keys: append([]string(nil), input.Keys...),
			}, fmt.Errorf("send %q: %w", key, sendErr)
		}
	}
	output := KeysOutput{
		Status: "ok", Code: 0, SocketPath: target.SocketPath, Pane: target.Pane,
		Count: len(input.Keys), Keys: append([]string(nil), input.Keys...),
	}
	if input.Capture {
		_, text, captureCode, detail, captureErr := injector.Capture(ctx, input.Target, 0)
		if captureErr != nil {
			return nil, output, fmt.Errorf("capture: %w", captureErr)
		}
		if captureCode != 0 {
			return nil, output, fmt.Errorf("capture: %s", detail)
		}
		output.Text = text
	}
	return nil, output, nil
}

func (service *Service) chatLS(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input LSInput,
) (*mcp.CallToolResult, LSOutput, error) {
	output, err := service.backend.list(ctx, input)
	return nil, output, err
}

func (service *Service) chatResolve(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input ResolveInput,
) (*mcp.CallToolResult, ResolveOutput, error) {
	kind := resolve.Kind(input.Kind)
	if kind != resolve.Label && kind != resolve.Session && kind != resolve.CxWindow {
		return nil, ResolveOutput{}, fmt.Errorf(
			"kind must be label, session, or cxwin",
		)
	}
	if kind == resolve.CxWindow {
		target, code, detail, err := service.backend.injector.ResolveEngine(
			ctx, input.Name, string(pfmengine.Codex),
		)
		if err != nil {
			return nil, ResolveOutput{}, err
		}
		status := "ok"
		switch code {
		case 0:
		case inject.CodeUnknown:
			status, code = statusNotFound, 1
		case inject.CodeAmbiguous:
			status, code = statusAmbiguous, 2
		default:
			return nil, ResolveOutput{}, fmt.Errorf(
				"resolve target %q failed with code %d: %s", input.Name, code, detail,
			)
		}
		return nil, ResolveOutput{
			Status: status, Code: code, SocketPath: target.SocketPath,
			Pane: target.Pane, Candidates: detail,
		}, nil
	}
	namespace, err := service.backend.resolver.Resolve(ctx, kind, input.Name)
	if err != nil {
		return nil, ResolveOutput{}, err
	}
	status := "ok"
	switch namespace.Code {
	case 1:
		status = statusNotFound
	case 2:
		status = statusAmbiguous
	}
	socket, pane := parseResolved(namespace.Stdout)
	return nil, ResolveOutput{
		Status:     status,
		Code:       namespace.Code,
		SocketPath: socket,
		Pane:       pane,
		Candidates: namespace.Stderr,
	}, nil
}

func (service *Service) chatInject(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input InjectInput,
) (*mcp.CallToolResult, InjectOutput, error) {
	injector, caller, err := service.injectorForRequest(ctx, request)
	if err != nil {
		return nil, InjectOutput{}, err
	}
	if refused, detail := service.selfCallerRefusal(caller); selfTarget(input.Target) && refused {
		return nil, InjectOutput{
			Status: statusNotFound, Code: inject.CodeUnknown, Message: detail,
		}, nil
	}
	result, err := injector.Inject(ctx, inject.Request{
		Target:   input.Target,
		Message:  input.Message,
		ForceNow: input.ForceNow,
		Then:     input.Then,
	})
	return nil, outputFromInject(result), err
}

func (service *Service) chatSelfCompact(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input SelfCompactInput,
) (*mcp.CallToolResult, InjectOutput, error) {
	focus := strings.TrimSpace(input.Focus)
	if focus == "" || strings.ContainsAny(focus, "\r\n\x00") {
		return nil, InjectOutput{}, fmt.Errorf("focus must be one non-empty line")
	}
	injector, caller, err := service.injectorForRequest(ctx, request)
	if err != nil {
		return nil, InjectOutput{}, err
	}
	if refused, detail := service.selfCallerRefusal(caller); refused {
		if detail == noAmbientCallerRemedy {
			detail = selfCompactNoAmbientRemedy
		}
		return nil, InjectOutput{
			Status: statusNotFound, Code: inject.CodeUnknown, Message: detail,
		}, nil
	}
	// One steer, by the operator's rule. The engine's own guards still run on
	// it — a steer is required, and it must not start with /compact — and a
	// blank string reaches them as no steer at all rather than as an empty one.
	var then []string
	if steer := strings.TrimSpace(input.Then); steer != "" {
		then = []string{steer}
	}
	// Composition ("/compact " + focus, the Codex bare-command exception) is
	// the engine's own job now (Task D: Engine.ScheduleSelfCompact) — the one
	// implementation `pfm chat self-compact` shares. focus is re-validated
	// there too; the check above stays because this handler must return a
	// tool-call error for a bad focus, not an InjectOutput refusal.
	result, err := injector.ScheduleSelfCompact(ctx, focus, then)
	// The stop notice is appended by the engine itself
	// (inject.SelfCompactStopNotice), which is the single writer for every
	// caller — MCP tool and `pfm chat self-compact` alike. Restating it here
	// would double it on the MCP path only.
	return nil, outputFromInject(result), err
}

// mcpUnsignedMessage restates inject.ErrUnsigned's refusal for an MCP
// caller. The engine's own wording (inject/body.go) is CLI-oriented — it
// tells the reader to set an environment variable or pass --allow-unsigned,
// both unreachable from an MCP tool call, and it blames a detached process
// chain that is not what happened here. The daemon-level cause is real (this
// process still derived no identity of its own), so only the remedy half is
// replaced with one an MCP caller can act on.
func mcpUnsignedMessage() string {
	return inject.ErrUnsigned.Error() +
		": this process derived no identity of its own, so the recipient " +
		"would be asked to act on an instruction from nobody. " +
		noAmbientCallerRemedy
}

func outputFromInject(result inject.Result) InjectOutput {
	message := result.Message
	if strings.Contains(message, inject.ErrUnsigned.Error()) {
		message = mcpUnsignedMessage()
	}
	return InjectOutput{
		Status:        result.Status,
		Code:          result.Code,
		Message:       message,
		SocketPath:    result.SocketPath,
		Pane:          result.Pane,
		Proof:         result.Proof,
		Busy:          result.Busy,
		Interrupted:   result.Interrupted,
		DraftStashed:  result.DraftStashed,
		Typed:         result.Typed,
		SubmitRetries: result.SubmitRetries,
		Steers:        result.Steers,
		SteerLog:      result.SteerLog,
		Unsigned:      result.Unsigned,
		AutoFilePath:  result.AutoFilePath,
		LiteralChunks: result.LiteralChunks,
	}
}

// chatWhoami answers with the requesting chat's identity. Codex sends its
// thread id in reserved protocol metadata. Only the per-chat stdio transport
// may use environment/ancestry; a shared daemon has no ambient caller identity.
func (service *Service) chatWhoami(
	ctx context.Context,
	request *mcp.CallToolRequest,
	_ WhoamiInput,
) (*mcp.CallToolResult, WhoamiOutput, error) {
	caller, err := service.backend.callerForRequest(ctx, requestMeta(request))
	if err != nil {
		return nil, WhoamiOutput{}, err
	}
	if caller.present {
		if !caller.valid {
			return nil, WhoamiOutput{Status: statusNotFound, Message: caller.detail}, nil
		}
		identity := caller.identity
		return nil, WhoamiOutput{
			Status:     "ok",
			Session:    identity.Session,
			SocketPath: identity.SocketPath,
			SocketName: identity.SocketName,
			Pane:       identity.Pane,
			Engine:     identity.Engine,
			ID:         identity.ID,
			Source:     identity.Source,
			Recovered:  identity.Recovered,
		}, nil
	}
	if !service.backend.allowAmbientIdentity {
		return nil, WhoamiOutput{
			Status:  statusNotFound,
			Message: noAmbientCallerRemedy,
		}, nil
	}
	identifier, err := resolve.NewWhoami(resolve.WhoamiDependencies{})
	if err != nil {
		return nil, WhoamiOutput{}, err
	}
	identity, err := identifier.Identify(ctx)
	if err != nil {
		return nil, WhoamiOutput{
			Status:  statusNotFound,
			Engine:  identity.Engine,
			ID:      identity.ID,
			Source:  identity.Source,
			Message: err.Error(),
		}, nil
	}
	return nil, WhoamiOutput{
		Status:     "ok",
		Session:    identity.Session,
		SocketPath: identity.SocketPath,
		SocketName: identity.SocketName,
		Pane:       identity.Pane,
		Engine:     identity.Engine,
		ID:         identity.ID,
		Source:     identity.Source,
		Recovered:  identity.Recovered,
	}, nil
}

func (service *Service) chatCapture(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input CaptureInput,
) (*mcp.CallToolResult, CaptureOutput, error) {
	lines := 40
	if input.TailLines != nil {
		lines = *input.TailLines
	}
	if lines < 1 || lines > 1000 {
		return nil, CaptureOutput{}, fmt.Errorf(
			"tail_lines must be between 1 and 1000",
		)
	}
	maxBytes := defaultCaptureBytes
	if input.MaxBytes != nil {
		maxBytes = *input.MaxBytes
	}
	if maxBytes < 1 || maxBytes > maxCaptureBytes {
		return nil, CaptureOutput{}, fmt.Errorf(
			"max_bytes must be between 1 and %d",
			maxCaptureBytes,
		)
	}
	injector, caller, err := service.injectorForRequest(ctx, request)
	if err != nil {
		return nil, CaptureOutput{}, err
	}
	if refused, detail := service.selfCallerRefusal(caller); selfTarget(input.Target) && refused {
		return nil, CaptureOutput{Status: statusNotFound, Code: inject.CodeUnknown, Message: detail}, nil
	}
	target, text, code, detail, err := injector.Capture(
		ctx,
		input.Target,
		lines,
	)
	status := "ok"
	if code == inject.CodeAmbiguous {
		status = statusAmbiguous
	} else if code != 0 {
		status = statusNotFound
	}
	// The engine captures the WHOLE scrollback; the byte bound is applied here,
	// after the capture, keeping the most recent screen when it has to cut.
	truncated := false
	if len(text) > maxBytes {
		text = tailBytes(text, maxBytes)
		truncated = true
	}
	return nil, CaptureOutput{
		Status:     status,
		Code:       code,
		SocketPath: target.SocketPath,
		Pane:       target.Pane,
		Text:       text,
		Message:    detail,
		Bytes:      len(text),
		Truncated:  truncated,
	}, err
}

// tailBytes keeps the last budget bytes of text without splitting a rune.
func tailBytes(text string, budget int) string {
	if len(text) <= budget {
		return text
	}
	cut := len(text) - budget
	for cut < len(text) && !utf8.RuneStart(text[cut]) {
		cut++
	}
	return text[cut:]
}

func (service *Service) chatFind(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input FindInput,
) (*mcp.CallToolResult, FindOutput, error) {
	output, err := service.backend.find(ctx, input)
	return nil, output, err
}

func (service *Service) chatRead(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input ReadInput,
) (*mcp.CallToolResult, ReadOutput, error) {
	output, err := service.backend.read(ctx, input)
	return nil, output, err
}

func parseResolved(value string) (string, string) {
	fields := strings.Split(strings.TrimSpace(value), "\t")
	if len(fields) != 2 {
		return "", ""
	}
	return fields[0], fields[1]
}
