package chat

import (
	"context"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/spawn"
)

type serverKey struct{}

// withoutCaller marks a door that opens for no terminal (a schedule's wake,
// an MCP open): unless the request carries its caller, the chat is placed by
// its own recorded host, never by the process that runs the door.
func withoutCaller(ctx context.Context, door string) context.Context {
	if _, carried := ctx.Value(callerKey{}).(birthCaller); carried {
		return ctx
	}
	return WithCaller(ctx, door, resolve.Identity{}, spawn.ErrNoCaller)
}

// WithPlacement carries a placement already made to the rest of its request
// (a door that hands its birth to another, like shim.cx to chat new), so no
// later step of the same request asks again.
func WithPlacement(ctx context.Context, choice spawn.BackendChoice) context.Context {
	return context.WithValue(ctx, placedKey{}, choice)
}

// WithHerdrServer carries the Herdr server a door's own environment names (a
// launcher handed its environment), read in place of this process's.
func WithHerdrServer(ctx context.Context, server spawn.HerdrServer) context.Context {
	return context.WithValue(ctx, serverKey{}, server)
}

// WithLocalCaller carries a door whose caller is the process running it (a
// stdio MCP server, launched in its engine's own process tree): its birth
// identifies that process, as a CLI birth does, never "no caller".
func WithLocalCaller(ctx context.Context, door string) context.Context {
	return context.WithValue(ctx, callerKey{}, birthCaller{door: door, local: true})
}

// localCaller identifies the process running the door, once, with Codex
// seats; spawn.ProvenCaller reads the lookup against its environment, so a
// Herdr pane claim or an engine's shell no pane proves is never "outside Herdr".
func localCaller(ctx context.Context, runtime *pfmconfig.Runtime) (resolve.Identity, error) {
	identifier, err := resolve.NewWhoami(resolve.WhoamiDependencies{CodexSeats: CodexSeats(runtime)})
	if err != nil {
		return resolve.Identity{}, err
	}
	identity, err := identifier.Identify(ctx)
	return identity, spawn.ProvenCaller(resolve.CallerEnvironment(), identity, err)
}
