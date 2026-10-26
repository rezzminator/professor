package mcpserv

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
)

func (service *Service) chatCall(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input CallInput,
) (*mcp.CallToolResult, CallOutput, error) {
	targets := []string{}
	for _, name := range append([]string{input.Target}, input.Targets...) {
		if name = strings.TrimSpace(name); name != "" && !slices.Contains(targets, name) {
			targets = append(targets, name)
		}
	}
	if len(targets) == 0 {
		return nil, CallOutput{}, errors.New("call needs a target or targets")
	}
	callEngine, caller, err := service.callEngineForRequest(ctx, request)
	if err != nil {
		return nil, CallOutput{}, err
	}
	if len(targets) == 1 {
		delivery, err := service.callOne(ctx, callEngine, caller, targets[0], input)
		return nil, CallOutput{CallDelivery: delivery}, err
	}
	// A broadcast reaches every target at once, so a busy one waiting for
	// idle never holds up the rest; one target's failure is its own result.
	results := make([]TargetCallOutput, len(targets))
	var wait sync.WaitGroup
	for index, target := range targets {
		wait.Go(func() {
			delivery, err := service.callOne(ctx, callEngine, caller, target, input)
			if err != nil {
				delivery = CallDelivery{Status: "error", Code: pfmcall.CodeUndelivered, Message: err.Error()}
			}
			results[index] = TargetCallOutput{Target: target, CallDelivery: delivery}
		})
	}
	wait.Wait()
	var failed []string
	for index := range results {
		if result := &results[index]; result.Code != 0 {
			failed = append(failed, fmt.Sprintf("%s (%s: %s)", result.Target, result.Status, result.Message))
		}
	}
	output := CallOutput{CallDelivery: CallDelivery{Status: "delivered"}, Results: results}
	output.Message = fmt.Sprintf("broadcast delivered %d/%d", len(targets)-len(failed), len(targets))
	if len(failed) > 0 {
		output.Status, output.Code = "partial", pfmcall.CodeUndelivered
		if len(failed) == len(targets) {
			output.Status = "undelivered"
		}
		output.Message += "; not delivered: " + strings.Join(failed, "; ")
	}
	return nil, output, nil
}

// callOne delivers one call to one target for this request's caller.
func (service *Service) callOne(
	ctx context.Context,
	callEngine callService,
	caller callerIdentity,
	target string,
	input CallInput,
) (CallDelivery, error) {
	if refusal, detail := service.selfCallerRefusal(caller); selfTarget(target) && refusal != "" {
		return CallDelivery{Status: refusal, Code: pfmcall.CodeUnknown, Message: detail}, nil
	}
	result, err := callEngine.Call(ctx, pfmcall.Request{
		Target:   target,
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
	return output, err
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

func outputFromCall(result pfmcall.Result) CallDelivery {
	message := result.Message
	if strings.Contains(message, pfmcall.ErrUnsigned.Error()) {
		message = mcpUnsignedMessage()
	}
	return CallDelivery{
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
