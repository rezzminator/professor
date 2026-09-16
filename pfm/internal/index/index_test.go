package index

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/naming"
	"hostops/pfm/internal/paths"
	"hostops/pfm/internal/store"
)

func TestNewWithPathsUsesInjectedRoots(t *testing.T) {
	fixture := setupIndexFixture(t)
	wrongRoot := t.TempDir()
	wrongCodexRoot := t.TempDir()
	t.Setenv(paths.EnvClaudeRoots, wrongRoot)
	t.Setenv(paths.EnvCodexRoot, wrongCodexRoot)

	database := openIndexStore(t)
	t.Cleanup(func() { _ = database.Close() })
	indexer, err := NewWithPaths(database, paths.Values{
		Roots: map[pfmengine.ID][]string{
			pfmengine.Claude: {fixture.claudeRoot},
			pfmengine.Codex:  {fixture.codexRoot},
		},
	})
	if err != nil {
		t.Fatalf("NewWithPaths() error = %v", err)
	}
	counters, err := indexer.Run(context.Background(), Options{})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if counters.FilesSeen != 10 || counters.FullParsed != 10 {
		t.Fatalf("injected-root counters = %+v, want 10 files and 10 full parses", counters)
	}
}

func TestIndexerIteratesEveryConfiguredCodexRoot(t *testing.T) {
	root := t.TempDir()
	codexRoots := []string{filepath.Join(root, "codex-1"), filepath.Join(root, "codex-2")}
	for index, codexRoot := range codexRoots {
		id := fmt.Sprintf("codex-account-%d", index+1)
		path := filepath.Join(codexRoot, "sessions", "2026", "08", fmt.Sprintf("rollout-%s.jsonl", id))
		rewriteJSONLines(t, path, []any{map[string]any{
			"type": "response_item",
			"payload": map[string]any{
				"type": "message", "role": "user",
				"content": []map[string]any{{"type": "input_text", "text": id}},
			},
		}})
	}
	database := openIndexStore(t)
	t.Cleanup(func() { _ = database.Close() })
	indexer, err := NewWithRoots(database, paths.Values{}, map[pfmengine.ID][]string{pfmengine.Codex: codexRoots})
	if err != nil {
		t.Fatal(err)
	}
	counters, err := indexer.Run(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if counters.FilesSeen != 2 || counters.FullParsed != 2 {
		t.Fatalf("multi-root counters=%+v, want both Codex rollouts parsed", counters)
	}
	for index := range codexRoots {
		id := fmt.Sprintf("codex-account-%d", index+1)
		rollout, found, err := database.Rollout(context.Background(), id)
		if err != nil || !found || rollout.FirstPrompt != id {
			t.Fatalf("Rollout(%q)=%#v found=%t err=%v", id, rollout, found, err)
		}
	}
	warm, err := indexer.Run(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if warm.FilesSeen != 2 || warm.FilesSkipped != 2 || warm.FullParsed != 0 || warm.RowsTouched != 0 ||
		warm.BytesRead != 0 ||
		warm.CxNamesReloaded {
		t.Fatalf("warm multi-root counters=%+v, want two skips and no work", warm)
	}
}

func TestIndexGoldenAndIncrementalTransitions(t *testing.T) {
	fixture := setupIndexFixture(t)
	database := openIndexStore(t)
	t.Cleanup(func() { _ = database.Close() })
	indexer, err := New(database)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := context.Background()

	initial, err := indexer.Run(ctx, Options{})
	if err != nil {
		t.Fatalf("initial Run() error = %v", err)
	}
	if initial.FilesSeen != 10 ||
		initial.FullParsed != 10 ||
		initial.FilesSkipped != 0 ||
		initial.DeltaParsed != 0 ||
		initial.Deleted != 0 ||
		!initial.CxNamesReloaded {
		t.Fatalf("initial counters = %+v, want 10 full files and cx_names reload", initial)
	}
	if initial.BytesRead <= 70<<10 {
		t.Fatalf("initial BytesRead = %d, want >70 KiB fixture coverage", initial.BytesRead)
	}

	gotGolden := dumpIndex(t, database, fixture.claudeRoot, fixture.codexRoot)
	wantGolden, err := os.ReadFile(testdataPath(t, "golden", "index.tsv"))
	if err != nil {
		t.Fatalf("read index golden: %v", err)
	}
	if gotGolden != string(wantGolden) {
		t.Fatalf("index.tsv mismatch\n--- got ---\n%s--- want ---\n%s", gotGolden, wantGolden)
	}

	large, found, err := database.Transcript(ctx, "large-line")
	if err != nil || !found {
		t.Fatalf("large-line Transcript() found = %v, error = %v", found, err)
	}
	if len(large.FirstPrompt) != 75<<10 || large.PromptCount != 1 {
		t.Fatalf(
			"large-line prompt length/count = %d/%d, want %d/1",
			len(large.FirstPrompt),
			large.PromptCount,
			75<<10,
		)
	}
	if _, found, err := database.Transcript(ctx, "ignored-memory"); err != nil || found {
		t.Fatalf("memory symlink transcript found = %v, error = %v; want false, nil", found, err)
	}
	if _, found, err := database.Transcript(ctx, "ignored-deep"); err != nil || found {
		t.Fatalf("maxdepth transcript found = %v, error = %v; want false, nil", found, err)
	}

	unchanged, err := indexer.Run(ctx, Options{})
	if err != nil {
		t.Fatalf("unchanged Run() error = %v", err)
	}
	assertNoChangeCounters(t, unchanged, 10)

	alphaPath := filepath.Join(fixture.claudeRoot, "project-alpha", "alpha.jsonl")
	alphaAppend := appendJSONLine(t, alphaPath, map[string]any{
		"type": "user",
		"cwd":  "/work/alpha",
		"message": map[string]any{
			"content": "Alpha appended",
		},
	})
	alphaCustom := appendJSONLine(t, alphaPath, map[string]any{
		"type":        "custom-title",
		"customTitle": "Manual alpha delta",
	})
	alphaAI := appendJSONLine(t, alphaPath, map[string]any{
		"type":    "ai-title",
		"aiTitle": "AI delta after manual",
	})
	alphaDelta, err := indexer.Run(ctx, Options{})
	if err != nil {
		t.Fatalf("Claude delta Run() error = %v", err)
	}
	assertSingleDelta(
		t,
		alphaDelta,
		10,
		int64(len(alphaAppend)+len(alphaCustom)+len(alphaAI)),
	)
	alpha, found, err := database.Transcript(ctx, "alpha")
	if err != nil || !found {
		t.Fatalf("alpha Transcript() found = %v, error = %v", found, err)
	}
	if alpha.PromptCount != 3 ||
		alpha.LastPrompt != "Alpha appended" ||
		naming.DisplayName(
			alpha.CustomTitle,
			alpha.AITitle,
			alpha.FirstPrompt,
		) != "Manual alpha delta" ||
		alpha.AITitle != "AI delta after manual" {
		t.Fatalf("alpha after delta = %#v", alpha)
	}

	partialBefore, found, err := database.Transcript(ctx, "partial")
	if err != nil || !found {
		t.Fatalf("partial Transcript() found = %v, error = %v", found, err)
	}
	if partialBefore.ParsedOffset >= partialBefore.Size || partialBefore.PromptCount != 1 {
		t.Fatalf("partial initial row = %#v, want unconsumed tail and one prompt", partialBefore)
	}
	partialSuffix := []byte(" completed\"}}\n")
	appendBytes(t, fixture.partialPath, partialSuffix)
	partialDelta, err := indexer.Run(ctx, Options{})
	if err != nil {
		t.Fatalf("partial completion Run() error = %v", err)
	}
	wantPartialRead := partialBefore.Size - partialBefore.ParsedOffset + int64(len(partialSuffix))
	assertSingleDelta(t, partialDelta, 10, wantPartialRead)
	partialAfter, found, err := database.Transcript(ctx, "partial")
	if err != nil || !found {
		t.Fatalf("partial Transcript() after completion found = %v, error = %v", found, err)
	}
	if partialAfter.PromptCount != 2 || partialAfter.LastPrompt != "Partial completed" {
		t.Fatalf("partial after completion = %#v", partialAfter)
	}

	codexAppend := appendJSONLine(t, fixture.userRolloutPath, map[string]any{
		"type": "response_item",
		"payload": map[string]any{
			"type": "message",
			"role": "user",
			"content": []map[string]any{
				{"type": "input_text", "text": "Codex appended"},
			},
		},
	})
	codexDelta, err := indexer.Run(ctx, Options{})
	if err != nil {
		t.Fatalf("Codex delta Run() error = %v", err)
	}
	assertSingleDelta(t, codexDelta, 10, int64(len(codexAppend)))
	rollout, found, err := database.Rollout(ctx, "cx-user")
	if err != nil || !found {
		t.Fatalf("cx-user Rollout() found = %v, error = %v", found, err)
	}
	if rollout.PromptCount != 3 || rollout.FirstPrompt != "Codex first" {
		t.Fatalf("cx-user after delta = %#v", rollout)
	}

	shrunkPath := filepath.Join(fixture.claudeRoot, "project-beta", "shrunk.jsonl")
	rewriteJSONLines(t, shrunkPath, []any{
		map[string]any{
			"type": "user",
			"cwd":  "/work/rewritten",
			"message": map[string]any{
				"content": "New prompt",
			},
		},
	})
	if err := os.Chtimes(shrunkPath, time.Unix(1, 0), time.Unix(1, 0)); err != nil {
		t.Fatalf("move shrunk fixture mtime backwards: %v", err)
	}
	shrunkCounters, err := indexer.Run(ctx, Options{})
	if err != nil {
		t.Fatalf("shrunk Run() error = %v", err)
	}
	if shrunkCounters.FullParsed != 1 ||
		shrunkCounters.DeltaParsed != 0 ||
		shrunkCounters.FilesSkipped != 9 {
		t.Fatalf("shrunk counters = %+v, want one full reparse", shrunkCounters)
	}
	shrunk, found, err := database.Transcript(ctx, "shrunk")
	if err != nil || !found {
		t.Fatalf("shrunk Transcript() found = %v, error = %v", found, err)
	}
	if shrunk.PromptCount != 1 ||
		shrunk.FirstPrompt != "New prompt" ||
		shrunk.CustomTitle != "" ||
		shrunk.AITitle != "" {
		t.Fatalf("shrunk rewritten row = %#v", shrunk)
	}

	junkPath := filepath.Join(fixture.claudeRoot, "project-beta", "junk.jsonl")
	if err := os.Remove(junkPath); err != nil {
		t.Fatalf("remove junk fixture: %v", err)
	}
	deleted, err := indexer.Run(ctx, Options{})
	if err != nil {
		t.Fatalf("deleted Run() error = %v", err)
	}
	if deleted.FilesSeen != 9 ||
		deleted.FilesSkipped != 9 ||
		deleted.Deleted != 1 ||
		deleted.RowsTouched != 1 ||
		deleted.BytesRead != 0 {
		t.Fatalf("deleted counters = %+v", deleted)
	}
	if _, found, err := database.Transcript(ctx, "junk"); err != nil || found {
		t.Fatalf("deleted Transcript() found = %v, error = %v; want false, nil", found, err)
	}

	appendJSONLine(t, filepath.Join(fixture.codexRoot, "session_index.jsonl"), map[string]any{
		"id":          "cx-user",
		"thread_name": "Own newest name",
	})
	namesReload, err := indexer.Run(ctx, Options{})
	if err != nil {
		t.Fatalf("cx_names reload Run() error = %v", err)
	}
	if namesReload.FilesSkipped != 9 ||
		!namesReload.CxNamesReloaded ||
		namesReload.FullParsed != 0 ||
		namesReload.DeltaParsed != 0 ||
		namesReload.BytesRead <= 0 {
		t.Fatalf("cx_names counters = %+v", namesReload)
	}
	names, err := database.CxNames(ctx)
	if err != nil {
		t.Fatalf("CxNames() error = %v", err)
	}
	if names["cx-user"] != "Own newest name" {
		t.Fatalf("cx-user name = %q, want newest", names["cx-user"])
	}

	finalUnchanged, err := indexer.Run(ctx, Options{})
	if err != nil {
		t.Fatalf("final unchanged Run() error = %v", err)
	}
	assertNoChangeCounters(t, finalUnchanged, 9)
}

func TestParseCodexSubagentMessagesNeverPromoteToUserThread(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(
		root,
		"rollout-2026-01-01T00-00-00-subagent-id.jsonl",
	)
	content := strings.Join([]string{
		`{"type":"session_meta","thread_source":"subagent","payload":{"id":"subagent-id","cwd":"/work/sub"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"delegated task"}]}}`,
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	file := diskFile{
		ID:      "subagent-id",
		Path:    path,
		Size:    info.Size(),
		MTimeNS: info.ModTime().UnixNano(),
	}
	rollout, offset, err := parseCodex(file, 0, store.Rollout{})
	if err != nil {
		t.Fatal(err)
	}
	if rollout.UserThread || rollout.PromptCount != 1 {
		t.Fatalf("full subagent parse = %#v", rollout)
	}

	appended := `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"follow-up"}]}}` + "\n"
	appendBytes(t, path, []byte(appended))
	info, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	file.Size = info.Size()
	file.MTimeNS = info.ModTime().UnixNano()
	rollout, _, err = parseCodex(file, offset, rollout)
	if err != nil {
		t.Fatal(err)
	}
	if rollout.UserThread || rollout.PromptCount != 2 {
		t.Fatalf("delta subagent parse = %#v", rollout)
	}
}

func TestClaudeMetadataAppendDoesNotRefreshPromptActivity(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "semantic-activity.jsonl")
	const promptStamp = "2026-08-16T17:10:00.123Z"
	content := `{"type":"user","timestamp":"` + promptStamp + `","cwd":"/work/marketing","message":{"content":"Ship the campaign"}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	physical := time.Date(2026, 8, 23, 21, 8, 27, 0, time.UTC)
	if err := os.Chtimes(path, physical, physical); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	file := diskFile{ID: "semantic-activity", Path: path, Size: info.Size(), MTimeNS: info.ModTime().UnixNano()}
	transcript, offset, err := parseClaude(file, 0, store.Transcript{})
	if err != nil {
		t.Fatal(err)
	}
	want, _ := time.Parse(time.RFC3339Nano, promptStamp)
	if transcript.ActivityNS != want.UnixNano() {
		t.Fatalf("full activity=%s, want last meaningful prompt %s (physical mtime %s)",
			time.Unix(0, transcript.ActivityNS).UTC(), want, physical)
	}

	appendBytes(t, path, []byte(`{"type":"bridge-session","version":"1"}`+"\n"))
	later := physical.Add(time.Hour)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	info, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	file.Size = info.Size()
	file.MTimeNS = info.ModTime().UnixNano()
	transcript, _, err = parseClaude(file, offset, transcript)
	if err != nil {
		t.Fatal(err)
	}
	if transcript.ActivityNS != want.UnixNano() {
		t.Fatalf("metadata-only delta changed prompt activity to %s, want %s",
			time.Unix(0, transcript.ActivityNS).UTC(), want)
	}
}

func TestCodexFilenameIdentityPreventsForkCollisionAndWarmReparse(t *testing.T) {
	root := t.TempDir()
	codexRoot := filepath.Join(root, "codex")
	sessionDir := filepath.Join(codexRoot, "sessions", "2026", "01", "01")
	for _, directory := range []string{
		sessionDir,
		filepath.Join(root, "claude"),
		filepath.Join(root, "sid"),
		filepath.Join(root, "tmux"),
		filepath.Join(root, "home"),
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for id, lines := range map[string][]string{
		"parent-id": {
			`{"type":"session_meta","payload":{"id":"parent-id","thread_source":"user","cwd":"/work/parent"}}`,
			`{"type":"event_msg","id":"event-parent","payload":{"type":"user_message","message":"parent prompt"}}`,
		},
		"fork-id": {
			`{"type":"session_meta","payload":{"id":"fork-id","session_id":"parent-id","parent_thread_id":"parent-id","source":{"subagent":{"thread_spawn":{"parent_thread_id":"parent-id"}}},"cwd":"/work/fork"}}`,
			`{"type":"event_msg","id":"parent-id","payload":{"type":"user_message","message":"delegated prompt"}}`,
		},
	} {
		path := filepath.Join(
			sessionDir,
			"rollout-2026-01-01T00-00-00-"+id+".jsonl",
		)
		if err := os.WriteFile(
			path,
			[]byte(strings.Join(lines, "\n")+"\n"),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("TMUX_TMPDIR", filepath.Join(root, "t"))
	t.Setenv(paths.EnvDB, filepath.Join(root, "fleet.db"))
	t.Setenv(paths.EnvSIDDir, filepath.Join(root, "sid"))
	t.Setenv(paths.EnvClaudeRoots, filepath.Join(root, "claude"))
	t.Setenv(paths.EnvCodexRoot, codexRoot)
	t.Setenv(paths.EnvTmuxDir, filepath.Join(root, "tmux"))
	t.Setenv(paths.EnvHome, filepath.Join(root, "home"))

	database := openIndexStore(t)
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}()
	indexer, err := New(database)
	if err != nil {
		t.Fatal(err)
	}
	first, err := indexer.Run(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if first.FullParsed != 2 ||
		first.RowsTouched != 4 ||
		!first.CxNamesReloaded {
		t.Fatalf("first collision pass = %+v", first)
	}
	rollouts, err := database.Rollouts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rollouts) != 2 ||
		rollouts[0].ID != "fork-id" ||
		rollouts[0].UserThread ||
		rollouts[1].ID != "parent-id" ||
		!rollouts[1].UserThread {
		t.Fatalf("rollouts after collision pass = %#v", rollouts)
	}

	second, err := indexer.Run(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if second.FilesSeen != 2 ||
		second.FilesSkipped != 2 ||
		second.FullParsed != 0 ||
		second.DeltaParsed != 0 ||
		second.RowsTouched != 0 ||
		second.BytesRead != 0 {
		t.Fatalf("warm collision pass = %+v", second)
	}
}

func TestSDKSpawnedSessionsIndexAsBackgroundAndReparseOnVersionBump(t *testing.T) {
	root := t.TempDir()
	claudeRoot := filepath.Join(root, "claude")
	project := filepath.Join(claudeRoot, "project-spawned")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile(testdataPath(t, "sdk-spawned-session.jsonl"))
	if err != nil {
		t.Fatalf("read SDK-spawned fixture: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(project, "sdk-spawned.jsonl"),
		fixture,
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	rewriteJSONLines(t, filepath.Join(project, "interactive.jsonl"), []any{
		map[string]any{
			"type":         "user",
			"cwd":          "/work/spawned",
			"promptSource": "typed",
			"entrypoint":   "cli",
			"origin":       map[string]any{"kind": "human"},
			"message":      map[string]any{"role": "user", "content": "Typed by a human"},
		},
	})

	t.Setenv("TMUX_TMPDIR", filepath.Join(root, "t"))
	t.Setenv(paths.EnvDB, filepath.Join(root, "state", "fleet.db"))
	t.Setenv(paths.EnvSIDDir, filepath.Join(root, "sid"))
	t.Setenv(paths.EnvClaudeRoots, claudeRoot)
	t.Setenv(paths.EnvCodexRoot, filepath.Join(root, "codex"))
	t.Setenv(paths.EnvTmuxDir, filepath.Join(root, "tmux"))
	t.Setenv(paths.EnvHome, filepath.Join(root, "home"))

	database := openIndexStore(t)
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}()
	indexer, err := New(database)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := indexer.Run(ctx, Options{}); err != nil {
		t.Fatalf("initial Run() error = %v", err)
	}
	spawned, found, err := database.Transcript(ctx, "sdk-spawned")
	if err != nil || !found || !spawned.IsBG || spawned.PromptCount != 1 {
		t.Fatalf("SDK-spawned row = %#v found=%t err=%v, want one background prompt",
			spawned, found, err)
	}
	interactive, found, err := database.Transcript(ctx, "interactive")
	if err != nil || !found || interactive.IsBG || interactive.PromptCount != 1 {
		t.Fatalf("interactive row = %#v found=%t err=%v, want one foreground prompt",
			interactive, found, err)
	}

	// A row indexed before SDK detection existed carries is_bg=0 while its file
	// on disk is byte-identical, so only a parser version bump reparses it.
	stale := spawned
	stale.IsBG = false
	if err := database.Batch(ctx, 1, func(tx *store.ImmediateTx, _, _ int) error {
		return tx.UpsertTranscript(ctx, stale)
	}); err != nil {
		t.Fatal(err)
	}
	warm, err := indexer.Run(ctx, Options{})
	if err != nil {
		t.Fatalf("warm Run() error = %v", err)
	}
	if warm.FilesSkipped != 2 || warm.FullParsed != 0 {
		t.Fatalf("warm counters = %+v, want both files skipped", warm)
	}
	if unchanged, _, _ := database.Transcript(ctx, "sdk-spawned"); unchanged.IsBG {
		t.Fatal("stale row was repaired without a parser version bump")
	}

	if err := database.SetMeta(ctx, claudeParserVersionKey, "0"); err != nil {
		t.Fatal(err)
	}
	bumped, err := indexer.Run(ctx, Options{})
	if err != nil {
		t.Fatalf("version bump Run() error = %v", err)
	}
	if bumped.FullParsed != 2 || bumped.FilesSkipped != 0 {
		t.Fatalf("version bump counters = %+v, want a full reparse", bumped)
	}
	repaired, found, err := database.Transcript(ctx, "sdk-spawned")
	if err != nil || !found || !repaired.IsBG {
		t.Fatalf("repaired row = %#v found=%t err=%v, want is_bg set", repaired, found, err)
	}
	version, found, err := database.Meta(ctx, claudeParserVersionKey)
	if err != nil || !found || version != claudeParserVersion {
		t.Fatalf("stored parser version = %q found=%t err=%v", version, found, err)
	}
}

func TestAttachmentMarkerAloneDoesNotFlagBackground(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "attached-interactive.jsonl")
	content := `{"type":"user","cwd":"/work/attached","sessionKind":"bg","entrypoint":"cli","promptSource":"typed","message":{"content":"Typed while detached"}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	file := diskFile{ID: "attached-interactive", Path: path, Size: info.Size(), MTimeNS: info.ModTime().UnixNano()}
	transcript, _, err := parseClaude(file, 0, store.Transcript{})
	if err != nil {
		t.Fatal(err)
	}
	if transcript.IsBG || transcript.PromptCount != 1 {
		t.Fatalf(
			"attached interactive transcript = %#v, want is_bg=false (sessionKind:\"bg\" measures pane attachment, not who spawned the session)",
			transcript,
		)
	}
}

func TestBackgroundMarkerOnLaterRecordDoesNotRetroactivelyFlagInteractiveSession(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sticky-interactive.jsonl")
	content := strings.Join([]string{
		`{"type":"user","cwd":"/work/sticky","entrypoint":"cli","promptSource":"typed","message":{"content":"First, attached"}}`,
		`{"type":"user","cwd":"/work/sticky","sessionKind":"bg","entrypoint":"cli","promptSource":"typed","message":{"content":"Second, detached mid-session"}}`,
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	file := diskFile{ID: "sticky-interactive", Path: path, Size: info.Size(), MTimeNS: info.ModTime().UnixNano()}
	transcript, _, err := parseClaude(file, 0, store.Transcript{})
	if err != nil {
		t.Fatal(err)
	}
	if transcript.IsBG || transcript.PromptCount != 2 {
		t.Fatalf(
			"sticky-interactive transcript = %#v, want is_bg=false across the whole session even after a later detached-pane record",
			transcript,
		)
	}
}

func TestPriorityProjectPassUpdatesOnlyLaunchCWDThenFullPass(t *testing.T) {
	fixture := setupIndexFixture(t)
	database := openIndexStore(t)
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}()
	indexer, err := New(database)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := indexer.Run(ctx, Options{}); err != nil {
		t.Fatal(err)
	}
	alphaPath := filepath.Join(fixture.claudeRoot, "project-alpha", "alpha.jsonl")
	betaPath := filepath.Join(fixture.claudeRoot, "project-beta", "shrunk.jsonl")
	appendJSONLine(t, alphaPath, map[string]any{
		"type": "user", "cwd": "/work/alpha",
		"message": map[string]any{"content": "priority alpha"},
	})
	appendJSONLine(t, betaPath, map[string]any{
		"type": "user", "cwd": "/work/beta",
		"message": map[string]any{"content": "deferred beta"},
	})

	priority, err := indexer.Run(ctx, Options{
		PriorityCWD:  "/work/alpha",
		PriorityOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if priority.DeltaParsed != 1 || priority.RowsTouched != 1 ||
		priority.Deleted != 0 || priority.CxNamesReloaded {
		t.Fatalf("priority counters = %+v", priority)
	}
	alpha, found, err := database.Transcript(ctx, "alpha")
	if err != nil || !found || alpha.LastPrompt != "priority alpha" {
		t.Fatalf("priority alpha = %#v found=%t err=%v", alpha, found, err)
	}
	beta, found, err := database.Transcript(ctx, "shrunk")
	if err != nil || !found || beta.LastPrompt == "deferred beta" {
		t.Fatalf("beta was updated during priority pass: %#v found=%t err=%v", beta, found, err)
	}

	full, err := indexer.Run(ctx, Options{PriorityCWD: "/work/alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if full.DeltaParsed != 1 {
		t.Fatalf("full-after-priority counters = %+v", full)
	}
	beta, found, err = database.Transcript(ctx, "shrunk")
	if err != nil || !found || beta.LastPrompt != "deferred beta" {
		t.Fatalf("full beta = %#v found=%t err=%v", beta, found, err)
	}
}

type indexFixture struct {
	claudeRoot      string
	codexRoot       string
	partialPath     string
	userRolloutPath string
}

func setupIndexFixture(t *testing.T) indexFixture {
	t.Helper()

	root := t.TempDir()
	claudeRoot := filepath.Join(root, "claude-physical")
	codexRoot := filepath.Join(root, "codex")
	copyTree(t, testdataPath(t, "claude-store"), claudeRoot)
	copyTree(t, testdataPath(t, "codex-store"), codexRoot)

	largeLinePath := filepath.Join(claudeRoot, "project-alpha", "large-line.jsonl")
	rewriteJSONLines(t, largeLinePath, []any{
		map[string]any{
			"type": "user",
			"cwd":  "/work/large",
			"message": map[string]any{
				"content": strings.Repeat("L", 75<<10),
			},
		},
	})

	partialPath := filepath.Join(claudeRoot, "project-beta", "partial.jsonl")
	complete := []byte(`{"type":"user","cwd":"/work/partial","message":{"content":"Complete before partial"}}` + "\n")
	partial := []byte(`{"type":"user","cwd":"/work/partial","message":{"content":"Partial`)
	if err := os.WriteFile(partialPath, append(complete, partial...), 0o600); err != nil {
		t.Fatalf("write partial fixture: %v", err)
	}

	memoryTarget := filepath.Join(root, "memory-target")
	if err := os.MkdirAll(memoryTarget, 0o700); err != nil {
		t.Fatalf("create memory target: %v", err)
	}
	rewriteJSONLines(t, filepath.Join(memoryTarget, "ignored-memory.jsonl"), []any{
		map[string]any{"type": "user", "message": map[string]any{"content": "ignored"}},
	})
	if err := os.Symlink(
		memoryTarget,
		filepath.Join(claudeRoot, "project-alpha", "memory"),
	); err != nil {
		t.Fatalf("create memory symlink: %v", err)
	}
	rewriteJSONLines(
		t,
		filepath.Join(claudeRoot, "project-alpha", "deep", "ignored-deep.jsonl"),
		[]any{map[string]any{"type": "user", "message": map[string]any{"content": "ignored"}}},
	)

	linkOne := filepath.Join(root, "claude-link-1")
	linkTwo := filepath.Join(root, "claude-link-2")
	if err := os.Symlink(claudeRoot, linkOne); err != nil {
		t.Fatalf("create first Claude root symlink: %v", err)
	}
	if err := os.Symlink(claudeRoot, linkTwo); err != nil {
		t.Fatalf("create second Claude root symlink: %v", err)
	}

	t.Setenv("TMUX_TMPDIR", filepath.Join(root, "t"))
	t.Setenv(paths.EnvDB, filepath.Join(root, "state", "fleet.db"))
	t.Setenv(paths.EnvSIDDir, filepath.Join(root, "sid"))
	t.Setenv(paths.EnvClaudeRoots, strings.Join([]string{linkOne, linkTwo, claudeRoot}, string(os.PathListSeparator)))
	t.Setenv(paths.EnvCodexRoot, codexRoot)
	t.Setenv(paths.EnvTmuxDir, filepath.Join(root, "tmux"))
	t.Setenv(paths.EnvHome, filepath.Join(root, "home"))

	return indexFixture{
		claudeRoot:  claudeRoot,
		codexRoot:   codexRoot,
		partialPath: partialPath,
		userRolloutPath: filepath.Join(
			codexRoot,
			"sessions",
			"2026",
			"01",
			"01",
			"rollout-2026-01-01T00-00-01-cx-user.jsonl",
		),
	}
}

func openIndexStore(t *testing.T) *store.Store {
	t.Helper()

	database, err := store.Open()
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	return database
}

func assertNoChangeCounters(t *testing.T, counters Counters, files int) {
	t.Helper()
	if counters.FilesSeen != files ||
		counters.FilesSkipped != files ||
		counters.FullParsed != 0 ||
		counters.DeltaParsed != 0 ||
		counters.Deleted != 0 ||
		counters.RowsTouched != 0 ||
		counters.BytesRead != 0 ||
		counters.CxNamesReloaded {
		t.Fatalf("no-change counters = %+v, want %d skipped and zero touches", counters, files)
	}
}

func assertSingleDelta(t *testing.T, counters Counters, files int, bytesRead int64) {
	t.Helper()
	if counters.FilesSeen != files ||
		counters.FilesSkipped != files-1 ||
		counters.DeltaParsed != 1 ||
		counters.FullParsed != 0 ||
		counters.Deleted != 0 ||
		counters.RowsTouched != 1 ||
		counters.BytesRead != bytesRead ||
		counters.CxNamesReloaded {
		t.Fatalf(
			"single-delta counters = %+v, want %d skipped and %d bytes",
			counters,
			files-1,
			bytesRead,
		)
	}
}

func dumpIndex(t *testing.T, database *store.Store, claudeRoot, codexRoot string) string {
	t.Helper()

	ctx := context.Background()
	transcripts, err := database.Transcripts(ctx)
	if err != nil {
		t.Fatalf("Transcripts() error = %v", err)
	}
	rollouts, err := database.Rollouts(ctx)
	if err != nil {
		t.Fatalf("Rollouts() error = %v", err)
	}
	names, err := database.CxNames(ctx)
	if err != nil {
		t.Fatalf("CxNames() error = %v", err)
	}

	var output strings.Builder
	for i := range transcripts {
		transcript := &transcripts[i]
		fmt.Fprintf(
			&output,
			"T\t%s\t%s\t%d\t%d\t%s\t%s\t%s\t%s\t%s\t%d\t%s\n",
			transcript.UUID,
			fixtureRelativePath(transcript.Path, claudeRoot, "claude"),
			transcript.Size,
			transcript.ParsedOffset,
			transcript.CWD,
			transcript.CustomTitle,
			transcript.AITitle,
			goldenText(transcript.FirstPrompt),
			goldenText(transcript.LastPrompt),
			transcript.PromptCount,
			strconv.FormatBool(transcript.IsBG),
		)
	}
	for i := range rollouts {
		rollout := &rollouts[i]
		fmt.Fprintf(
			&output,
			"R\t%s\t%s\t%d\t%d\t%s\t%s\t%s\t%s\t%s\t%d\t%s\n",
			rollout.ID,
			fixtureRelativePath(rollout.Path, codexRoot, "codex"),
			rollout.Size,
			rollout.ParsedOffset,
			rollout.CWD,
			strconv.FormatBool(rollout.UserThread),
			rollout.SessionID,
			rollout.ParentThread,
			goldenText(rollout.FirstPrompt),
			rollout.PromptCount,
			strconv.FormatBool(rollout.IsBG),
		)
	}
	nameIDs := make([]string, 0, len(names))
	for id := range names {
		nameIDs = append(nameIDs, id)
	}
	sort.Strings(nameIDs)
	for _, id := range nameIDs {
		fmt.Fprintf(&output, "N\t%s\t%s\n", id, names[id])
	}
	return output.String()
}

func fixtureRelativePath(path, root, prefix string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(filepath.Join(prefix, relative))
}

func goldenText(value string) string {
	if len(value) <= 120 {
		return value
	}
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("sha256:%s:%d", hex.EncodeToString(sum[:]), len(value))
}

func testdataPath(t *testing.T, elements ...string) string {
	t.Helper()
	parts := append([]string{"..", "..", "testdata"}, elements...)
	return filepath.Join(parts...)
}

func copyTree(t *testing.T, source, destination string) {
	t.Helper()

	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, content, 0o600)
	})
	if err != nil {
		t.Fatalf("copy fixture tree %q: %v", source, err)
	}
}

func rewriteJSONLines(t *testing.T, path string, records []any) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create fixture directory: %v", err)
	}
	var content []byte
	for _, record := range records {
		line, err := json.Marshal(record)
		if err != nil {
			t.Fatalf("marshal fixture record: %v", err)
		}
		content = append(content, line...)
		content = append(content, '\n')
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write JSONL fixture %q: %v", path, err)
	}
}

func appendJSONLine(t *testing.T, path string, record any) []byte {
	t.Helper()

	line, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal appended fixture record: %v", err)
	}
	line = append(line, '\n')
	appendBytes(t, path, line)
	return line
}

func appendBytes(t *testing.T, path string, content []byte) {
	t.Helper()

	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open fixture for append %q: %v", path, err)
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		t.Fatalf("append fixture %q: %v", path, err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close appended fixture %q: %v", path, err)
	}
}

func TestRolloutIDFromFilename(t *testing.T) {
	tests := map[string]string{
		"rollout-2026-01-02T03-04-05-id.jsonl": "id",
		"rollout-unusual.jsonl":                "unusual",
	}
	for name, want := range tests {
		if got := rolloutIDFromFilename(name); got != want {
			t.Fatalf("rolloutIDFromFilename(%q) = %q, want %q", name, got, want)
		}
	}
}
