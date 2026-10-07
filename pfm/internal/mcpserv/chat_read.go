package mcpserv

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/chat"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func (service *Service) chatFind(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input FindInput,
) (*mcp.CallToolResult, FindOutput, error) {
	self := ""
	if !input.IncludeSelf {
		caller, err := service.backend.callerForRequest(ctx, requestMeta(request))
		if err != nil {
			return nil, FindOutput{}, err
		}
		switch {
		case caller.valid && caller.row.Engine == pfmengine.Claude && caller.identity.ID != "":
			self = caller.identity.ID
		case !caller.present && service.backend.allowAmbientIdentity:
			self = chat.AskingSession()
		}
	}
	output, err := service.backend.find(ctx, input, self)
	return nil, output, err
}

func (service *Service) chatRead(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input ReadInput,
) (*mcp.CallToolResult, ReadOutput, error) {
	var err error
	ctx, input.Source, err = service.cliTargetForRequest(ctx, request, input.Source)
	if err != nil {
		return nil, ReadOutput{}, err
	}
	output, err := service.backend.read(ctx, input)
	return nil, output, err
}
