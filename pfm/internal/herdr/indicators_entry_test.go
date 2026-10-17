package herdr

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// TestIndicatorsEntryHandsThePainterTheHooksSocketAndTheHerdrStateDir pins the
// `pfm internal herdr-indicators` entry a Herdr plugin hook execs: the socket
// comes from the hook's HERDR_SOCKET_PATH, the lock directory is pfm's Herdr
// state directory, a painter error is exit 1, and a home that will not resolve
// never reaches the painter.
func TestIndicatorsEntryHandsThePainterTheHooksSocketAndTheHerdrStateDir(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	cases := []struct {
		name       string
		env        *paths.MapEnv
		paintErr   error
		wantCode   int
		wantCalled bool
		wantSocket string
		wantStderr string
	}{
		{
			name:       "painted",
			env:        &paths.MapEnv{HomeDir: home, Values: map[string]string{EnvSocket: "/run/herdr.sock"}},
			wantCode:   0,
			wantCalled: true,
			wantSocket: "/run/herdr.sock",
		},
		{
			name:       "painter error is exit 1",
			env:        &paths.MapEnv{HomeDir: home, Values: map[string]string{EnvSocket: "/run/herdr.sock"}},
			paintErr:   errors.New("herdr socket /run/herdr.sock: dial: refused"),
			wantCode:   1,
			wantCalled: true,
			wantSocket: "/run/herdr.sock",
		},
		{
			name:       "unset socket still reaches the painter, which names it",
			env:        &paths.MapEnv{HomeDir: home},
			paintErr:   errors.New("herdr indicators: HERDR_SOCKET_PATH is not set"),
			wantCode:   1,
			wantCalled: true,
			wantSocket: "",
		},
		{
			name:       "home error",
			env:        &paths.MapEnv{HomeErr: errors.New("no home")},
			wantCode:   1,
			wantStderr: "pfm internal herdr-indicators: resolve home directory: no home",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var called bool
			var gotSocket, gotLockDir string
			paint := func(_ context.Context, socket, lockDir string, _ io.Writer) error {
				called, gotSocket, gotLockDir = true, socket, lockDir
				return tc.paintErr
			}
			var stderr bytes.Buffer
			code := IndicatorsEntry(&stderr, tc.env, paint)
			if code != tc.wantCode || called != tc.wantCalled {
				t.Fatalf("exit %d, painter called %v; want exit %d, called %v (stderr %q)",
					code, called, tc.wantCode, tc.wantCalled, stderr.String())
			}
			if !tc.wantCalled {
				if !strings.Contains(stderr.String(), tc.wantStderr) {
					t.Fatalf("stderr %q lacks %q", stderr.String(), tc.wantStderr)
				}
				return
			}
			if gotSocket != tc.wantSocket || gotLockDir != paths.HerdrLockDir(home) {
				t.Fatalf("painter got socket %q lockDir %q; want %q %q",
					gotSocket, gotLockDir, tc.wantSocket, paths.HerdrLockDir(home))
			}
		})
	}
}
