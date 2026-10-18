package fleet

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestNativeSeatsAcrossFleetViews(t *testing.T) {
	for _, cached := range []bool{false, true} {
		for _, tc := range []struct {
			name string
			view compose.View
			want []string
		}{
			{"default", compose.DefaultView, []string{"live", "lost", "failed"}},
			{"killed", compose.KilledView, []string{"hidden"}},
			{"all", compose.AllView, []string{"live", "hidden", "lost", "failed"}},
		} {
			t.Run(fmt.Sprintf("cached=%t/%s", cached, tc.name), func(t *testing.T) {
				testjail.Fleet(t)
				values, err := config.ResolvePaths()
				if err != nil {
					t.Fatal(err)
				}
				ctx := context.Background()
				state := fleetdb.OpenSharedState(ctx, values)
				t.Cleanup(func() {
					if err := state.Close(); err != nil {
						t.Error(err)
					}
				})
				for _, item := range []struct{ id, name, state string }{
					{"live", "REVIEW", "live"},
					{"hidden", "_KILL_hidden", "live"},
					{"lost", "LOST", "lost"},
					{"failed", "FAILED", "failed"},
				} {
					err := state.PutSeat(
						ctx,
						fleetdb.Seat{
							SeatID:      item.id,
							SessionID:   item.id,
							Engine:      "cc",
							Backend:     "herdr",
							State:       item.state,
							Name:        item.name,
							HerdrSocket: filepath.Join(values.Home, "dead.sock"),
							HerdrPane:   "w6:" + item.id,
							LostNote:    "engine exited",
						},
					)
					if err != nil {
						t.Fatal(err)
					}
				}
				db, err := store.Open()
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := db.Close(); err != nil {
						t.Error(err)
					}
				})
				request := Request{View: tc.view, ReadOnly: true}
				var result Result
				if cached {
					result, err = ScanCached(ctx, db, request)
				} else {
					result, err = Scan(ctx, db, request, &bytes.Buffer{})
				}
				if err != nil {
					t.Fatal(err)
				}
				got := map[string]compose.Row{}
				for _, row := range result.Output.Rows {
					if row.Backend == "herdr" {
						got[row.SeatID] = row
					}
				}
				if len(got) != len(tc.want) {
					t.Fatalf("native rows=%+v; want seats=%v", got, tc.want)
				}
				for _, id := range tc.want {
					row, ok := got[id]
					if !ok {
						t.Fatalf("native seat %s missing from %+v", id, got)
					}
					if row.BackendError == "" {
						t.Errorf("native error lost: %+v", row)
					}
					if row.Killed != (id == "hidden") {
						t.Errorf("canonical hidden classification=%+v", row)
					}
					if (id == "lost" || id == "failed") && row.Kind != compose.ResumeClaude {
						t.Errorf("non-live seat is addressable: %+v", row)
					}
				}
				if len(result.Errors) != 2 || !strings.Contains(strings.Join(result.Errors, " "), "HERDR UNREACHABLE") {
					t.Errorf("errors=%v, want only the two live probe failures", result.Errors)
				}
			})
		}
	}
}

func TestNativeDurableDiagnosticsDoNotFailListing(t *testing.T) {
	for _, cached := range []bool{false, true} {
		t.Run(fmt.Sprintf("cached=%t", cached), func(t *testing.T) {
			testjail.Fleet(t)
			values, err := config.ResolvePaths()
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			state := fleetdb.OpenSharedState(ctx, values)
			t.Cleanup(func() {
				if err := state.Close(); err != nil {
					t.Error(err)
				}
			})
			for _, status := range []string{"failed", "lost"} {
				if err := state.PutSeat(
					ctx,
					fleetdb.Seat{
						SeatID:    status,
						SessionID: status,
						Engine:    "cc",
						Backend:   "herdr",
						State:     status,
						Name:      status,
						LostNote:  "engine exited",
					},
				); err != nil {
					t.Fatal(err)
				}
			}
			db, err := store.Open()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			})
			request := Request{View: compose.DefaultView, ReadOnly: true}
			var result Result
			if cached {
				result, err = ScanCached(ctx, db, request)
			} else {
				result, err = Scan(ctx, db, request, &bytes.Buffer{})
			}
			if err != nil {
				t.Fatal(err)
			}
			diagnostics := 0
			for _, row := range result.Output.Rows {
				if row.Backend == "herdr" {
					diagnostics++
					if !strings.Contains(row.BackendError, "engine exited") || row.Kind != compose.ResumeClaude {
						t.Errorf("durable diagnostic lost: %+v", row)
					}
				}
			}
			if diagnostics != 2 || len(result.Errors) != 0 {
				t.Fatalf(
					"durable diagnostics=%d scan errors=%v; want two visible rows and successful listing",
					diagnostics,
					result.Errors,
				)
			}
		})
	}
}

func TestNativeReadFailurePreservesFleetRows(t *testing.T) {
	for _, cached := range []bool{false, true} {
		t.Run(fmt.Sprintf("cached=%t", cached), func(t *testing.T) {
			testjail.Fleet(t)
			ctx := context.Background()
			values, err := config.ResolvePaths()
			if err != nil {
				t.Fatal(err)
			}
			db, err := store.Open()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			})
			transcript := filepath.Join(values.Roots["cc"][0], "healthy.jsonl")
			body := "{\"type\":\"custom-title\",\"customTitle\":\"HEALTHY\"}\n" +
				"{\"type\":\"user\",\"message\":{\"content\":\"keep this healthy chat\"}}\n"
			if err := os.WriteFile(transcript, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := db.UpsertTranscript(
				ctx,
				store.Transcript{
					UUID:        "healthy",
					Path:        transcript,
					Size:        int64(len(body)),
					PromptCount: 1,
					CustomTitle: "HEALTHY",
				},
			); err != nil {
				t.Fatal(err)
			}
			runtime := config.Runtime{Config: config.Defaults(values.Home, values.Roots["cc"]), Paths: values}
			scan := func() (Result, error) {
				request := Request{View: compose.DefaultView, ReadOnly: true, Runtime: &runtime}
				if cached {
					return ScanCached(ctx, db, request)
				}
				return Scan(ctx, db, request, &bytes.Buffer{})
			}
			before, err := scan()
			if err != nil {
				t.Fatal(err)
			}
			find := func(result Result) compose.Row {
				for _, row := range result.Output.Rows {
					if row.ID == "healthy" {
						return row
					}
				}
				return compose.Row{}
			}
			if find(before).Name != "HEALTHY" {
				t.Fatalf("missing healthy baseline: %+v", before.Output.Rows)
			}
			blocker := filepath.Join(values.Home, "blocker")
			if err := os.WriteFile(blocker, []byte("file"), 0o600); err != nil {
				t.Fatal(err)
			}
			runtime.Paths.StateDB = filepath.Join(blocker, "state.db")
			after, err := scan()
			if err != nil {
				t.Fatalf("native DB failure discarded healthy fleet: %v", err)
			}
			if find(after).Name != "HEALTHY" || find(after).Path != transcript {
				t.Fatalf("native DB failure omitted healthy row: %+v", after.Output.Rows)
			}
			if len(after.Errors) != 1 || !strings.Contains(after.Errors[0], "durable seats") {
				t.Fatalf("native DB failure not explicit: %v", after.Errors)
			}
		})
	}
}

// The production scan tail accepts an already gathered snapshot. This is a unit
// fixture, not a real tmux server or an injected Gather implementation.
func TestNativeReadFailurePreservesLiveTmuxSnapshot(t *testing.T) {
	home := t.TempDir()
	blocker := filepath.Join(home, "blocker")
	if err := os.WriteFile(blocker, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	environment := Env{
		Paths:  paths.Values{Home: home, StateDB: filepath.Join(blocker, "state.db")},
		Config: config.Defaults(home, nil),
	}
	transcript := filepath.Join(home, "healthy.jsonl")
	data := Data{
		Transcripts: []store.Transcript{
			{UUID: "healthy", Path: transcript, PromptCount: 3, Size: 42, CustomTitle: "HEALTHY"},
		},
	}
	live := gather.Snapshot{
		Panes: []gather.ProbePane{{Socket: "cc-1-2-3", PaneID: "%0"}},
		Crumbs: []gather.Crumb{
			{Filename: "cc-1-2-3.%0", TranscriptPath: transcript, Socket: "cc-1-2-3", PaneID: "%0"},
		},
		ClaudeProcesses: []gather.ClaudeProcess{{Socket: "cc-1-2-3"}},
	}
	output, failures, err := composeNativeFleet(
		context.Background(),
		nil,
		environment,
		compose.DefaultView,
		data,
		live,
		true,
	)
	if err != nil {
		t.Fatalf("native DB failure discarded healthy live snapshot: %v", err)
	}
	if len(failures) != 1 || !strings.Contains(failures[0], "durable seats") {
		t.Fatalf("native DB failure not explicit beside live snapshot: %v", failures)
	}
	for _, row := range output.Rows {
		if row.ID == "healthy" {
			if row.Kind != compose.LiveClaude || row.Socket != "cc-1-2-3" || row.PaneID != "%0" ||
				row.Name != "HEALTHY" {
				t.Fatalf("healthy live tmux address changed: %+v", row)
			}
			return
		}
	}
	t.Fatalf("native DB error omitted healthy live tmux row: %+v", output.Rows)
}

func TestReadOnlyNativeScanDoesNotCreateState(t *testing.T) {
	for _, cached := range []bool{false, true} {
		t.Run(fmt.Sprint(cached), func(t *testing.T) {
			testjail.Fleet(t)
			values, err := config.ResolvePaths()
			if err != nil {
				t.Fatal(err)
			}
			db, err := store.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			}()
			runtime := config.Runtime{Config: config.Defaults(values.Home, values.Roots["cc"]), Paths: values}
			runtime.Paths.StateDB = filepath.Join(t.TempDir(), "absent", "state.db")
			request := Request{View: compose.DefaultView, ReadOnly: true, Runtime: &runtime}
			if cached {
				_, err = ScanCached(context.Background(), db, request)
			} else {
				_, err = Scan(context.Background(), db, request, &bytes.Buffer{})
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Dir(runtime.Paths.StateDB)); !os.IsNotExist(err) {
				t.Fatalf("read-only native scan created state directory: %v", err)
			}
		})
	}
}
