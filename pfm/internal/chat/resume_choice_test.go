package chat

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/launchnotice"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// A dead native chat opened from the picker after its resume walk launches
// under the walk's account and cache, the seat retired as the chat opens; an
// open with no walk keeps the account the chat itself resumes on.
func TestOpenNativeDeadRowResumesUnderTheWalksChoice(t *testing.T) {
	for _, walked := range []bool{true, false} {
		t.Run(fmt.Sprintf("walked=%t", walked), func(t *testing.T) {
			root := testjail.Fleet(t)
			socket := filepath.Join(testjail.ShortRoot(t), "gone", "herdr-api.sock")
			runtime, db, seat := nativeChatSeat(t, socket, "lost")
			loaded, err := config.LoadRuntime("")
			if err != nil {
				t.Fatal(err)
			}
			runtime.Config = loaded.Config
			birth, other := filepath.Join(root, "cc-1"), filepath.Join(root, "cc-2")
			for _, dir := range []string{birth, other} {
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			runtime.Config.Accounts = []config.Account{{ID: 1, ConfigDir: birth}, {ID: 2, ConfigDir: other}}
			if err := db.WriteSeatRun(seat.SeatID, fleetdb.SeatRun{
				Binary: "/fixture/claude", Env: []string{"CLAUDE_CONFIG_DIR=" + birth},
				Args: []string{"--resume", seat.SessionID}, CWD: runtime.Paths.Home,
			}); err != nil {
				t.Fatal(err)
			}
			seedClaudeChat(t, root, seat.SessionID)
			for _, name := range []string{"HERDR_ENV", "HERDR_SOCKET_PATH", "HERDR_PANE_ID", "TMUX"} {
				t.Setenv(name, "")
			}
			fake := &fakeOpenTmux{alive: map[string]bool{}}
			stubOpenExecutor(t, fake)
			t.Cleanup(launchnotice.Replace(strings.NewReader("\n"), false))
			ctx := context.Background()
			row := nativeRow(seat)
			row.Account = 2
			ctx = WithResumeChoice(ctx, walked)
			var stdout, stderr bytes.Buffer
			code := OpenRow(ctx, row, 2, true, "", &stdout, &stderr, &runtime)
			if code != 0 || len(fake.created) != 1 {
				t.Fatalf("code=%d created=%+v stderr=%q", code, fake.created, stderr.String())
			}
			run := fake.created[0].Run
			// Unwalked, the chat resumes on its recipe's account at its own cache.
			want := "CLAUDE_CONFIG_DIR='" + birth + "' CACHE_LIVE_CONTROL_MAIN_TTL='5m' "
			if walked {
				want = "CLAUDE_CONFIG_DIR='" + other + "' CACHE_LIVE_CONTROL_MAIN_TTL='1h' "
			}
			if !strings.Contains(run, want) {
				t.Fatalf("run %q, want it to launch with %q", run, want)
			}
			stored, found, err := db.Seat(ctx, seat.SeatID)
			if err != nil || found {
				t.Fatalf("replaced seat %+v found=%t err=%v, want it retired", stored, found, err)
			}
		})
	}
}
