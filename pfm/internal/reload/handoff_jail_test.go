package reload

import (
	"context"
	"os"
	"strings"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func TestRunIgnoresUnusableHandoffAndReloads(t *testing.T) {
	for _, tc := range []struct{ content, why string }{
		{"{", "unexpected end of JSON input"},
		{"null", "no written_at"},
		{"{}", "no written_at"},
		{`{"engine":"cc","account":2}`, "no written_at"},
		{
			`{"engine":"zz","written_at":"1970-01-01T00:00:42Z"}`,
			`engine: unknown engine "zz" (want cc/claude, cx/codex, ox/opencode)`,
		},
	} {
		t.Run(tc.content, func(t *testing.T) {
			dir := t.TempDir()
			lockPath := LockPath(dir, "probe-1", "%7")
			if err := os.WriteFile(lockPath, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			tmux := &fakeReloadTmux{}
			var stderr strings.Builder
			result, err := Run(context.Background(), Request{
				Engine: pfmengine.Claude, SocketPath: "/tmp/probe-1", Pane: "%7",
				SessionID: "11111111-1111-4111-8111-111111111111", Account: 1, AccountIDs: []int{1},
				CWD: "/jail/project", Machine: reloadTestMachine("", dir),
			}, Options{SIDDir: dir, Delay: -1, Poll: -1, ExitTries: 2}, tmux, nil, &stderr)
			want := "pfm chat reload: ignoring reload handoff " + lockPath + ": reload handoff record unusable: " +
				tc.why + "\n"
			if err != nil || tmux.respawn == "" ||
				parsedReloadShell(t, tmux.respawn).Resume != "11111111-1111-4111-8111-111111111111" ||
				!strings.HasPrefix(stderr.String(), want) {
				t.Fatalf("err=%v respawned=%t result=%+v\nstderr=%q\nwant prefix %q",
					err, tmux.respawn != "", result, stderr.String(), want)
			}
		})
	}
}
