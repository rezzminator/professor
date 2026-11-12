package chat

import (
	"context"
	"io"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// Verbs binds the verb functions to one process's runtime, for a surface that
// holds a handle instead of threading a runtime through every call — the MCP
// server. Each method is the package function of the same name.
type Verbs struct {
	Runtime  *pfmconfig.Runtime
	Warnings io.Writer
}

// Last is chat.Last over the bound runtime.
func (verbs Verbs) Last(ctx context.Context, request LastRequest) (LastResult, error) {
	return LastAnswer(ctx, verbs.Runtime, request)
}

// Status is chat.Status over the bound runtime.
func (verbs Verbs) Status(ctx context.Context, request StatusRequest) (headless.Status, error) {
	return Status(ctx, verbs.Runtime, request, verbs.warnings())
}

// List is chat.List over the bound runtime.
func (verbs Verbs) List(ctx context.Context, request ListRequest) (ListResult, error) {
	return List(ctx, verbs.Runtime, request, verbs.warnings())
}

// Find is chat.Find over the bound runtime.
func (verbs Verbs) Find(ctx context.Context, request FindRequest) ([]TranscriptMatch, error) {
	return Find(ctx, verbs.Runtime, request)
}

// Read is chat.ReadEntries over the bound runtime.
func (verbs Verbs) Read(ctx context.Context, target string, tail int) (headless.Chat, []transcript.Entry, bool, error) {
	return ReadEntries(ctx, target, tail, verbs.Runtime)
}

// Exit is chat.ExitResolved over the bound runtime.
func (verbs Verbs) Exit(ctx context.Context, self headless.Chat) (string, error) {
	return ExitResolved(ctx, verbs.Runtime, self)
}

// Capture is chat.Capture over the bound runtime.
func (verbs Verbs) Capture(ctx context.Context, target string, tail int) (Captured, error) {
	return Capture(ctx, verbs.Runtime, target, tail)
}

// Keys is chat.PressKeys over the bound runtime.
func (verbs Verbs) Keys(ctx context.Context, target string, request KeysRequest) (Keyed, error) {
	return PressKeys(ctx, verbs.Runtime, target, request)
}

// CallAnswer is chat.CallAnswer over the bound runtime.
func (verbs Verbs) CallAnswer(ctx context.Context, result CallResult, name string) (CallResult, string, bool) {
	return CallAnswer(ctx, result, name, verbs.Runtime)
}

// Inspect is chat.InspectSeat of a listed row over the bound runtime, now.
func (verbs Verbs) Inspect(ctx context.Context, row compose.Row) (headless.Status, error) {
	return InspectSeat(ctx, verbs.Runtime, FromRow(row), clock.Real.Now(), nil)
}

func (verbs Verbs) warnings() io.Writer {
	if verbs.Warnings == nil {
		return io.Discard
	}
	return verbs.Warnings
}
