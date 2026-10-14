package picker

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/store"
)

// TestCachedFirstFrameDrawsNoStarForAResumableChatWithComms pins the cached
// first frame — the one a cold start paints before any gather has run, and the
// only snapshot that can outlive a reboot on screen. It composes an empty
// gather, so it holds no live row; a comms event naming two resumable chats
// (the ledger keeps 24h, so every chat that died in the crash has them) must
// therefore draw no star either.
func TestCachedFirstFrameDrawsNoStarForAResumableChatWithComms(t *testing.T) {
	jailTest(t)
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	const first, second = "aaaaaaaa-0000-4000-8000-000000000001", "aaaaaaaa-0000-4000-8000-000000000002"
	for index, uuid := range []string{first, second} {
		if err := database.UpsertTranscript(context.Background(), store.Transcript{
			UUID: uuid, Path: "/jail/" + uuid + ".jsonl", Size: 1024, MTimeNS: int64(index + 1),
			CWD: cwd, FirstPrompt: "resumable " + uuid, PromptCount: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	nowNS := time.Now().UnixNano()
	comms := &fakeCommsReader{events: []fleetdb.CommsEvent{{
		AtNS: nowNS - int64(time.Minute), Kind: fleetdb.KindCall,
		SenderUUID: first, Target: second, Message: "before the crash",
	}}}
	cached, err := scanFleetCached(context.Background(), database, scanRequest{Comms: comms})
	if err != nil {
		t.Fatal(err)
	}
	listed := 0
	for _, row := range cached.Snapshot.Rows {
		if row.ID == first || row.ID == second {
			listed++
		}
	}
	if listed != 2 {
		t.Fatalf("the cached frame lists %d of the 2 resumable chats: the fixture proves nothing", listed)
	}
	if comms.limit == 0 {
		t.Fatal("the cached frame never read the comms ledger: the fixture proves nothing")
	}
	if nodes := cached.Snapshot.Cosmos.Nodes; len(nodes) != 0 {
		t.Fatalf("cached first frame draws %d star(s) for chats with no live pane: %#v", len(nodes), nodes)
	}
}
