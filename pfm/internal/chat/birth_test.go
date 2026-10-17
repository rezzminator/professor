package chat

import (
	"context"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/spawn"
)

func TestBirthBackendHonorsRequestBoundary(t *testing.T) {
	env := &paths.MapEnv{
		Values: map[string]string{
			"HERDR_ENV":         "1",
			"HERDR_SOCKET_PATH": "/unreachable/herdr.sock",
			"HERDR_PANE_ID":     "w6:p3",
		},
	}
	for _, ctx := range []context.Context{
		spawn.WithBackendEnvironment(context.Background(), nil),
		WithResolvedSelf(context.Background(), headless.Chat{Backend: "tmux"}),
		WithResolvedSelf(context.Background(), headless.Chat{}),
	} {
		choice, err := BirthBackend(ctx, "auto", env)
		if err != nil || choice.Backend != "tmux" || choice.Warning != "" {
			t.Fatalf("request boundary choice=%+v err=%v", choice, err)
		}
	}
}
