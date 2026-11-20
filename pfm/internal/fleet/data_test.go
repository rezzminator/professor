package fleet

import (
	"context"
	"reflect"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestRolloutIDFromPathStripsTheTimestampPrefix pins the rollout file name
// grammar: the thread id is what follows the 19-character timestamp.
func TestRolloutIDFromPathStripsTheTimestampPrefix(t *testing.T) {
	for _, test := range []struct{ path, want string }{
		{"/r/2026/01/02/rollout-2026-01-02T03-04-05-0199aaaa-bbbb-7ccc-8ddd-eeeeeeeeeeee.jsonl", "0199aaaa-bbbb-7ccc-8ddd-eeeeeeeeeeee"},
		{"rollout-short.jsonl", "short"},
		{"/r/2026/01/02/rollout-2026-01-02T03-04-05-0199aaaa-bbbb-7ccc-8ddd-eeeeeeeeeeee.jsonl.zst", "0199aaaa-bbbb-7ccc-8ddd-eeeeeeeeeeee"},
		{"", ""},
	} {
		if got := rolloutIDFromPath(test.path); got != test.want {
			t.Errorf("rolloutIDFromPath(%q) = %q, want %q", test.path, got, test.want)
		}
	}
}

// TestEnrichLiveLeavesCompleteDataUntouched pins the no-op path: when every
// live chat is already loaded, enrichment asks the index nothing (the nil
// database would fail the call) and returns the data as given. A rollout-less
// Codex process has no id to look up at all.
func TestEnrichLiveLeavesCompleteDataUntouched(t *testing.T) {
	data := Data{
		Transcripts: []store.Transcript{{UUID: "claude-1"}},
		Rollouts:    []store.Rollout{{ID: "codex-1"}},
	}
	live := gather.Snapshot{
		Agents: []gather.Agent{{SessionID: "claude-1"}},
		Codex:  []gather.LiveCodex{{RolloutPath: "rollout-2026-01-02T03-04-05-codex-1.jsonl"}, {RolloutPath: ""}},
	}
	got, err := EnrichLive(context.Background(), nil, data, live)
	if err != nil {
		t.Fatalf("EnrichLive() = %v", err)
	}
	if len(got.Transcripts) != 1 || len(got.Rollouts) != 1 {
		t.Fatalf("EnrichLive() = %+v, want the data unchanged", got)
	}
}

// TestLoadDataFilesEachSpaceUnderTheIDsItsChatIsListedBy pins that both loads
// hand compose a recorded space under every id a row of that chat carries: a
// live Herdr row's id is its seat's session id, the one `pfm chat space`
// records, and the open of a resumable row births its seat under the row's id,
// so a chat keeps its space through end and resume only while its resumable
// row is listed by a key that resolves to the record. A Claude chat continued
// into a new session resumes by the tail, a Codex chat by its lineage root, so
// those keys carry the record too; the newest record among a chat's ids wins,
// a cleared one included. A record of a chat the index no longer holds is
// dropped, so a stale space reserves no label.
func TestLoadDataFilesEachSpaceUnderTheIDsItsChatIsListedBy(t *testing.T) {
	database, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	ctx := context.Background()
	cwd := testjail.Place("/jail/work/orchard")
	for _, transcript := range []store.Transcript{
		{UUID: "cc-plain"},
		{UUID: "cc-root", ContinuedIn: "cc-tail"},
		{UUID: "cc-tail"},
		{UUID: "cc-cleared-root", ContinuedIn: "cc-cleared-tail"},
		{UUID: "cc-cleared-tail"},
	} {
		transcript.Path, transcript.CWD, transcript.PromptCount = testjail.Place(
			"/jail/"+transcript.UUID+".jsonl",
		), cwd, 2
		if err := database.UpsertTranscript(ctx, transcript); err != nil {
			t.Fatal(err)
		}
	}
	for _, rollout := range []store.Rollout{
		{ID: "cx-root", MTimeNS: 1},
		{ID: "cx-thread", ParentThread: "cx-root", MTimeNS: 2},
	} {
		rollout.Path, rollout.CWD, rollout.PromptCount, rollout.UserThread = testjail.Place(
			"/jail/"+rollout.ID+".jsonl"), cwd, 2, true
		if err := database.UpsertRollout(ctx, rollout); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.ReplaceOpenCodeSessions(
		ctx,
		[]store.OpenCodeSession{{ID: "oc-plain", Directory: cwd}},
	); err != nil {
		t.Fatal(err)
	}
	for _, record := range []struct {
		id, space string
		at        int64
	}{
		{"cc-plain", "glass", 1},
		{"cc-root", "glass", 1},
		{"cc-cleared-root", "glass", 1},
		{"cc-cleared-tail", "glass", 2},
		{"cc-cleared-tail", "", 3},
		{"cx-thread", "glass", 1},
		{"oc-plain", "glass", 1},
		{"gone-chat", "epic", 1},
	} {
		if err := database.Shared().SetSpace(ctx, record.id, record.space, record.at); err != nil {
			t.Fatal(err)
		}
	}
	want := map[string]string{
		"cc-plain": "glass", "cc-root": "glass", "cc-tail": "glass", "cx-thread": "glass", "cx-root": "glass",
		"oc-plain": "glass",
	}
	for name, load := range map[string]func(context.Context, *store.Store) (Data, error){
		"all":     LoadData,
		"default": LoadDefaultData,
	} {
		data, err := load(ctx, database)
		if err != nil || !reflect.DeepEqual(data.Spaces, want) {
			t.Fatalf("%s: spaces = %v, %v; want %v", name, data.Spaces, err, want)
		}
	}
	data, err := LoadData(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	output := compose.Compose(compose.Input{
		Transcripts: data.Transcripts, Rollouts: data.Rollouts, OpenCodeSessions: data.OpenCodeSessions,
		Spaces: data.Spaces, Options: compose.Options{View: compose.AllView},
	})
	got := map[string]string{}
	for _, row := range output.Rows {
		if row.Kind.IsResumable() {
			got[row.ID] = row.Space
		}
	}
	// The all view lists a continued chat's earlier segment too; it wears the chat's space.
	wantRows := map[string]string{
		"cc-plain": "glass", "cc-root": "glass", "cc-tail": "glass", "cc-cleared-root": "", "cc-cleared-tail": "",
		"cx-root": "glass", "oc-plain": "glass",
	}
	if !reflect.DeepEqual(got, wantRows) {
		t.Fatalf("resumable rows' spaces = %v, want %v", got, wantRows)
	}
}
