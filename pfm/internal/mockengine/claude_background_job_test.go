package mockengine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/testwait"
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
		name      string
		hostLives bool
	}{
		{"stopped while its host lives", true},
		{"host gone", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "sessions", strconv.Itoa(os.Getpid())+".json")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			link, host, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = link.Close() }()
			defer func() { _ = host.Close() }()
			done := make(chan error, 1)
			go func() { done <- serveBackgroundJob(ctx, dir, session, link) }()
			testwait.Until(t, "the record "+path+" to hold the job", func() (bool, string) {
				var got map[string]any
				if content, err := os.ReadFile(path); err == nil {
					_ = json.Unmarshal(content, &got)
				}
				return reflect.DeepEqual(got, want), fmt.Sprintf("record %v, want %v", got, want)
			})
			if test.hostLives {
				cancel()
			} else {
				_ = host.Close() // the host's end of the link closes as its process exits
			}
			if err, _ := testwait.Receive(t, "serveBackgroundJob to return", done); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("record %s after the job ended: %v, want it removed", path, err)
			}
		})
	}
}
