package mcpserv

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	"github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/resolve"
)

const (
	lastToolName = "last"
	// defaultCaptureBytes bounds a whole-scrollback capture for the default
	// caller; maxCaptureBytes is the ceiling an explicit max_bytes may ask for.
	defaultCaptureBytes = 256 << 10
	maxCaptureBytes     = 4 << 20
	statusNotFound      = "not_found"
	statusAmbiguous     = "ambiguous"
	// statusDead is the answer for a target that RESOLVED and then could not
	// be driven or read — pfmcall.CodeDead. It is deliberately not
	// statusNotFound: a chat that exists and whose pane vanished mid-call is
	// not a chat nobody has ever heard of.
	statusDead = "dead"
)

const (
	killToolName = "kill"
	newToolName  = "new"
	saveToolName = "save"
)

var chatToolNames = []string{
	"call", "capture", "exit",
	"keys", killToolName, lastToolName, "ls", "name",
	newToolName, "open", "resolve",
	saveToolName, "servicedesk", "status", "unkill", "whoami",
}

// chatInstructions is the chat part of every professor server's routing text.
const chatInstructions = "Message a chat → call; list chats → ls; who am I → whoami; start a chat or model run → new, never claude -p or pfm headless exec; kill a run once done, exit this chat; fork this conversation → pfm chat branch; is a chat idle or busy → status; its last answer → last; old transcripts → /chatlog; save my transcript → save; complain about Professor itself → servicedesk. Chats are sessions, never sub-agents. end, modal, watch, stream, recover and history stay shell-only pfm chat commands."

// ToolNames returns the canonical advertised chat MCP roster. The jailed
// protocol test compares it to tools/list, so a registered tool cannot vanish
// from daemon status and doctor through a second hand-maintained list.
func ToolNames() []string {
	return append([]string(nil), chatToolNames...)
}

// Service owns one MCP server and its long-lived SQLite handle.
type Service struct {
	server       *mcp.Server
	backend      *backend
	nameMutex    sync.Mutex
	pendingNames map[string]struct{}
}

// Runtime is the already-loaded machine policy the stdio server shares with
// the command that started it. The server never re-reads machine config.
type Runtime struct {
	Clock        clock.Clock
	Paths        paths.Values
	Accounts     []pfmconfig.Account
	ConfigPath   string
	ClaudeBinary string
	CodexBinary  string
	// OpenCodeBinary is the configured OpenCode launch command; empty means
	// the registered OpenCode descriptor's default binary.
	OpenCodeBinary string
	DaemonAddress  string
	// Chat is the typed verb layer (production: chat.Verbs over the command's
	// runtime). Verbs not yet on it still reach package main through Dispatch.
	Chat ChatVerbs
	// Names is call's fleet roster rung (production: chat.NameResolver over
	// the command's runtime). Nil leaves call to its raw pane fallbacks.
	Names    pfmcall.NameResolver
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
	Exit(ctx context.Context, self headless.Chat) (string, error)
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
		Name:    pfmconfig.MCPRegistrationName(pfmconfig.MCPServerChat),
		Version: version,
	}, &mcp.ServerOptions{Instructions: chatInstructions})
	service := &Service{server: server, backend: backend}
	service.registerTools(server)
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

// registerTools adds the chat family to server, bound to this service: its
// own server and every professor server carrying the chat family.
func (service *Service) registerTools(server *mcp.Server) {
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true}
	mutating := &mcp.ToolAnnotations{ReadOnlyHint: false}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "ls",
		Description: "Lists live and resumable chats as rows — \"what chats are running\", \"is there a chat named X\". Call ls{} or ls{project:\"substring\", all:true}. Scoped to the caller's repository when the caller resolves, else to every one, named in scope; all drops the scope and adds killed and background rows (pfm chat ls --all stays on live chats). Returns rows plus matched, truncated, scope, elsewhere, and the filter echoed back; rows empty with matched 0 = nothing matched; a tool error = the fleet could not be read. Address a row with call by its session or name.",
		Annotations: readOnly,
	}, obs.Tool("ls", service.chatLS))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "resolve",
		Description: "Resolves one exact name to its tmux socket and pane — \"where does chat X live\", checking a target before call or keys. Call resolve{kind:\"label\", name:\"my-chat\"}. Returns status ok (code 0) with socket_path and pane; not_found (1) = no such chat; ambiguous (2) = several match, listed in candidates; a tool error = the resolver itself failed.",
		Annotations: readOnly,
	}, obs.Tool("resolve", service.chatResolve))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "call",
		Description: "Types and submits a message into another live chat — every \"send / tell / message / reply to / call into chat X\" ask. Call call{target:\"my-chat\", message:\"…\"}; follow-up steers go in then. Cross-chat only — one independent chat addressing another; a sub-agent reports to its parent by returning its result and never calls this. Returns status delivered with proof; queued = target mid-turn, submits after; refused or undelivered = not sent, message says why; not_found = no such chat; a tool error = delivery itself broke.",
		Annotations: mutating,
	}, obs.Tool("call", service.chatCall))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "keys",
		Description: "Presses tmux keys in a live chat — \"press Escape / Enter in chat X\", accept a modal, interrupt a turn. Call keys{target:\"my-chat\", keys:[\"Escape\"]}; raw text is keys:[\"y\"] with literal:true. For a whole message use call; a sub-agent never drives its parent's pane. Returns status ok with count sent; not_found = no such chat; dead = the pane vanished mid-sequence, count says how many landed; a tool error = an unknown key name, the valid ones listed.",
		Annotations: mutating,
	}, obs.Tool("keys", service.chatKeys))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "capture",
		Description: "Captures a live chat's screen text — \"what is on chat X's screen\", \"show me its scrollback\". Call capture{target:\"my-chat\"} or capture{target:\"my-chat\", tail_lines:200}. Returns status ok with text (truncated flags a cut, most recent kept); not_found = no such chat; ambiguous = several match; dead = the chat resolved but its pane could not be read; error = the capture failed some other way, message says how. For the last answer only, last; for a killed or old chat, use /chatlog.",
		Annotations: readOnly,
	}, obs.Tool("capture", service.chatCapture))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "whoami",
		Description: "Reports THIS chat's own identity — \"who am I\", \"what is my address\" — the session name another chat targets with call. Call whoami{}. Returns status ok with session, socket_path, pane, engine, id; not_found = this transport carries no caller identity, message names the pfm chat command to run from the chat's own shell instead. A sub-agent has no chat identity — it reports to its parent by returning.",
		Annotations: readOnly,
	}, obs.Tool("whoami", service.chatWhoami))
	mcp.AddTool(server, &mcp.Tool{
		Name:        lastToolName,
		Description: "Returns the newest assistant answer of a chat — \"what did chat X just say\", \"read its last reply\". Call last{target:\"my-chat\"}. Returns text; a chat that has not answered yet and an unknown target are both tool errors whose message names which (\"returned no answer\" versus a resolve failure); on Codex a turn still in progress is a tool error that says so; Claude and OpenCode return the newest answer written so far. For screen text, capture; for older turns, use /chatlog.",
		Annotations: readOnly,
	}, obs.Tool(lastToolName, service.chatLast))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "status",
		Description: "Inspects one chat — \"is chat X idle / busy / dead\", \"what is it doing\". Call status{target:\"my-chat\"}; summary:true adds a digest of its last exchange, ask:true a live-screen answer. Returns name, state, idle_seconds (nonzero only while state is idle or error), context_pct and last; state error is a turn the model server ended (error names its kind, e.g. server_overloaded) and the chat waits at its prompt; state blocked is a chat held by a permission dialog, question or modal on its screen, waiting for its human to answer it; state dead is a result, not an error; a tool error = the target did not resolve or the status command failed.",
		Annotations: readOnly,
	}, obs.Tool("status", service.chatStatus))
	newInputSchema, err := jsonschema.For[NewInput](nil)
	if err != nil {
		panic(fmt.Sprintf("new input schema: %v", err))
	}
	newInputSchema.Properties["cache"].Enum = []any{"1h", "5m"}
	mcp.AddTool(server, &mcp.Tool{
		Name:        newToolName,
		Description: "MANDATORY for every chat or model run this chat starts — a seat, a worker, a probe, at any model, effort or agent role; never claude -p or pfm headless exec. Call new{name:\"auth-review\", model:\"claude-sonnet-5-5\", effort:\"xhigh\", agentRole:\"flights-foreman\", prompt:\"…\", await:true}. kill it once its job is done, or it stays listed in pfm ls. Returns status ok with the launch message, or with await the first answer; a tool error = the launch failed or its first turn hit an API error, its stderr in the message. Not for a helper inside THIS chat → a harness sub-agent.",
		Annotations: mutating,
		InputSchema: newInputSchema,
	}, obs.Tool(newToolName, service.chatNew))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "open",
		Description: "Reopens a resumable (not live) chat in a pane — \"resume / reopen chat X\". Call open{target:\"my-chat\"}. Returns status ok with the open message; a tool error = no such resumable chat or the open failed, message carries its stderr. A live chat needs no opening — address it with call.",
		Annotations: mutating,
	}, obs.Tool("open", service.chatOpen))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "name",
		Description: "Names or renames a live chat — \"call this chat X\", \"rename chat A to B\". Call name{target:\"self\", name:\"my-chat\"}. Returns status ok; a tool error = the name was empty or multi-line, the target did not resolve, or the rename failed, message says which.",
		Annotations: mutating,
	}, obs.Tool("name", service.chatName))
	mcp.AddTool(server, &mcp.Tool{
		Name:        killToolName,
		Description: "Hides a chat from the fleet — \"kill / hide / close chat X\". A live target is also ended (its pane and socket close); a resumable-only target is only hidden. Call kill{target:\"my-chat\"}. Returns status ok; a tool error = the target did not resolve or the kill failed, message says which. Reverse with unkill.",
		Annotations: mutating,
	}, obs.Tool(killToolName, service.chatKill))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "exit",
		Description: "Ends THIS chat itself — \"exit / quit / close this chat\". Call exit{} as this chat's LAST action: then reply one line and end the turn — once the turn ends the pane closes, background work stops, and the chat is hidden from pfm ls; unkill reverses the hide. Only the main chat calls it; a sub-agent returns to its parent instead. Returns status ok, the exit scheduled; a tool error = this call carries no caller identity or the kill failed — nothing ended, unless the error says the chat is hidden with its pane still open. Another chat → kill.",
		Annotations: mutating,
	}, obs.Tool("exit", service.chatExit))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "unkill",
		Description: "Restores a killed chat to the fleet listing — \"unkill / unhide chat X\", the reverse of kill. Call unkill{target:\"my-chat\"}. Returns status ok; a tool error = no killed chat by that name or the restore failed. It does not relaunch a pane — open does that.",
		Annotations: mutating,
	}, obs.Tool("unkill", service.chatUnkill))
	mcp.AddTool(server, &mcp.Tool{
		Name:        saveToolName,
		Description: "Appends a transcript snapshot plus environment snapshot to a FILE — \"save / dump this conversation to notes.md\". Call save{target:\"./notes/session.md\"}; the calling chat's own transcript by default. target is a file path, never a chat — a bare word is refused. Returns status ok with the write message; a tool error = the path had no directory separator, the transcript was not found, or the write failed.",
		Annotations: mutating,
	}, obs.Tool(saveToolName, service.chatSave))
	mcp.AddTool(server, &mcp.Tool{
		Name:        "servicedesk",
		Description: "Files a durable complaint about Professor itself for a human to triage — a command, agent, hook, or tool that misbehaved. Call servicedesk{title:\"one line\", detail:\"what went wrong, what was expected, where\", area:\"/pfm\"}. Reporter identity is captured, never supplied. Returns status ok with the issue id; a tool error = title or detail missing, unknown severity, or the write failed — nothing was filed. Not for a bug in the user's own project.",
		Annotations: mutating,
	}, obs.Tool("servicedesk", service.issueServicedesk))
}

type requestScopedCallEngine interface {
	WithIdentity(resolve.Identity, string) *pfmcall.Engine
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

func relativeResolveTarget(target string) bool {
	if selfTarget(target) {
		return true
	}
	if len(target) < 2 || target[0] != '%' {
		return false
	}
	return strings.Trim(target[1:], "0123456789") == ""
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
// transport onto per-chat stdio (`pfm mcp serve --stdio`), which inherits
// ambient identity deliberately.
const noAmbientCallerRemedy = "MCP request has no _meta.threadId, and this " +
	"server is pfm's shared HTTP daemon (one process serving every chat on " +
	"the machine), so it cannot derive who is calling: Claude Code does not " +
	"attach per-call caller identity over this transport. Run the " +
	"equivalent `pfm chat ...` command from the chat's own shell instead — " +
	"that process IS the chat. A Codex chat should resolve automatically; " +
	"if it does not, its MCP client is not attaching _meta.threadId to this call."

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

// callerForRequest binds a valid Codex _meta.threadId to a fresh call
// engine. A malformed or unknown metadata value remains usable only for an
// explicit target; self is refused by each stateful handler below.
func (service *Service) callEngineForRequest(
	ctx context.Context,
	request *mcp.CallToolRequest,
) (callService, callerIdentity, error) {
	caller, err := service.backend.callerForRequest(ctx, requestMeta(request))
	if err != nil {
		return nil, caller, err
	}
	if !caller.valid {
		return service.backend.callEngine, caller, nil
	}
	scoped, ok := service.backend.callEngine.(requestScopedCallEngine)
	if !ok {
		return nil, caller, fmt.Errorf("request-scoped MCP caller identity is unsupported by the call engine")
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
	callEngine, caller, err := service.callEngineForRequest(ctx, request)
	if err != nil {
		return nil, KeysOutput{}, err
	}
	if refused, _ := service.selfCallerRefusal(caller); selfTarget(input.Target) && refused {
		return nil, KeysOutput{
			Status: statusNotFound,
			Code:   pfmcall.CodeUnknown,
			Keys:   append([]string(nil), input.Keys...),
		}, nil
	}
	target, code, detail, err := callEngine.Resolve(ctx, input.Target)
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
	if !input.Literal {
		if err := pfmcall.ValidateKeyCapabilities(target, input.Keys); err != nil {
			return nil, KeysOutput{
				Status: statusError, Code: codeCapability, SocketPath: target.SocketPath,
				Pane: target.Pane, Keys: append([]string(nil), input.Keys...),
			}, fmt.Errorf("keys: %w", err)
		}
	}
	transport, err := pfmcall.New(pfmcall.Dependencies{})
	if err != nil {
		return nil, KeysOutput{}, fmt.Errorf("keys transport: %w", err)
	}
	for index, key := range input.Keys {
		if index > 0 && delay > 0 {
			timer := service.backend.clock.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, KeysOutput{}, ctx.Err()
			case <-timer.C():
			}
		}
		var sendErr error
		if input.Literal {
			sendErr = transport.SendLiteral(ctx, target, key)
		} else {
			sendErr = transport.SendKey(ctx, target, key)
		}
		if sendErr != nil {
			return nil, KeysOutput{
				Status: statusDead, Code: pfmcall.CodeDead, SocketPath: target.SocketPath,
				Pane: target.Pane, Count: index, Keys: append([]string(nil), input.Keys...),
			}, fmt.Errorf("send %q: %w", key, sendErr)
		}
	}
	output := KeysOutput{
		Status: "ok", Code: 0, SocketPath: target.SocketPath, Pane: target.Pane,
		Count: len(input.Keys), Keys: append([]string(nil), input.Keys...),
	}
	if input.Capture {
		_, text, captureCode, detail, captureErr := callEngine.Capture(ctx, input.Target, 0)
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
	request *mcp.CallToolRequest,
	input LSInput,
) (*mcp.CallToolResult, LSOutput, error) {
	repo, scope, err := service.backend.lsScope(ctx, requestMeta(request), input)
	if err != nil {
		return nil, LSOutput{}, err
	}
	output, err := service.backend.listProjected(ctx, input, true, repo)
	output.Scope = scope
	return nil, output, err
}

func (service *Service) chatResolve(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input ResolveInput,
) (*mcp.CallToolResult, ResolveOutput, error) {
	kind := resolve.Kind(input.Kind)
	if kind != resolve.Label && kind != resolve.Session && kind != resolve.CxWindow {
		return nil, ResolveOutput{}, fmt.Errorf(
			"kind must be label, session, or cxwin",
		)
	}
	name := strings.TrimSpace(input.Name)
	if len(name) >= 2 && strings.HasPrefix(name, `"`) && strings.HasSuffix(name, `"`) {
		name = strings.TrimSpace(name[1 : len(name)-1])
	}
	relative := relativeResolveTarget(name)
	requiredEngine := ""
	if kind == resolve.CxWindow && !relative {
		requiredEngine = string(pfmengine.Codex)
	}
	callEngine, caller, err := service.callEngineForRequest(ctx, request)
	if err != nil {
		return nil, ResolveOutput{}, err
	}
	if refused, detail := service.selfCallerRefusal(caller); relative && refused {
		return nil, ResolveOutput{Status: statusNotFound, Code: 1, Candidates: detail}, nil
	}
	type kindResolver interface {
		ResolveKind(context.Context, string, resolve.Kind, string) (pfmcall.Target, int, string, error)
	}
	var target pfmcall.Target
	var code int
	var detail string
	if resolver, ok := callEngine.(kindResolver); ok {
		target, code, detail, err = resolver.ResolveKind(ctx, name, kind, requiredEngine)
	} else if requiredEngine != "" {
		target, code, detail, err = callEngine.ResolveEngine(ctx, name, requiredEngine)
	} else {
		target, code, detail, err = callEngine.Resolve(ctx, name)
	}
	if err != nil {
		return nil, ResolveOutput{}, err
	}
	status := "ok"
	switch code {
	case 0:
	case pfmcall.CodeUnknown:
		status, code = statusNotFound, 1
	case pfmcall.CodeAmbiguous:
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

func (service *Service) chatCall(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input CallInput,
) (*mcp.CallToolResult, CallOutput, error) {
	callEngine, caller, err := service.callEngineForRequest(ctx, request)
	if err != nil {
		return nil, CallOutput{}, err
	}
	if refused, detail := service.selfCallerRefusal(caller); selfTarget(input.Target) && refused {
		return nil, CallOutput{
			Status: statusNotFound, Code: pfmcall.CodeUnknown, Message: detail,
		}, nil
	}
	result, err := callEngine.Call(ctx, pfmcall.Request{
		Target:   input.Target,
		Message:  input.Message,
		ForceNow: input.ForceNow,
		Then:     input.Then,
	})
	output := outputFromCall(result)
	if caller.present && !caller.valid && caller.detail != "" &&
		strings.Contains(output.Message, pfmcall.ErrUnsigned.Error()) {
		// The request did carry an identity that failed to bind: name why, so
		// a binding failure never reads as "no identity was sent".
		output.Message += " The request's own caller identity did not bind: " + caller.detail
	}
	return nil, output, err
}

// mcpUnsignedMessage restates pfmcall.ErrUnsigned's refusal for an MCP
// caller. The engine's own wording (call/body.go) is CLI-oriented — it
// tells the reader to set an environment variable or pass --allow-unsigned,
// both unreachable from an MCP tool call, and it blames a detached process
// chain that is not what happened here. The daemon-level cause is real (this
// process still derived no identity of its own), so only the remedy half is
// replaced with one an MCP caller can act on.
func mcpUnsignedMessage() string {
	return pfmcall.ErrUnsigned.Error() +
		": this process derived no identity of its own, so the recipient " +
		"would be asked to act on an instruction from nobody. " +
		noAmbientCallerRemedy
}

func outputFromCall(result pfmcall.Result) CallOutput {
	message := result.Message
	if strings.Contains(message, pfmcall.ErrUnsigned.Error()) {
		message = mcpUnsignedMessage()
	}
	return CallOutput{
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
	callEngine, caller, err := service.callEngineForRequest(ctx, request)
	if err != nil {
		return nil, CaptureOutput{}, err
	}
	if refused, detail := service.selfCallerRefusal(caller); selfTarget(input.Target) && refused {
		return nil, CaptureOutput{Status: statusNotFound, Code: pfmcall.CodeUnknown, Message: detail}, nil
	}
	target, text, code, detail, err := callEngine.Capture(
		ctx,
		input.Target,
		lines,
	)
	// One status per engine code, never a catch-all: pfmcall.Engine.Capture
	// answers CodeDead for a pane it resolved and could not read, and folding
	// that into not_found reported a live chat as one that does not exist.
	// An unregistered code is a capture that FAILED for a reason this surface
	// has not been taught yet (statusError) — still never an absence.
	status := "ok"
	switch code {
	case 0:
	case pfmcall.CodeAmbiguous:
		status = statusAmbiguous
	case pfmcall.CodeUnknown:
		status = statusNotFound
	case pfmcall.CodeDead:
		status = statusDead
	case pfmcall.CodeCaptureFailed:
		// Every other case above describes the TARGET pane and stays a
		// legitimate soft answer (err untouched, nil). CodeCaptureFailed
		// means Engine.Capture's tmux call never ran at all
		// (pfmtmux.CouldNotRun) — this tool's OWN execution failed, so it
		// surfaces as a real Go error the way chatOpenDetached and
		// cliAction (actions.go) already return one for their statusError
		// outcomes, instead of asking the caller to notice a JSON field.
		status = statusError
		err = fmt.Errorf("capture: %s", detail)
	default:
		status = statusError
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
