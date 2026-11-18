package hookentry

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
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
			var stdout bytes.Buffer
			InstallPing(context.Background(), &stdout, tc.offline,
				config.Runtime{Version: "1.2.3"}, &paths.MapEnv{Values: tc.env})
			if stdout.String() != tc.want || sent != tc.wantSent {
				t.Fatalf("notice=%q sent=%d, want %q sent=%d", stdout.String(), sent, tc.want, tc.wantSent)
			}
		})
	}
}

// TestDailyInstallPingDuringWaitsOutThePing: the daily ping carries the
// runtime's version, and work that returns at once is held until the ping has
// finished.
func TestDailyInstallPingDuringWaitsOutThePing(t *testing.T) {
	previous := installPingDaily
	t.Cleanup(func() { installPingDaily = previous })
	worked := make(chan struct{})
	pinged := make(chan string, 1)
	installPingDaily = func(_ context.Context, options installping.Options) error {
		<-worked // work has already returned
		pinged <- options.Version
		return nil
	}
	code := DailyInstallPingDuring(context.Background(), config.Runtime{Version: "1.2.3"},
		&paths.MapEnv{Values: map[string]string{}}, func() int {
			close(worked)
			return 7
		})
	select {
	case version := <-pinged:
		if code != 7 || version != "1.2.3" {
			t.Fatalf("code=%d daily ping version=%q, want 7 and 1.2.3", code, version)
		}
	default:
		t.Fatalf("code=%d returned before the daily install ping finished", code)
	}
}
