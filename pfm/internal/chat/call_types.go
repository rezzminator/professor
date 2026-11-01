package chat

import pfmcall "github.com/rezzminator/professor/pfm/internal/call"

// The call engine's vocabulary, re-exported so a door above the chat verbs
// (the MCP server) reaches a pane through this package alone.
type (
	// CallEngine is the guarded call transport; it is the production SeatDoor.
	CallEngine = pfmcall.Engine
	// CallDependencies configures NewCallEngineWith.
	CallDependencies = pfmcall.Dependencies
	// CallTarget is one resolved seat: backend, socket and pane.
	CallTarget = pfmcall.Target
	// CallRequest is one call delivery.
	CallRequest = pfmcall.Request
	// CallResult is a call delivery's outcome.
	CallResult = pfmcall.Result
	// CallNameResolver is call's fleet roster rung.
	CallNameResolver = pfmcall.NameResolver
	// CommandThenSpawner runs a call's detached then-waiter through the pfm binary.
	CommandThenSpawner = pfmcall.CommandThenSpawner
)

// The call engine's result codes a door maps onto its own statuses.
const (
	CodeAmbiguous     = pfmcall.CodeAmbiguous
	CodeUnknown       = pfmcall.CodeUnknown
	CodeDead          = pfmcall.CodeDead
	CodeUndelivered   = pfmcall.CodeUndelivered
	CodeCaptureFailed = pfmcall.CodeCaptureFailed
)

// ErrUnsigned is call's refusal to send a message it could not sign.
var ErrUnsigned = pfmcall.ErrUnsigned

// NewCallEngineWith builds a call engine from explicit dependencies: the zero
// value is the bare per-seat door the keys and capture verbs send through.
func NewCallEngineWith(dependencies CallDependencies) (*CallEngine, error) {
	return pfmcall.New(dependencies)
}
