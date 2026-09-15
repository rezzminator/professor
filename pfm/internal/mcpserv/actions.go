package mcpserv

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hostops/pfm/internal/chat"
	pfmengine "hostops/pfm/internal/engine"
)

const chatCommand = "chat"

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

func (service *Service) chatOpen(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input TargetInput,
) (*mcp.CallToolResult, ActionOutput, error) {
	target, err := service.cliTargetForRequest(ctx, request, input.Target)
	if err != nil {
		return nil, ActionOutput{}, err
	}
	return service.cliTargetAction(ctx, "open", target)
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
	if !caller.present {
		if !service.backend.allowAmbientIdentity {
			return "", fmt.Errorf("resolve MCP self: %s", noAmbientCallerRemedy)
		}
		return target, nil
	}
	if !caller.valid {
		return "", fmt.Errorf("resolve MCP self: %s", caller.detail)
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
		output := ActionOutput{Status: "error", Code: 1}
		return nil, output, fmt.Errorf("chat action in-process CLI dispatcher is not configured")
	}
	var stdout, stderr strings.Builder
	code := service.backend.dispatch(ctx, args, &stdout, &stderr)
	if code != 0 {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = strings.TrimSpace(stdout.String())
		}
		output := ActionOutput{Status: "error", Code: code, Message: message}
		return nil, output, fmt.Errorf("pfm %s exited %d: %s", strings.Join(args, " "), code, message)
	}
	return nil, ActionOutput{Status: "ok", Code: 0, Message: strings.TrimSpace(stdout.String())}, nil
}
