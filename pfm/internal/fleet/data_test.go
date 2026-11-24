package fleet

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
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

// TestCappedLoadListsTheResumableChatsOfAFullLoad pins the picker's two
// frames of one pass to the same rows: the capped default load (each pass's
// first frame, and ScanCached's) must list every resumable chat and the same
// omitted counts the full load lists. A live agent still zero bytes long, a
// live Codex pane and one native seat per engine sit among the newest chats,
// the Codex seat on a fork, which makes its whole lineage live as a live
// Codex process does; none may take a resume slot or be counted as omitted,
// so the capped read reaches past them to the oldest chats the caps still
// hold, and never further. A Codex process no pane holds is no live row, so
// its chat stays resumable; an empty OpenCode session is counted in both
// frames.
func TestCappedLoadListsTheResumableChatsOfAFullLoad(t *testing.T) {
	database, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	ctx := context.Background()
	cwd := testjail.Place("/jail/work/orchard")
	rolloutPath := func(index int) string {
		return fmt.Sprintf("/jail/codex/rollout-2026-01-02T03-04-05-cx-%02d.jsonl", index)
	}
	for index := range 34 {
		transcript := store.Transcript{
			UUID:        fmt.Sprintf("cc-%02d", index),
			Path:        fmt.Sprintf("/jail/claude/cc-%02d.jsonl", index),
			Size:        100,
			PromptCount: 1,
			MTimeNS:     int64(index + 1),
			ActivityNS:  int64(index + 1),
			CWD:         cwd,
		}
		if index == 33 {
			transcript.Size, transcript.PromptCount = 0, 0
		}
		if err := database.UpsertTranscript(ctx, transcript); err != nil {
			t.Fatal(err)
		}
	}
	// Two label-killed chats, the newest of all: one under the legacy "_HIDE"
	// prefix, one under "_KILL" lowercased. Both are hidden in both frames,
	// never a resumable candidate nor an eligible chat past the cap.
	for index, title := range []string{"_HIDE worker 1", "_kill worker 2"} {
		uuid := fmt.Sprintf("cc-%02d", 34+index)
		if err := database.UpsertTranscript(ctx, store.Transcript{
			UUID:        uuid,
			Path:        "/jail/claude/" + uuid + ".jsonl",
			Size:        100,
			PromptCount: 1,
			MTimeNS:     int64(35 + index),
			ActivityNS:  int64(35 + index),
			CWD:         cwd,
			CustomTitle: title,
		}); err != nil {
			t.Fatal(err)
		}
	}
	for index := range 19 {
		if err := database.UpsertRollout(ctx, store.Rollout{
			ID:          fmt.Sprintf("cx-%02d", index),
			Path:        rolloutPath(index),
			Size:        100,
			PromptCount: 1,
			MTimeNS:     int64(index + 1),
			CWD:         cwd,
			UserThread:  true,
		}); err != nil {
			t.Fatal(err)
		}
	}
	// The native Codex seat holds cx-19, a fork of cx-10: the newest thread
	// of the cx-10 lineage is live, though the lineage's root is not.
	if err := database.UpsertRollout(ctx, store.Rollout{
		ID:           "cx-19",
		Path:         rolloutPath(19),
		Size:         100,
		PromptCount:  1,
		MTimeNS:      20,
		CWD:          cwd,
		UserThread:   true,
		ParentThread: "cx-10",
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.ReplaceOpenCodeSessions(
		ctx,
		[]store.OpenCodeSession{{ID: "oc-empty", Directory: cwd}},
	); err != nil {
		t.Fatal(err)
	}
	live := gather.Snapshot{
		Panes:  []gather.ProbePane{{Socket: "cx-1-2-3", PaneID: "%1", CurrentPath: cwd}},
		Agents: []gather.Agent{{PID: 33, SessionID: "cc-33"}},
		Codex: []gather.LiveCodex{
			{PID: 16, Socket: "cx-1-2-3", PaneID: "%1", RolloutPath: rolloutPath(16)},
			{PID: 17, RolloutPath: rolloutPath(5)},
		},
	}
	native := []gather.HerdrSeat{
		{Seat: fleetdb.Seat{SeatID: "seat-cc", SessionID: "cc-20", Engine: "cc", Backend: "herdr", State: "live"}},
		{Seat: fleetdb.Seat{SeatID: "seat-cx", SessionID: "cx-19", Engine: "cx", Backend: "herdr", State: "live"}},
	}
	var wantClaude, wantCodex []string
	for index := 2; index <= 32; index++ {
		if index != 20 {
			wantClaude = append(wantClaude, fmt.Sprintf("cc-%02d", index))
		}
	}
	for index := 2; index <= 18; index++ {
		if index != 10 && index != 16 {
			wantCodex = append(wantCodex, fmt.Sprintf("cx-%02d", index))
		}
	}
	wantClaims := store.LiveClaims{
		Transcripts: map[string]struct{}{"cc-20": {}, "cc-33": {}},
		Rollouts:    map[string]struct{}{"cx-10": {}, "cx-16": {}},
	}
	for _, loader := range []struct {
		name string
		load func(context.Context, *store.Store) (Data, error)
	}{
		{name: "full", load: LoadData},
		{name: "capped", load: LoadDefaultData},
	} {
		t.Run(loader.name, func(t *testing.T) {
			output := composeLoaded(t, database, loader.load, compose.DefaultView, live, native...)
			var claude, codex []string
			for index := range output.Rows {
				row := &output.Rows[index]
				switch row.Kind {
				case compose.ResumeClaude:
					claude = append(claude, row.ID)
				case compose.ResumeCodex:
					codex = append(codex, row.ID)
				}
			}
			slices.Sort(claude)
			slices.Sort(codex)
			if !reflect.DeepEqual(claude, wantClaude) {
				t.Errorf("resumable Claude chats =\n%v\nwant\n%v", claude, wantClaude)
			}
			if !reflect.DeepEqual(codex, wantCodex) {
				t.Errorf("resumable Codex chats =\n%v\nwant\n%v", codex, wantCodex)
			}
			if output.KilledCount != 2 || output.SuppressedCount != 5 {
				t.Errorf(
					"killed, suppressed = %d, %d, want 2, 5 (cc-34 _HIDE, cc-35 _kill; cc-00, cc-01, cx-00, cx-01 past the caps, oc-empty)",
					output.KilledCount,
					output.SuppressedCount,
				)
			}
			if claims := output.LiveClaims(); !reflect.DeepEqual(claims, wantClaims) {
				t.Errorf("live claims = %v, want %v", claims, wantClaims)
			}
		})
	}
	// The re-read around the claims stays inside the caps: the two oldest
	// chats of each engine are never read.
	data, err := LoadDefaultData(ctx, database)
	if err != nil {
		t.Fatalf("LoadDefaultData() = %v", err)
	}
	data, err = EnrichLive(ctx, database, data, live)
	if err != nil {
		t.Fatalf("EnrichLive() = %v", err)
	}
	data, err = refillDefault(ctx, database, data, wantClaims)
	if err != nil {
		t.Fatalf("refillDefault() = %v", err)
	}
	var read, wantRead []string
	for index := range data.Transcripts {
		read = append(read, data.Transcripts[index].UUID)
	}
	for index := range data.Rollouts {
		read = append(read, data.Rollouts[index].ID)
	}
	for index := 2; index <= 33; index++ {
		wantRead = append(wantRead, fmt.Sprintf("cc-%02d", index))
	}
	for index := 2; index <= 19; index++ {
		if index != 10 {
			wantRead = append(wantRead, fmt.Sprintf("cx-%02d", index))
		}
	}
	slices.Sort(read)
	if !slices.Equal(read, wantRead) {
		t.Errorf("refilled candidates =\n%v\nwant\n%v", read, wantRead)
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
