package mcpserv

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hostops/pfm/internal/chat"
	pfmconfig "hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
)

const chatCommand = "chat"

// statusError is an ActionOutput's failure status, beside server.go's
// statusNotFound and statusAmbiguous: the verb did not run. A chat that is
// simply not there is statusNotFound — absence and a step that could not run
// are never the same answer.
const statusError = "error"

func (service *Service) chatLast(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input LastInput,
) (*mcp.CallToolResult, LastOutput, error) {
	if strings.TrimSpace(input.Target) == "" {
		return nil, LastOutput{}, fmt.Errorf("target is required")
	}
	if service.backend.chat == nil {
		return nil, LastOutput{}, fmt.Errorf("chat_last verb is not configured")
	}
	target, err := service.cliTargetForRequest(ctx, request, input.Target)
	if err != nil {
		return nil, LastOutput{}, err
	}
	result, err := service.backend.chat.Last(ctx, chat.LastRequest{Target: target})
	if err != nil {
		return nil, LastOutput{}, fmt.Errorf("chat_last: %w", err)
	}
	text := strings.TrimRight(result.Text, "\r\n")
	if text == "" {
		return nil, LastOutput{}, fmt.Errorf("chat_last: %q returned an empty answer", target)
	}
	return nil, LastOutput{Target: input.Target, Text: text}, nil
}

func (service *Service) chatStatus(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input StatusInput,
) (*mcp.CallToolResult, StatusOutput, error) {
	if strings.TrimSpace(input.Target) == "" {
		return nil, StatusOutput{}, fmt.Errorf("target is required")
	}
	if !input.Summary && !input.Ask && (input.Engine != "" || input.Model != "") {
		return nil, StatusOutput{}, fmt.Errorf("engine and model require summary=true or ask=true")
	}
	var engine pfmengine.ID
	if input.Engine != "" {
		parsed, err := pfmengine.Parse(input.Engine)
		if err != nil {
			return nil, StatusOutput{}, fmt.Errorf("chat_status: %w", err)
		}
		engine = parsed
	}
	if service.backend.chat == nil {
		return nil, StatusOutput{}, fmt.Errorf("chat_status verb is not configured")
	}
	target, err := service.cliTargetForRequest(ctx, request, input.Target)
	if err != nil {
		return nil, StatusOutput{}, err
	}
	// A dead chat is a status, not an error: it comes back with its state.
	status, err := service.backend.chat.Status(ctx, chat.StatusRequest{
		Target: target, Summary: input.Summary, Ask: input.Ask,
		Engine: engine, Model: input.Model,
	})
	if err != nil {
		return nil, StatusOutput{}, fmt.Errorf("chat_status: %w", err)
	}
	return nil, StatusOutput(status), nil
}

func (service *Service) chatNew(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input NewInput,
) (*mcp.CallToolResult, ActionOutput, error) {
	if strings.TrimSpace(input.Name) == "" {
		return nil, ActionOutput{}, fmt.Errorf("name is required")
	}
	args := []string{chatCommand, "new", "--name", input.Name}
	if input.Engine != "" {
		args = append(args, "--engine", input.Engine)
	}
	if input.CWD != "" {
		args = append(args, "--cwd", input.CWD)
	} else {
		// Canonical dir: a chat spawned over MCP is born in its CALLER's
		// project directory, not wherever the MCP server process happens to
		// sit — the same request-scoped identity every caller-bound tool
		// resolves from. An
		// unresolvable caller keeps the explicit-only behaviour (no --cwd)
		// rather than guessing a home.
		caller, err := service.backend.callerForRequest(ctx, requestMeta(request))
		if err != nil {
			return nil, ActionOutput{}, err
		}
		if caller.valid && strings.TrimSpace(caller.row.Dir) != "" {
			args = append(args, "--cwd", caller.row.Dir)
		}
	}
	if input.Account != 0 {
		args = append(args, "--account", fmt.Sprint(input.Account))
	}
	if input.Cache1H {
		args = append(args, "--1h")
	}
	if input.Model != "" {
		args = append(args, "--model", input.Model)
	}
	if input.Effort != "" {
		args = append(args, "--effort", input.Effort)
	}
	if input.Await {
		args = append(args, "--await")
	}
	if input.Timeout != 0 {
		args = append(args, "--timeout", fmt.Sprint(input.Timeout))
	}
	if input.Settle != 0 {
		args = append(args, "--settle", fmt.Sprint(input.Settle))
	}
	if input.Progress {
		args = append(args, "--progress")
	}
	if input.Attach {
		args = append(args, "--attach")
	}
	if input.Prompt != "" {
		args = append(args, input.Prompt)
	}
	return service.cliAction(ctx, args...)
}

// chatOpen never routes through cliTargetAction/Dispatch: that seam ends in
// action.Dispatch, whose K1 non-terminal branch only ever prints the eval
// line for a shell wrapper to `eval` — the MCP daemon has no shell reading
// its stdout, so the line went nowhere and nothing opened. chat_open instead
// takes chat.OpenDetachedID, the verb layer's own door for a caller with no
// terminal to attach: the same resolution and the same preparation `pfm chat
// open` runs, ending in action.Executor.OpenDetached instead of an eval line.
func (service *Service) chatOpen(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input TargetInput,
) (*mcp.CallToolResult, ActionOutput, error) {
	target, err := service.cliTargetForRequest(ctx, request, input.Target)
	if err != nil {
		return nil, ActionOutput{}, err
	}
	return service.chatOpenDetached(ctx, target)
}

func (service *Service) chatOpenDetached(
	ctx context.Context,
	target string,
) (*mcp.CallToolResult, ActionOutput, error) {
	if strings.TrimSpace(target) == "" {
		return nil, ActionOutput{}, fmt.Errorf("target is required")
	}
	// The daemon carries only a thin projection of the machine's config
	// (server.go's Runtime); a real open needs the FULL config — every
	// configured Codex/OpenCode account among them — so it is loaded fresh
	// here, exactly as a bare CLI invocation would load it.
	effective, err := pfmconfig.LoadRuntime("")
	if err != nil {
		return nil, ActionOutput{}, fmt.Errorf("chat_open: load machine config: %w", err)
	}
	// Resolved exactly as `pfm chat open` resolves it — by name, id prefix or
	// socket — because a name is how a caller addresses a chat. A door
	// matching only the indexed id answers a perfectly good name with an
	// absence.
	resolved, err := chat.Target(ctx, target, &effective)
	if err != nil {
		// Absence and a fleet that could not be read are different answers:
		// not_found means no such chat, error means the lookup itself failed.
		status, code := statusError, 2
		if errors.Is(err, chat.ErrUnknownChat) {
			status, code = statusNotFound, 1
		}
		output := ActionOutput{Status: status, Code: code, Message: err.Error()}
		return nil, output, fmt.Errorf("chat_open: %w", err)
	}
	result, err := chat.OpenDetachedID(ctx, resolved.ID, service.backend.warnings, &effective)
	if err != nil {
		output := ActionOutput{Status: statusError, Code: 1, Message: err.Error()}
		return nil, output, fmt.Errorf("chat_open: %w", err)
	}
	message := fmt.Sprintf("%s %s on socket %s", result.State, result.Name, result.Socket)
	if result.Detail != "" {
		message += " — " + result.Detail
	}
	return nil, ActionOutput{Status: "ok", Code: 0, Message: message}, nil
}

func (service *Service) chatName(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input NameInput,
) (*mcp.CallToolResult, ActionOutput, error) {
	if strings.TrimSpace(input.Name) == "" || strings.ContainsAny(input.Name, "\r\n\x00") {
		return nil, ActionOutput{}, fmt.Errorf("name must be one non-empty line")
	}
	target, err := service.cliTargetForRequest(ctx, request, input.Target)
	if err != nil {
		return nil, ActionOutput{}, err
	}
	return service.cliAction(ctx, chatCommand, "name", target, input.Name)
}

func (service *Service) chatKill(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input KillInput,
) (*mcp.CallToolResult, ActionOutput, error) {
	target, err := service.cliTargetForRequest(ctx, request, input.Target)
	if err != nil {
		return nil, ActionOutput{}, err
	}
	args := []string{chatCommand, "kill", target}
	if input.Exit {
		args = append(args, "--exit")
	}
	return service.cliAction(ctx, args...)
}

func (service *Service) chatUnkill(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input TargetInput,
) (*mcp.CallToolResult, ActionOutput, error) {
	target, err := service.cliTargetForRequest(ctx, request, input.Target)
	if err != nil {
		return nil, ActionOutput{}, err
	}
	return service.cliTargetAction(ctx, "unkill", target)
}

func (service *Service) chatSave(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input SaveInput,
) (*mcp.CallToolResult, ActionOutput, error) {
	// A bare word here is almost always a chat name reached for by habit, and
	// obeying it writes a transcript into a file of that name beside whatever
	// directory the server happens to sit in. Demand a path shape instead.
	if !strings.ContainsRune(input.Target, filepath.Separator) {
		return nil, ActionOutput{}, fmt.Errorf(
			"chat_save target %q is not a file path: this verb appends a transcript to a FILE, "+
				"not to a chat — pass a path such as ./%s.md",
			input.Target, strings.TrimSpace(input.Target),
		)
	}
	target, err := service.cliTargetForRequest(ctx, request, input.Target)
	if err != nil {
		return nil, ActionOutput{}, err
	}
	args := []string{chatCommand, "save", target}
	if input.Transcript != "" {
		args = append(args, input.Transcript)
	}
	return service.cliAction(ctx, args...)
}

// cliTargetForRequest translates a request-scoped Codex self into the stable
// thread id understood by the in-process CLI dispatcher. The HTTP daemon has
// no tmux ancestry of its own, so forwarding the literal word "self" asks the
// daemon who it is and necessarily resolves nothing.
func (service *Service) cliTargetForRequest(
	ctx context.Context,
	request *mcp.CallToolRequest,
	target string,
) (string, error) {
	if !selfTarget(target) {
		return target, nil
	}
	caller, err := service.backend.callerForRequest(ctx, requestMeta(request))
	if err != nil {
		return "", err
	}
	// The present/valid/ambient tri-state is decided in exactly one place —
	// selfCallerRefusal (server.go) — so this door and every handler that
	// refuses "self" can never drift apart on what counts as identity.
	if refused, detail := service.selfCallerRefusal(caller); refused {
		return "", fmt.Errorf("resolve MCP self: %s", detail)
	}
	if !caller.valid {
		// Not refused and not valid is the stdio server's ambient case: the
		// word "self" travels on to the CLI, which derives the identity from
		// the process that launched this server.
		return target, nil
	}
	return caller.identity.ID, nil
}

func (service *Service) cliTargetAction(
	ctx context.Context,
	verb, target string,
) (*mcp.CallToolResult, ActionOutput, error) {
	if strings.TrimSpace(target) == "" {
		return nil, ActionOutput{}, fmt.Errorf("target is required")
	}
	return service.cliAction(ctx, chatCommand, verb, target)
}

func (service *Service) cliAction(ctx context.Context, args ...string) (*mcp.CallToolResult, ActionOutput, error) {
	if service.backend.dispatch == nil {
		output := ActionOutput{Status: statusError, Code: 1}
		return nil, output, fmt.Errorf("chat action in-process CLI dispatcher is not configured")
	}
	var stdout, stderr strings.Builder
	code := service.backend.dispatch(ctx, args, &stdout, &stderr)
	if code != 0 {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = strings.TrimSpace(stdout.String())
		}
		output := ActionOutput{Status: statusError, Code: code, Message: message}
		return nil, output, fmt.Errorf("pfm %s exited %d: %s", strings.Join(args, " "), code, message)
	}
	// A verb that exited 0 can still have written a warning — `pfm chat kill`
	// says on stderr when it only de-listed a row. Dropping it here is how the
	// caller came to read a bare "killed <id>" for a chat nothing was closed
	// for: an incomplete answer that reads like a complete one.
	message := strings.TrimSpace(stdout.String())
	if warning := strings.TrimSpace(stderr.String()); warning != "" {
		if message == "" {
			message = warning
		} else {
			message += "\n" + warning
		}
	}
	return nil, ActionOutput{Status: "ok", Code: 0, Message: message}, nil
}
