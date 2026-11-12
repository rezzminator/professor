package statusline

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// Claude fires no hook on /rename; its statusline renders the new
// session_name, and the render carries it to the chat's native Herdr seat.
func TestRenderNamesTheHerdrSeatOfARenamedClaudeChat(t *testing.T) {
	const (
		fresh    = `{"session_id":"herdr-session","session_name":"Fresh name"}`
		other    = `{"session_id":"herdr-session","session_name":"Other name"}`
		subagent = `{"session_id":"herdr-session","session_name":"Fresh name","agent":{"name":"scout"}}`
		unnamed  = `{"session_id":"herdr-session"}`
	)
	herdrPane := map[string]string{"HERDR_SOCKET_PATH": "/run/herdr/fixture.sock", "HERDR_PANE_ID": "w6:p3"}
	for _, testCase := range []struct {
		name     string
		engine   pfmengine.ID
		env      map[string]string
		payloads []string
		refuse   bool
		want     []string // titles the seat is named after, in order
		warned   []string
	}{
		{
			name: "a renamed chat names its seat once", engine: pfmengine.Claude, env: herdrPane,
			payloads: []string{fresh, fresh}, want: []string{"Fresh name"},
		},
		{
			name: "a new name names it again", engine: pfmengine.Claude, env: herdrPane,
			payloads: []string{fresh, other}, want: []string{"Fresh name", "Other name"},
		},
		{
			name: "a refused rename retries on the next render", engine: pfmengine.Claude, env: herdrPane,
			payloads: []string{fresh, fresh}, refuse: true,
			want: []string{"Fresh name", "Fresh name"},
			warned: []string{
				`statusline: name the Herdr seat of herdr-session "Fresh name": seat refused`,
				`statusline: name the Herdr seat of herdr-session "Fresh name": seat refused`,
			},
		},
		{name: "a sub-agent's line", engine: pfmengine.Claude, env: herdrPane, payloads: []string{subagent}},
		{name: "an unnamed chat", engine: pfmengine.Claude, env: herdrPane, payloads: []string{unnamed}},
		{name: "a chat outside Herdr", engine: pfmengine.Claude, env: map[string]string{}, payloads: []string{fresh}},
		{name: "a Codex chat", engine: pfmengine.Codex, env: herdrPane, payloads: []string{fresh}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			seat := func(title string) string {
				return "herdr-session | " + title + " | " + root + "/config-state.db | " + root + "/proc"
			}
			var calls []string
			RenameHerdrSeat = func(_ context.Context, values paths.Values, sessionID, title string) error {
				calls = append(calls, strings.Join([]string{sessionID, title, values.StateDB, values.ProcRoot}, " | "))
				if testCase.refuse {
					return errors.New("seat refused")
				}
				return nil
			}
			t.Cleanup(func() { RenameHerdrSeat = nil })
			var warn bytes.Buffer
			runtime := Runtime{
				Now:          time.Now,
				Home:         root,
				ConfigDir:    filepath.Join(root, ".claude"),
				CacheDir:     filepath.Join(root, "cache"),
				RateLimitDir: filepath.Join(root, "rates"),
				SIDDir:       filepath.Join(root, "sid"),
				TmuxDir:      filepath.Join(root, "tmux"),
				ProcRoot:     filepath.Join(root, "proc"),
				StateDB:      filepath.Join(root, "config-state.db"),
				Engine:       testCase.engine,
				Env:          testCase.env,
				Spawn:        func(RefreshKind) error { return nil },
				Warn:         &warn,
			}
			for _, payload := range testCase.payloads {
				if _, err := Render(context.Background(), []byte(payload), runtime); err != nil {
					t.Fatalf("Render(%s): %v", payload, err)
				}
			}
			var warned []string
			for _, line := range strings.Split(warn.String(), "\n") {
				if strings.HasPrefix(line, "statusline: name the Herdr seat") {
					warned = append(warned, line)
				}
			}
			var want []string
			for _, title := range testCase.want {
				want = append(want, seat(title))
			}
			if !slices.Equal(calls, want) || !slices.Equal(warned, testCase.warned) {
				t.Fatalf("seat renames %q, warnings %q; want %q, %q", calls, warned, want, testCase.warned)
			}
		})
	}
}
