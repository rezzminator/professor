package chat

import (
	"context"
	"io"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestResolvedSelfIsRequestLocalAndExact(t *testing.T) {
	testjail.Fleet(t)
	want := headless.Chat{
		Name: "second", ID: "split-second", Engine: "cc", Path: "/jail/second.jsonl",
		CWD: "/work/second", Socket: "cc-shared", Session: "renamed", Pane: "%8", Live: true,
	}
	ctx := WithResolvedSelf(context.Background(), want)
	for _, name := range []string{"self", "me", " self ", `"me"`} {
		got, found, err := Resolve(ctx, name, io.Discard, nil)
		if err != nil || !found || got != want {
			t.Fatalf("Resolve(%q) = %+v found=%t err=%v, want exact scoped chat %+v", name, got, found, err, want)
		}
	}
	if _, found, err := Resolve(ctx, "somebody-else", io.Discard, nil); err != nil || found {
		t.Fatalf("explicit target used scoped self: found=%t err=%v", found, err)
	}
}
