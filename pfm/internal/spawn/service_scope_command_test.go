package spawn

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/servicemanager"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// A chat server spawned from inside a systemd user service escapes it through
// a transient scope and drops the service's invocation marker; under launchd
// it launches directly, and with no systemd-run to make the scope it refuses.
func TestChatSpawnInsideUserServiceUsesTransientScope(t *testing.T) {
	cases := []struct {
		name       string
		kind       servicemanager.Kind
		haveScope  bool
		wantScope  string
		wantMarker string
		wantErr    string
	}{
		{
			name: "systemd scopes the server", kind: servicemanager.KindSystemd, haveScope: true,
			wantScope: "--user --collect --scope -- {tmux} -S {socket} new-session -d", wantMarker: "",
		},
		{
			name: "launchd launches the server directly", kind: servicemanager.KindLaunchd,
			wantMarker: "service-invocation",
		},
		{
			name: "systemd without systemd-run refuses", kind: servicemanager.KindSystemd,
			wantErr: "create chat server: running inside a systemd user service requires systemd-run" +
				" to create a durable chat scope: no systemd-run here",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			bin := filepath.Join(root, "bin")
			if err := os.MkdirAll(bin, 0o700); err != nil {
				t.Fatal(err)
			}
			scopeLog := filepath.Join(root, "systemd-run.log")
			markerLog := filepath.Join(root, "marker.log")
			systemdRun := filepath.Join(bin, "systemd-run")
			writeExecutable(t, systemdRun, `#!/bin/sh
printf '%s\n' "$*" >> "$SYSTEMD_RUN_LOG"
[ "$1" = --user ] && [ "$2" = --collect ] && [ "$3" = --scope ] && [ "$4" = -- ] || exit 91
shift 4
exec "$@"
`)
			tmuxPath := filepath.Join(bin, "tmux")
			writeExecutable(t, tmuxPath, `#!/bin/sh
case "$*" in
  *" new-session "*) printf '%s' "$INVOCATION_ID" > "$MARKER_LOG" ;;
esac
exit 0
`)
			t.Setenv("PATH", bin)
			t.Setenv("INVOCATION_ID", "service-invocation")
			t.Setenv("SYSTEMD_RUN_LOG", scopeLog)
			t.Setenv("MARKER_LOG", markerLog)
			tmuxDir := filepath.Join(root, "tmux")
			tmux := TmuxSpawner{Binary: tmuxPath, TmuxDir: tmuxDir, serviceKind: tc.kind}
			tmux.lookPath = func(name string) (string, error) {
				if name != servicemanager.SystemdRunBinary || !tc.haveScope {
					return "", errors.New("no systemd-run here")
				}
				return systemdRun, nil
			}
			err := tmux.NewSession(context.Background(), SessionSpec{
				Socket: "cx-scope", Session: "cx-scope", Window: "Codex", CWD: root,
				Width: 180, Height: 45, Run: "sleep 120",
			})
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("spawn error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("spawn under service: %v", err)
			}
			logged, readErr := os.ReadFile(scopeLog)
			if tc.wantScope == "" {
				if !errors.Is(readErr, os.ErrNotExist) {
					t.Fatalf("systemd-run ran (%q, %v), want a direct launch; log %s", logged, readErr, scopeLog)
				}
			} else {
				want := strings.NewReplacer("{tmux}", tmuxPath, "{socket}", filepath.Join(tmuxDir, "cx-scope")).
					Replace(tc.wantScope)
				if readErr != nil || !strings.HasPrefix(string(logged), want) {
					t.Fatalf("systemd-run argv = %q (%v), want prefix %q; log %s", logged, readErr, want, scopeLog)
				}
			}
			marker, err := os.ReadFile(markerLog)
			if err != nil || string(marker) != tc.wantMarker {
				t.Fatalf("tmux saw INVOCATION_ID = %q (%v), want %q; log %s", marker, err, tc.wantMarker, markerLog)
			}
		})
	}
}

func writeExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := testjail.WriteExecutable(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
}
