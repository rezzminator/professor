package store

import (
	"context"
	"strings"
	"testing"

	"hostops/pfm/internal/obs"
)

// TestKilledWriteRecordsRetries: killedWrite is the package's only
// busy-retry loop — one comp=db record per Kill/Unkill call, its "retries"
// field set only when the first write raced a persistent SQLITE_BUSY. The
// real statement obs.SQL is given is the fixed shape "UPDATE hidden", never
// the shared store's real parameterized INSERT ... ON CONFLICT text or its
// bound arguments.
func TestKilledWriteRecordsRetries(t *testing.T) {
	setStoreTestJail(t)
	writer := openTestStore(t)
	t.Cleanup(func() { _ = writer.Close() })

	if err := writer.Kill(context.Background(), Killed{ID: "seed", KilledAt: 1}); err != nil {
		t.Fatalf("seed Kill() error = %v", err)
	}
	if err := writer.state.SetBusyTimeout(context.Background(), 1); err != nil {
		t.Fatalf("shorten shared busy timeout: %v", err)
	}
	release := holdSharedWriteLock(t, writer.SharedPath())

	ctx, recorder := obs.Test(t)
	if err := writer.Kill(ctx, Killed{ID: "busy-kill", KilledAt: 2}); err == nil {
		t.Fatal("Kill() under persistent SQLITE_BUSY reported success")
	}
	release()

	var found bool
	for _, record := range recorder.Records() {
		if record.Message != "db.statement" {
			continue
		}
		// killedWrite's own record is the sole `UPDATE hidden` under kind
		// store — the busy-warning path also emits an unrelated `insert meta`
		// through IncrementMeta, which this must not mistake for its own.
		if kind, _ := record.Field("kind"); kind != storeKind {
			continue
		}
		op, _ := record.Field("op")
		table, _ := record.Field("table")
		if op != "update" || table != "hidden" {
			continue
		}
		found = true
		if comp, _ := record.Field(obs.FieldComp); comp != "db" {
			t.Fatalf("comp = %v, want db", comp)
		}
		if retries, hasRetries := record.Field("retries"); !hasRetries || retries != float64(1) {
			t.Fatalf("retries = %v, found = %v, want 1", retries, hasRetries)
		}
	}
	if !found {
		t.Fatalf("no UPDATE hidden comp=db record for the killed write: %s", recorder.Raw())
	}
	// The shared store's real statement text — ON CONFLICT ... excluded — is
	// what obs.SQL must never see; killedWrite hands it the fixed shape
	// "UPDATE hidden" instead.
	if strings.Contains(recorder.Raw(), "ON CONFLICT") || strings.Contains(recorder.Raw(), "excluded") {
		t.Fatalf("the shared store's real SQL text reached the file: %s", recorder.Raw())
	}
}
