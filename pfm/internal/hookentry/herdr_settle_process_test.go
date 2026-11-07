package hookentry

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A settle process records or replays only what a hook could hand it: a
// recorded event needs the session it records into and an event a settle
// records, an unproved replay the ancestry its engine proof walks.
func TestHerdrSettleRefusesWhatNoHookHandsIt(t *testing.T) {
	const stored, payload = `{"state":"idle"}`, `{"session_id":"activity-session"}`
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "event without a session",
			args: []string{herdrSettleEvent, "7", stored, `{}`, "idle"},
			want: `pfm internal herdr-state settle event: session "", event "idle": ` +
				"want a session id and an event a settle records\n",
		},
		{
			name: "event a settle never records",
			args: []string{herdrSettleEvent, "7", stored, payload, "agent-start"},
			want: `pfm internal herdr-state settle event: session "activity-session", event "agent-start": ` +
				"want a session id and an event a settle records\n",
		},
		{
			name: "unproved without an ancestry",
			args: []string{
				herdrSettleUnproved, "7", stored, payload, `{"pane":"%1"}`, "--socket", "/fixture/tmux", "idle",
			},
			want: `pfm internal herdr-state settle: decode the engine proof "{\"pane\":\"%1\"}": ` +
				"<nil>, want an ancestry\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stderr bytes.Buffer
			rc := settleHerdrStateWithWait(test.args, &stderr, herdrDeps{}, func() {
				t.Fatal("a refused settle waited")
			})
			if rc != 1 || stderr.String() != test.want {
				t.Fatalf("settle %q: rc=%d stderr=%q, want rc=1 stderr=%q", test.args, rc, stderr.String(), test.want)
			}
		})
	}
}

// A Herdr that never takes the dial again ends the settle process at its
// bound, whichever of its attempts or its time comes first, and the session's
// log says it gave up.
func TestHerdrSettleGivesUpAtItsBound(t *testing.T) {
	for _, test := range []struct {
		name     string
		step     time.Duration
		attempts int
		spent    string
	}{
		{"attempts", 0, 120, "0s"},
		{"time", 10 * time.Minute, 6, "1h0m0s"},
	} {
		t.Run(test.name, func(t *testing.T) {
			rig := newNativeActivityRig(t)
			rig.quietly(t, "prompt")
			settle, _ := rig.dropUnder(t, "no dial", "idle")
			clock := time.Unix(5000, 0)
			rig.deps.now = func() time.Time { clock = clock.Add(time.Millisecond); return clock }
			waits := 0
			var stderr bytes.Buffer
			rc := settleHerdrStateWithWait(settle[3:], &stderr, rig.deps, func() {
				waits++
				if waits > 2*test.attempts {
					t.Fatalf("settle still retrying after %d waits: %s", waits, &stderr)
				}
				clock = clock.Add(test.step)
			})
			want := fmt.Sprintf("pfm internal herdr-state settle: gave up after %d attempts over %s: "+
				"the state of seq %s stays undelivered, and the pane keeps its last delivered state "+
				"until the session's next event", test.attempts, test.spent, settle[4])
			logPath := filepath.Join(rig.lockDir, "activity-session.log")
			data, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			last := lines[len(lines)-1]
			if _, message, _ := strings.Cut(last, "\t"); rc != 0 || waits != test.attempts || message != want {
				t.Fatalf("settle: rc=%d waits=%d, %s ends %q; want exit 0 after %d waits ending %q",
					rc, waits, logPath, last, test.attempts, want)
			}
		})
	}
}
