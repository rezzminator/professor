package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/store"
)

// A --new --hide reload hides the conversation it left behind by recording
// a PERMANENT kill for its id through the same manager `pfm chat kill <id>`
// uses — never a /clear-style prompt baseline, which the very next prompt in
// the reborn pane would undo. An id the index has not caught up with is still
// hidden (the pane's socket vouches for the engine, exactly as ⌃X on a fresh
// agent row does), and an empty id is an error, never a silent no-op: the
// worker guards it, and this helper must never let a missing identity read as
// "hidden".
func TestHideReloadedConversationRecordsAPermanentKillForTheConversationLeftBehind(t *testing.T) {
	root := jailTest(t)
	t.Setenv("PFM_FLEET_DB", filepath.Join(root, "shared.db"))
	ctx := context.Background()
	runtime := commandRuntime{Paths: paths.Values{Home: filepath.Join(root, "home")}}

	indexed := "22222222-2222-4222-8222-222222222222"
	transcriptPath := filepath.Join(root, "claude", "project", indexed+".jsonl")
	if err := os.MkdirAll(filepath.Dir(transcriptPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		transcriptPath,
		[]byte(`{"type":"user","cwd":"/work/example","message":{"content":"first"}}`+"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertTranscript(ctx, store.Transcript{
		UUID: indexed, Path: transcriptPath, CWD: "/work/example", Size: 1, PromptCount: 3,
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	got, err := hideReloadedConversation(ctx, runtime, pfmengine.Claude, indexed, transcriptPath, &stderr)
	if err != nil {
		t.Fatalf("hide indexed conversation: %v\nstderr=%s", err, stderr.String())
	}
	if got != indexed {
		t.Fatalf("hidden id = %q, want %q", got, indexed)
	}

	unindexed := "33333333-3333-4333-8333-333333333333"
	if got, err := hideReloadedConversation(
		ctx,
		runtime,
		pfmengine.Claude,
		unindexed,
		"",
		&stderr,
	); err != nil ||
		got != unindexed {
		t.Fatalf(
			"hide unindexed conversation: id=%q err=%v — the pane's engine vouches for an id the index has not seen\nstderr=%s",
			got,
			err,
			stderr.String(),
		)
	}

	if _, err := hideReloadedConversation(ctx, runtime, pfmengine.Claude, "", "", &stderr); err == nil {
		t.Fatal("empty id hid nothing and reported success — a missing identity must be an error, never a silent no-op")
	}

	database, err = store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}()
	for _, id := range []string{indexed, unindexed} {
		killed, found, err := database.Killed(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if !found {
			t.Fatalf("no kill recorded for %s — the conversation left behind would still be listed as resumable", id)
		}
		if killed.BaselinePrompts != nil {
			t.Fatalf(
				"kill for %s carries prompt baseline %d — a hide is permanent, not a /clear baseline the reborn pane's first prompt undoes",
				id,
				*killed.BaselinePrompts,
			)
		}
	}
}

// TestHideReloadedConversationNeverSpawnsTheExitFinisher pins the invariant
// this change must not disturb: a --new --hide reload tombstones the
// conversation it left behind through a kill.Request that carries no socket
// or pane (hideReloadedConversation calls manager.Kill with only ID/Engine/
// RolloutPath — never fleet.ResolveRow), so manager.Kill's own `live` gate
// (SocketPath != "" && PaneID != "") can never see one either. The reborn
// pane in the SAME socket/window this reload just repainted must keep
// running, never get closed out from under the conversation now living
// there.
//
// To prove the absence of a spawn without ever risking a REAL one, PATH is
// cleared first: deps.DetachLauncher cannot resolve setsid OR nohup with an
// empty PATH, so if hideReloadedConversation ever forwarded a live address,
// manager.Kill's spawn attempt would fail loudly right here with a "detach
// kill finisher" error — never silently, and never by actually launching a
// detached process. Today's fixed code never reaches that call at all, so
// this passes with no launcher on PATH and no error.
func TestHideReloadedConversationNeverSpawnsTheExitFinisher(t *testing.T) {
	root := jailTest(t)
	t.Setenv("PFM_FLEET_DB", filepath.Join(root, "shared.db"))
	t.Setenv("PATH", "")
	ctx := context.Background()
	runtime := commandRuntime{Paths: paths.Values{Home: filepath.Join(root, "home")}}

	const id = "66666666-6666-4666-8666-666666666666"
	var stderr bytes.Buffer
	if _, err := hideReloadedConversation(ctx, runtime, pfmengine.Claude, id, "", &stderr); err != nil {
		t.Fatalf(
			"hide with no live address must never need a detach launcher: %v\nstderr=%s",
			err, stderr.String(),
		)
	}
}
