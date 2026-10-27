package themecmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/themesync"
)

// TestConvergeTmux: name-sync themes every live server only while the sync is on, and counts the refusals.
func TestConvergeTmux(t *testing.T) {
	cases := []struct {
		name       string
		on         bool
		failSocket string
		wantApply  []string
		wantOut    string
		wantErr    string
		wantBad    int
	}{
		{name: "sync off touches nothing and prints nothing", on: false},
		{
			name:      "sync on themes every live socket",
			on:        true,
			wantApply: []string{"cc-1", "cc-2"},
			wantOut:   "tmux theme applied: 2\n",
		},
		{
			name: "a failure is named and counted, the rest continue", on: true, failSocket: "cc-1",
			wantApply: []string{"cc-1", "cc-2"}, wantOut: "tmux theme applied: 1\n",
			wantErr: "pfm name-sync: tmux theme cc-1: boom", wantBad: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var applied []string
			apply := func(_ context.Context, _ themesync.TmuxServers, _, socket string) error {
				applied = append(applied, socket)
				if socket == tc.failSocket {
					return errors.New("boom")
				}
				return nil
			}
			var stdout, stderr bytes.Buffer
			bad := applyThemeToSockets(
				context.Background(),
				nil,
				"/state",
				[]string{"cc-1", "cc-2"},
				func(string) bool { return tc.on },
				apply,
				&stdout,
				&stderr,
			)
			if bad != tc.wantBad || strings.Join(applied, ",") != strings.Join(tc.wantApply, ",") {
				t.Fatalf("bad %d applied %v", bad, applied)
			}
			if stdout.String() != tc.wantOut || strings.TrimSpace(stderr.String()) != tc.wantErr {
				t.Fatalf("stdout %q stderr %q", stdout.String(), stderr.String())
			}
		})
	}
}
