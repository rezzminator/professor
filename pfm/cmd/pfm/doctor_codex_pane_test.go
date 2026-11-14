package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/doctor"
	"github.com/rezzminator/professor/pfm/internal/kill"
	"github.com/rezzminator/professor/pfm/internal/store"
)

// The binding table and the meta store are two different things, and an
// unreadable one must never read as an empty one.
func TestCodexPaneBindingsDistinguishAnEmptyTableFromAnUnreadableOne(t *testing.T) {
	jailTest(t)
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	manager, err := kill.New(database, kill.Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := manager.CodexPaneBindings(ctx)
	if err != nil || len(bindings) != 0 {
		t.Fatalf("empty table = (%v, %v), want (no bindings, no error)", bindings, err)
	}

	// Closing the store turns every read into a failure. The empty answer and
	// the failed answer must not be the same answer.
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CodexPaneBindings(ctx); err == nil {
		t.Fatal("a closed store returned no error — a failed read read as an empty table")
	}

	var stdout bytes.Buffer
	if warnings := doctor.PrintCodexPaneBinding(
		ctx, &stdout, database, commandRuntime{},
	); warnings == 0 {
		t.Fatalf("doctor called an unreadable binding table healthy: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "codex_pane_bindings=unreadable") {
		t.Fatalf("doctor did not name the read failure: %s", stdout.String())
	}
}
