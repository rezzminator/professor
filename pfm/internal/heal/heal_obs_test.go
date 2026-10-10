package heal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// TestHealRecordsEveryForeignStatement: Sweep's reads against the state and
// history stores, and Delete's three-table removal, each write one comp=db
// record under kind=codex — verb and table only, never the rollout path or
// the thread id bound into the statement.
func TestHealRecordsEveryForeignStatement(t *testing.T) {
	jail := newCodexJail(t)
	const wedged = "33333333-3333-4333-8333-333333333333"
	offsets := jail.addThread(t, wedged, 3)
	jail.setCursor(t, wedged, offsets[2], 1)

	ctx, recorder := obs.Test(t)
	if _, err := Sweep(ctx, jail.stores, ""); err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}
	if err := Delete(ctx, jail.stores, wedged); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	counts := map[string]int{}
	for _, record := range recorder.Records() {
		if record.Message != "db.statement" {
			continue
		}
		if kind, _ := record.Field("kind"); kind != "codex" {
			t.Fatalf("kind = %v, want codex", kind)
		}
		if comp, _ := record.Field(obs.FieldComp); comp != "db" {
			t.Fatalf("comp = %v, want db", comp)
		}
		op, _ := record.Field("op")
		table, _ := record.Field("table")
		counts[op.(string)+" "+table.(string)]++
		if _, found := record.Field(obs.FieldDur); !found {
			t.Fatalf("no dur_ms: %v", record.Fields)
		}
	}
	for _, want := range []string{
		"select thread_history_projection_state",
		"select threads",
		"delete thread_history_projection_state",
		"delete thread_items",
		"delete thread_turns",
	} {
		if counts[want] == 0 {
			t.Fatalf("no record for %q; saw %v in %s", want, counts, recorder.Raw())
		}
	}
	// db.open already records its own store path by design (obs.SQLOpen);
	// what must never appear is the bound thread id DELETE binds by argument.
	if strings.Contains(recorder.Raw(), wedged) {
		t.Fatalf("a bound thread id reached the file: %s", recorder.Raw())
	}
}

// TestRunRecordsTheHealsStateTransitions: the heal coordinator walks
// requested → scanned → planned → repaired → done through the state door,
// and Delete records its own present → deleted transition for the thread it
// heals.
func TestRunRecordsTheHealsStateTransitions(t *testing.T) {
	jail := newCodexJail(t)
	const (
		healthy = "22222222-2222-4222-8222-222222222222"
		wedged  = "33333333-3333-4333-8333-333333333333"
	)
	healthyOffsets := jail.addThread(t, healthy, 3)
	wedgedOffsets := jail.addThread(t, wedged, 3)
	jail.setCursor(t, healthy, healthyOffsets[1], 1)
	jail.setCursor(t, wedged, wedgedOffsets[2], 1)

	runner, err := New(jail.root, func() time.Time { return time.Unix(1800000000, 0) })
	if err != nil {
		t.Fatal(err)
	}

	ctx, recorder := obs.Test(t)
	if _, err := runner.Run(ctx, Options{Apply: true}); err != nil {
		t.Fatalf("Run(--apply) error = %v", err)
	}

	var path []string
	deleted := false
	for _, record := range recorder.Records() {
		if record.Message != "state.transition" {
			continue
		}
		kind, _ := record.Field("kind")
		if kind != "heal" {
			continue
		}
		if comp, _ := record.Field(obs.FieldComp); comp != "state" {
			t.Fatalf("comp = %v, want state", comp)
		}
		prior, _ := record.Field("prior")
		next, _ := record.Field("next")
		if prior == "present" && next == "deleted" {
			deleted = true
			continue
		}
		path = append(path, next.(string))
	}
	if got := strings.Join(path, ","); got != "scanned,planned,repaired,done" {
		t.Fatalf("state path = %s: %s", got, recorder.Raw())
	}
	if !deleted {
		t.Fatalf("no present->deleted record for the healed thread: %s", recorder.Raw())
	}
}

func TestHealLeavesUnknownWriterLocksUntouched(t *testing.T) {
	for _, failure := range []string{"lock directory is a file", "lock is a symlink loop"} {
		t.Run(failure, func(t *testing.T) {
			jail := newCodexJail(t)
			const id = "33333333-3333-4333-8333-333333333333"
			offsets := jail.addThread(t, id, 3)
			jail.setCursor(t, id, offsets[2], 1)
			locks := filepath.Join(jail.root, "thread-writer-locks")
			if failure == "lock directory is a file" {
				if err := os.WriteFile(locks, []byte("not a directory"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.MkdirAll(locks, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(id+".lock", filepath.Join(locks, id+".lock")); err != nil {
					t.Fatal(err)
				}
			}
			runner, err := New(jail.root, nil)
			if err != nil {
				t.Fatal(err)
			}
			_, err = runner.Run(context.Background(), Options{Apply: true})
			if err == nil || !strings.Contains(err.Error(), "lock") {
				t.Errorf("unknown lock looked safe: Run error=%v", err)
			}
			if rows := jail.projectionRows(t, id); rows != 3 {
				t.Errorf("unknown writer lock allowed deletion: rows=%d", rows)
			}
		})
	}
}

func TestHealHoldsWriterLockThroughBackupAndDelete(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(strconv.FormatBool(existing), func(t *testing.T) {
			jail := newCodexJail(t)
			const id = "33333333-3333-4333-8333-333333333333"
			offsets := jail.addThread(t, id, 3)
			jail.setCursor(t, id, offsets[2], 1)
			locks := filepath.Join(jail.root, "thread-writer-locks")
			if err := os.MkdirAll(locks, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(locks, id+".lock")
			if existing {
				if err := os.WriteFile(path, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			probed := false
			runner, err := New(jail.root, func() time.Time {
				probed = true
				coordination, err := os.OpenFile(
					filepath.Join(locks, ".coordination.lock"),
					os.O_CREATE|os.O_RDWR,
					0o600,
				)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := coordination.Close(); err != nil {
						t.Errorf("close coordination: %v", err)
					}
				}()
				if err := unix.Flock(
					int(coordination.Fd()),
					unix.LOCK_EX|unix.LOCK_NB,
				); !errors.Is(
					err,
					unix.EWOULDBLOCK,
				) {
					t.Errorf("provider could recreate the writer inode during heal: coordination lock = %v", err)
				}
				writer, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := writer.Close(); err != nil {
						t.Errorf("close writer: %v", err)
					}
				}()
				err = unix.Flock(int(writer.Fd()), unix.LOCK_EX|unix.LOCK_NB)
				if !errors.Is(err, unix.EWOULDBLOCK) {
					t.Errorf("seat acquired writer lock between validation and deletion: %v", err)
				}
				return time.Unix(1800000000, 0)
			})
			if err != nil {
				t.Fatal(err)
			}
			report, err := runner.Run(context.Background(), Options{Apply: true})
			if err != nil {
				t.Fatal(err)
			}
			if !probed || len(report.Healed) != 1 || jail.projectionRows(t, id) != 0 {
				t.Fatalf("repair did not commit: %+v", report)
			}
			writer, err := os.OpenFile(path, os.O_RDWR, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := writer.Close(); err != nil {
					t.Errorf("close writer: %v", err)
				}
			}()
			if err := unix.Flock(int(writer.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
				t.Fatalf("heal leaked writer lock after commit: %v", err)
			}
		})
	}
}

func TestThreadWarnsOnRepairFailureWithoutBlockingResume(t *testing.T) {
	for _, failure := range []string{"missing home", "find stores", "read projection"} {
		t.Run(failure, func(t *testing.T) {
			jail := newCodexJail(t)
			const id = "33333333-3333-4333-8333-333333333333"
			home := jail.root
			switch failure {
			case "missing home":
				home = filepath.Join(jail.root, "nowhere")
			case "find stores":
				home = filepath.Join(jail.root, "invalid-home")
				if err := os.WriteFile(home, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			default:
				if err := os.WriteFile(jail.stores.State, []byte("not sqlite"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			message := Thread(context.Background(), home, id)
			if !strings.Contains(message, id) || !strings.Contains(message, "warning") ||
				!strings.Contains(message, "resume") {
				t.Fatalf("repair failure rendered as nothing to heal: %q", message)
			}
		})
	}
}

func TestHealReportsTheProtectedRevalidation(t *testing.T) {
	jail := newCodexJail(t)
	const id = "33333333-3333-4333-8333-333333333333"
	offsets := jail.addThread(t, id, 3)
	jail.setCursor(t, id, offsets[2], 1)
	path := filepath.Join(jail.root, "sessions", "rollout-"+id+".jsonl")
	runner, err := New(jail.root, func() time.Time {
		// Clock capture at the protected backup boundary plays a producer change.
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		content = []byte(strings.Replace(string(content), `"ordinal":2`, `"ordinal":4`, 1))
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		return time.Unix(1800000000, 0)
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := runner.Run(context.Background(), Options{Thread: id})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Threads) != 1 || report.Threads[0].Verdict != VerdictNoncanonical ||
		report.Totals[VerdictNoncanonical] != 1 || len(report.LeftAlone) != 1 ||
		len(report.Healed) != 0 || jail.projectionRows(t, id) != 3 {
		t.Fatalf("protected revalidation lost its refusal/report: %+v", report)
	}
}
