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
	// Refusal is a chat function's refusal as both its doors answer it.
	Refusal = pfmcall.Refusal
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

// The status words of an ambiguous name and of a step that could not run (pfmcall).
const (
	StatusAmbiguous = pfmcall.StatusAmbiguous
	StatusError     = pfmcall.StatusError
)

// LookupFailed is a target lookup that could not look, as both doors answer it (pfmcall.LookupFailed).
var LookupFailed = pfmcall.LookupFailed

// UnknownChat refuses a name no chat answers to (pfmcall.UnknownChat).
var UnknownChat = pfmcall.UnknownChat

// ExitRefusal is the refusal an exit code of `pfm chat {verb}` stands for (pfmcall.ExitRefusal).
var ExitRefusal = pfmcall.ExitRefusal

// ErrUnsigned is call's refusal to send a message it could not sign.
var ErrUnsigned = pfmcall.ErrUnsigned

// NewCallEngineWith builds a call engine from explicit dependencies: the zero
// value is the bare per-seat door the keys and capture verbs send through.
func NewCallEngineWith(dependencies CallDependencies) (*CallEngine, error) {
	return pfmcall.New(dependencies)
}
