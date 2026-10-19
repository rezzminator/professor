package installer

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

func TestLookupLoginShellLinux(t *testing.T) {
	for _, tc := range []struct {
		name, output, want, wantError string
		exitCode                      int
		err                           error
	}{
		{name: "login shell", output: "fixture:x:123:20:Fixture:/fixture:/bin/zsh\n"},
		{name: "lookup failed", err: errors.New("database unavailable"), wantError: "database unavailable"},
		{name: "nonzero exit", exitCode: 2, wantError: "exit 2; stderr: lookup diagnostic"},
		{name: "empty result", wantError: "invalid entry"},
		{name: "malformed", output: "fixture:x:123", wantError: "invalid entry"},
		{name: "wrong uid", output: "fixture:x:456:20:Fixture:/fixture:/bin/zsh", wantError: "invalid entry"},
		{name: "empty shell defaults to sh", output: "fixture:x:123:20:Fixture:/fixture:", want: "/bin/sh"},
		{name: "multiple entries", output: "fixture:x:123:20:Fixture:/fixture:/bin/zsh\nother", wantError: "invalid entry"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &deps.FakeRunner{}
			runner.Script([]string{"getent", "passwd", "123"}, deps.RunResult{
				Stdout: []byte(tc.output), Stderr: []byte("lookup diagnostic"), ExitCode: tc.exitCode,
			}, tc.err)
			got, err := lookupLoginShell(context.Background(), runner, 123)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("lookup = %q, %v; want error %q", got, err, tc.wantError)
				}
			} else {
				want := tc.want
				if want == "" {
					want = "/bin/zsh"
				}
				if err != nil || got != want {
					t.Fatalf("lookup = %q, %v; want login shell %q", got, err, want)
				}
			}
		})
	}
}
