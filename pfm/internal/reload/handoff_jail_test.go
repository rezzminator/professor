//go:build jail

package reload

import (
	"context"
	"os"
	"strings"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func TestRunIgnoresUnusableHandoffAndReloads(t *testing.T) {
	for _, tc := range []struct{ content, want string }{
		{"{", "pfm chat reload: ignoring reload handoff {lock}: reload handoff record unusable: unexpected end of JSON input\n"},
		{"null", "pfm chat reload: ignoring reload handoff {lock}: reload handoff record unusable: no written_at\n"},
		{"{}", "pfm chat reload: ignoring reload handoff {lock}: reload handoff record unusable: no written_at\n"},
		{`{"engine":"cc","account":2}`, "pfm chat reload: ignoring reload handoff {lock}: reload handoff record unusable: no written_at\n"},
		{`{"engine":"zz","written_at":"1970-01-01T00:00:42Z"}`, "pfm chat reload: ignoring reload handoff {lock}: reload handoff record unusable: engine: unknown engine \"zz\" (want cc/claude, cx/codex, ox/opencode)\n"},
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
			if err != nil || tmux.respawn == "" ||
				parsedReloadShell(t, tmux.respawn).Resume != "11111111-1111-4111-8111-111111111111" ||
				strings.ReplaceAll(stderr.String(), lockPath, "{lock}") != tc.want {
				t.Fatalf(
					"stderr = %q, want %q (err %v respawned %t result %+v)",
					strings.ReplaceAll(stderr.String(), lockPath, "{lock}"),
					tc.want,
					err,
					tmux.respawn != "",
					result,
				)
			}
		})
	}
}
