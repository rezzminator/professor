package hookentry

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/gather"
)

func TestRefreshSeatHostImage(t *testing.T) {
	dir := t.TempDir()
	installed, replaced := filepath.Join(dir, "pfm"), filepath.Join(dir, "pfm-replaced")
	for _, file := range []string{installed, replaced} {
		if err := os.WriteFile(file, []byte(file), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, image, binary string
		locateErr, execErr  error
		wantExec            bool
		wantStderr          string
	}{
		{name: "replaced binary execs the installed one", image: replaced, binary: installed, wantExec: true},
		{name: "current binary keeps running", image: installed, binary: installed},
		{
			name: "failed exec keeps running", image: replaced, binary: installed, wantExec: true,
			execErr:    errors.New("exec refused"),
			wantStderr: "pfm internal seat-host: switch to the installed pfm " + installed + ", keeping the running one: exec refused\n",
		},
		{
			name: "unlocatable binary keeps running", image: replaced, locateErr: errors.New("no executable"),
			wantStderr: "pfm internal seat-host: locate the installed pfm, keeping the running one: no executable\n",
		},
		{
			name: "unreadable image keeps running", binary: installed,
			wantStderr: "pfm internal seat-host: identify the running pfm, keeping it: stat " +
				filepath.Join(dir, "proc", strconv.Itoa(os.Getpid()), "exe") + ": no such file or directory\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := filepath.Join(dir, "proc")
			if err := os.RemoveAll(root); err != nil {
				t.Fatal(err)
			}
			self := filepath.Join(root, strconv.Itoa(os.Getpid()))
			if err := os.MkdirAll(self, 0o700); err != nil {
				t.Fatal(err)
			}
			if tc.image != "" {
				if err := os.Symlink(tc.image, filepath.Join(self, "exe")); err != nil {
					t.Fatal(err)
				}
			}
			var execed []string
			var stderr bytes.Buffer
			refreshSeatHostImage(
				gather.NewProcFS(root),
				func() (string, error) { return tc.binary, tc.locateErr },
				func(binary string, argv, _ []string) error {
					execed = append([]string{binary}, argv...)
					return tc.execErr
				},
				&stderr,
			)
			var want []string
			if tc.wantExec {
				want = append([]string{installed}, os.Args...)
			}
			if !slices.Equal(execed, want) || stderr.String() != tc.wantStderr {
				t.Fatalf(
					"exec = %q, stderr = %q; want exec %q, stderr %q",
					execed,
					stderr.String(),
					want,
					tc.wantStderr,
				)
			}
		})
	}
}
