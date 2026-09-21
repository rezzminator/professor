package mcpserv

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/chat"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/resolve"
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
	ctx, target, err := service.cliTargetForRequest(ctx, request, input.Target)
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
	ctx, target, err := service.cliTargetForRequest(ctx, request, input.Target)
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
	caller, err := service.backend.callerForRequest(ctx, requestMeta(request))
	if err != nil {
		return nil, ActionOutput{}, err
	}
	if !caller.valid && caller.present {
		if refused, detail := service.selfCallerRefusal(caller); refused {
			return nil, ActionOutput{}, fmt.Errorf("chat_new: resolve caller: %s", detail)
		}
	}
	var self headless.Chat
	if caller.valid {
		self, err = service.resolvedSelf(ctx, caller)
		if err != nil {
			return nil, ActionOutput{}, fmt.Errorf("chat_new: resolve caller: %w", err)
		}
		ctx = chat.WithResolvedSelf(ctx, self)
	}
	if input.Engine != "" {
		args = append(args, "--engine", input.Engine)
	} else if caller.valid && caller.row.Engine != "" {
		args = append(args, "--engine", string(caller.row.Engine))
	}
	if input.CWD != "" {
		directory := input.CWD
		if caller.valid && !filepath.IsAbs(directory) {
			if strings.TrimSpace(self.CWD) == "" {
				return nil, ActionOutput{}, fmt.Errorf(
					"chat_new: caller working directory is required to resolve relative cwd %q",
					directory,
				)
			}
			if !filepath.IsAbs(self.CWD) {
				return nil, ActionOutput{}, fmt.Errorf(
					"chat_new: caller working directory %q is not absolute",
					self.CWD,
				)
			}
			directory = filepath.Join(self.CWD, directory)
		}
		args = append(args, "--cwd", directory)
	} else if caller.valid && strings.TrimSpace(self.CWD) != "" {
		args = append(args, "--cwd", self.CWD)
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
	ctx, target, err := service.cliTargetForRequest(ctx, request, input.Target)
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
	result, err := chat.OpenDetachedTarget(ctx, resolved, service.backend.warnings, &effective)
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
	ctx, target, err := service.cliTargetForRequest(ctx, request, input.Target)
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
	ctx, target, err := service.cliTargetForRequest(ctx, request, input.Target)
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
	ctx, target, err := service.cliTargetForRequest(ctx, request, input.Target)
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
	target := input.Target
	transcriptPath := input.Transcript
	caller, err := service.backend.callerForRequest(ctx, requestMeta(request))
	if err != nil {
		return nil, ActionOutput{}, fmt.Errorf("chat_save: resolve caller: %w", err)
	}
	needsCaller := !filepath.IsAbs(target) || transcriptPath == "" || !filepath.IsAbs(transcriptPath)
	if !caller.valid {
		if refused, detail := service.selfCallerRefusal(caller); needsCaller && refused {
			return nil, ActionOutput{}, fmt.Errorf("chat_save: resolve caller: %s", detail)
		}
	} else {
		self, resolveErr := service.resolvedSelf(ctx, caller)
		if resolveErr != nil {
			return nil, ActionOutput{}, fmt.Errorf("chat_save: resolve caller: %w", resolveErr)
		}
		ctx = chat.WithResolvedSelf(ctx, self)
		if !filepath.IsAbs(target) || (transcriptPath != "" && !filepath.IsAbs(transcriptPath)) {
			if strings.TrimSpace(self.CWD) == "" {
				return nil, ActionOutput{}, fmt.Errorf(
					"chat_save: caller working directory is required to resolve relative paths",
				)
			}
			if !filepath.IsAbs(self.CWD) {
				return nil, ActionOutput{}, fmt.Errorf(
					"chat_save: caller working directory %q is not absolute",
					self.CWD,
				)
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(self.CWD, target)
			}
			if transcriptPath != "" && !filepath.IsAbs(transcriptPath) {
				transcriptPath = filepath.Join(self.CWD, transcriptPath)
			}
		}
		if transcriptPath == "" {
			if strings.TrimSpace(self.Path) == "" {
				return nil, ActionOutput{}, fmt.Errorf(
					"chat_save: caller transcript path is required when transcript is omitted",
				)
			}
			transcriptPath = self.Path
		}
	}
	args := []string{chatCommand, "save", target}
	if transcriptPath != "" {
		args = append(args, transcriptPath)
	}
	return service.cliAction(ctx, args...)
}

// cliTargetForRequest binds a validated request caller to context while
// preserving the literal self/me or raw-pane target consumed by typed verbs
// and the in-process CLI dispatcher. The HTTP daemon has no caller identity of
// its own, so the context carries the exact socket, pane and transcript instead.
func (service *Service) cliTargetForRequest(
	ctx context.Context,
	request *mcp.CallToolRequest,
	target string,
) (context.Context, string, error) {
	normalized := resolve.NormalizeTarget(target)
	isSelf := selfTarget(normalized)
	if !isSelf && !resolve.IsRawPane(normalized) {
		return ctx, target, nil
	}
	caller, err := service.backend.callerForRequest(ctx, requestMeta(request))
	if err != nil {
		return ctx, "", err
	}
	// The present/valid/ambient tri-state is decided in exactly one place —
	// selfCallerRefusal (server.go) — so this door and every handler that
	// refuses "self" can never drift apart on what counts as identity.
	if refused, detail := service.selfCallerRefusal(caller); refused {
		if !isSelf {
			return ctx, "", fmt.Errorf("resolve MCP raw pane: %s", detail)
		}
		return ctx, "", fmt.Errorf("resolve MCP self: %s", detail)
	}
	if !caller.valid {
		// Not refused and not valid is the stdio server's ambient case: the
		// word "self" travels on to the CLI, which derives the identity from
		// the process that launched this server.
		return ctx, target, nil
	}
	self, err := service.resolvedSelf(ctx, caller)
	if err != nil {
		return ctx, "", err
	}
	return chat.WithResolvedSelf(ctx, self), target, nil
}

func (service *Service) resolvedSelf(ctx context.Context, caller callerIdentity) (headless.Chat, error) {
	path := caller.row.transcriptPath
	engine := caller.row.Engine
	if parsed, err := pfmengine.Parse(caller.identity.Engine); err == nil {
		engine = parsed
	}
	if service.backend.database != nil && caller.identity.ID != "" {
		switch engine {
		case pfmengine.Claude:
			transcript, found, err := service.backend.database.Transcript(ctx, caller.identity.ID)
			if err != nil {
				return headless.Chat{}, fmt.Errorf("resolve MCP self transcript %q: %w", caller.identity.ID, err)
			}
			if found && strings.TrimSpace(transcript.Path) != "" {
				path = transcript.Path
			}
		case pfmengine.Codex:
			lineage, found, err := service.backend.database.CodexLineage(ctx, caller.identity.ID)
			if err != nil {
				return headless.Chat{}, fmt.Errorf("resolve MCP self rollout %q: %w", caller.identity.ID, err)
			}
			if found && strings.TrimSpace(lineage.Newest.Path) != "" {
				path = lineage.Newest.Path
			}
		}
	}
	socket := caller.identity.SocketName
	if socket == "" {
		socket = filepath.Base(caller.identity.SocketPath)
	}
	return headless.Chat{
		Name: caller.row.Name, ID: caller.identity.ID,
		Engine: engine, Path: path, CWD: caller.row.Dir,
		Socket: socket, Session: caller.identity.Session, Pane: caller.identity.Pane, Live: true,
	}, nil
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
