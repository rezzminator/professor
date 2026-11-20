package installping

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

func TestInstallPostsTheExactBody(t *testing.T) {
	c := newCounter(t, http.StatusNoContent, "")
	options := testOptions(t, c.server.URL+"/v1/ping", clock.NewFake(time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)))
	if err := SendInstall(context.Background(), options); err != nil {
		t.Fatalf("Install = %v, want nil", err)
	}
	want := postedBody(t, options, EventInstall)
	if len(c.bodies) != 1 || c.bodies[0] != want || c.types[0] != "POST /v1/ping application/json" {
		t.Fatalf("posted %q as %q; want one %s as POST /v1/ping application/json", c.bodies, c.types, want)
	}
	day, err := os.ReadFile(filepath.Join(options.StateDir, "install-ping-day-2026-11-17"))
	if string(day) != "2026-11-17\n" {
		t.Fatalf("day claim after Install = %q, %v; want 2026-11-17", day, err)
	}
	if err := SendDaily(context.Background(), options); err != nil || len(c.bodies) != 1 {
		t.Fatalf("Daily on the install's day = %v after %d posts; want nil and no second post", err, len(c.bodies))
	}
}
