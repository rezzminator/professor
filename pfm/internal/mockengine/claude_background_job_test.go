package mockengine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/gather"
)

// TestServeBackgroundJobHoldsItsRecordWhileTheHostLives: the daemon's per-process record is the shape pfm's
// gather reads (its pid, the session, kind bg, its start as pfm reads it), held while the host lives and the
// daemon runs, removed when it is stopped or finds its host gone.
func TestServeBackgroundJobHoldsItsRecordWhileTheHostLives(t *testing.T) {
	const session = "d3333333-3333-4333-8333-333333333333"
	stat, err := gather.NewProcFS("/proc").Stat(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"pid": float64(os.Getpid()), "sessionId": session, "kind": "bg",
		"procStart": strconv.FormatUint(stat.StartTime, 10),
	}
	for _, test := range []struct {
		name string
		host int
	}{
		{"stopped while its host lives", os.Getpid()},
		{"host gone", 1<<22 + 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "sessions", strconv.Itoa(os.Getpid())+".json")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- serveBackgroundJob(ctx, dir, session, test.host) }()
			if test.host == os.Getpid() {
				var got map[string]any
				deadline := time.Now().Add(5 * time.Second)
				for time.Now().Before(deadline) && !reflect.DeepEqual(got, want) {
					got = nil
					if content, err := os.ReadFile(path); err == nil {
						_ = json.Unmarshal(content, &got)
					}
					time.Sleep(20 * time.Millisecond)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("record %s = %v, want %v", path, got, want)
				}
				cancel()
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("serveBackgroundJob did not return")
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("record %s after the job ended: %v, want it removed", path, err)
			}
		})
	}
}
