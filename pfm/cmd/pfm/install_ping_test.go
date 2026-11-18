package main

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/installping"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// TestInstallPingNoticeSaysOnOrWhatTurnedItOff: pfm install prints one install
// ping line and sends the install ping only when it says on.
func TestInstallPingNoticeSaysOnOrWhatTurnedItOff(t *testing.T) {
	const on = "pfm: anonymous install ping on (random id, version, OS, Professor plugins; no IP stored) — " +
		"set PFM_INSTALL_PING_OFFLINE=1 or DO_NOT_TRACK=1 to turn it off\n"
	for _, tc := range []struct {
		name     string
		env      map[string]string
		offline  bool
		want     string
		wantSent int
	}{
		{name: "on", env: map[string]string{}, want: on, wantSent: 1},
		{
			name: "PFM_INSTALL_PING_OFFLINE=1", env: map[string]string{"PFM_INSTALL_PING_OFFLINE": "1"},
			want: "pfm: install ping off (PFM_INSTALL_PING_OFFLINE)\n",
		},
		{
			name: "DO_NOT_TRACK=1", env: map[string]string{"DO_NOT_TRACK": "1"},
			want: "pfm: install ping off (DO_NOT_TRACK)\n",
		},
		{
			name: "--offline", env: map[string]string{}, offline: true,
			want: "pfm: install ping not sent (--offline); the daily ping stays on — " +
				"set PFM_INSTALL_PING_OFFLINE=1 or DO_NOT_TRACK=1 to turn it off\n",
		},
		{
			name: "--offline under DO_NOT_TRACK=1", env: map[string]string{"DO_NOT_TRACK": "1"}, offline: true,
			want: "pfm: install ping off (DO_NOT_TRACK)\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previous := installPingInstall
			t.Cleanup(func() { installPingInstall = previous })
			sent := 0
			installPingInstall = func(context.Context, installping.Options) error {
				sent++
				return errors.New("counter unreachable")
			}
			options := installping.Options{Env: &paths.MapEnv{Values: tc.env}, Version: "1.2.3"}
			var stdout bytes.Buffer
			announceInstallPing(context.Background(), &stdout, tc.offline, options)
			if stdout.String() != tc.want || sent != tc.wantSent {
				t.Fatalf("notice=%q sent=%d, want %q sent=%d", stdout.String(), sent, tc.want, tc.wantSent)
			}
		})
	}
}
