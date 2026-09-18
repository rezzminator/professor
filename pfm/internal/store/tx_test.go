package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"hostops/pfm/internal/obs"
)

func TestBatchUsesFiveHundredRecordTransactions(t *testing.T) {
	setStoreTestJail(t)
	store := openTestStore(t)
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()

	var ranges [][2]int
	if err := store.Batch(ctx, 1001, func(tx *ImmediateTx, start, end int) error {
		ranges = append(ranges, [2]int{start, end})
		for index := start; index < end; index++ {
			if err := tx.SetMeta(ctx, fmt.Sprintf("batch-%04d", index), "seen"); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("Batch() error = %v", err)
	}
	wantRanges := [][2]int{{0, 500}, {500, 1000}, {1000, 1001}}
	if !reflect.DeepEqual(ranges, wantRanges) {
		t.Fatalf("Batch() ranges = %v, want %v", ranges, wantRanges)
	}

	var count int
	if err := store.db.QueryRow(
		`SELECT count(*) FROM meta WHERE key LIKE 'batch-%'`,
	).Scan(&count); err != nil {
		t.Fatalf("count batched rows: %v", err)
	}
	if count != 1001 {
		t.Fatalf("batched row count = %d, want 1001", count)
	}
}

func TestImmediateTransactionRollsBack(t *testing.T) {
	setStoreTestJail(t)
	store := openTestStore(t)
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	sentinel := errors.New("stop")

	err := store.WithImmediateTx(ctx, func(tx *ImmediateTx) error {
		if err := tx.SetMeta(ctx, "rolled_back", "yes"); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("WithImmediateTx() error = %v, want sentinel", err)
	}
	if _, found, err := store.Meta(ctx, "rolled_back"); err != nil || found {
		t.Fatalf("Meta() after rollback found = %v, error = %v; want false, nil", found, err)
	}
}

// TestImmediateTransactionRecordsRollbackFailure provokes a rollback that
// itself fails (the connection is closed from inside fn, so the deferred
// ROLLBACK cannot run) and asserts the db record obs.SQL writes for the
// BEGIN IMMEDIATE op — and the error WithImmediateTx returns — both name the
// rollback failure, not just fn's own error (F3).
func TestImmediateTransactionRecordsRollbackFailure(t *testing.T) {
	setStoreTestJail(t)
	store := openTestStore(t)
	t.Cleanup(func() { _ = store.Close() })
	ctx, recorder := obs.Test(t)
	sentinel := errors.New("stop")

	err := store.WithImmediateTx(ctx, func(tx *ImmediateTx) error {
		if closeErr := tx.conn.Close(); closeErr != nil {
			t.Fatalf("close connection early: %v", closeErr)
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("WithImmediateTx() error = %v, want to still wrap sentinel", err)
	}
	if !strings.Contains(err.Error(), "rollback immediate transaction") {
		t.Fatalf("WithImmediateTx() error = %v, want rollback failure named", err)
	}

	var beginRecord obs.Record
	found := false
	for _, record := range recorder.Records() {
		if record.Message != "db.statement" {
			continue
		}
		if op, ok := record.Field("op"); ok && op == "begin" {
			beginRecord = record
			found = true
		}
	}
	if !found {
		t.Fatalf("no db.statement record for the begin op found in %q", recorder.Raw())
	}
	errField, ok := beginRecord.Field("err")
	if !ok {
		t.Fatalf("begin record has no err field: %+v", beginRecord.Fields)
	}
	if !strings.Contains(fmt.Sprint(errField), "rollback immediate transaction") {
		t.Fatalf("begin record err = %v, want rollback failure named", errField)
	}
}
