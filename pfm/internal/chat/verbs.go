package chat

import (
	"context"
	"io"

	pfmconfig "hostops/pfm/internal/config"
	"hostops/pfm/internal/headless"
	"hostops/pfm/internal/transcript"
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

func (verbs Verbs) warnings() io.Writer {
	if verbs.Warnings == nil {
		return io.Discard
	}
	return verbs.Warnings
}
