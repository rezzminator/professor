package hookentry

import (
	"context"
	"errors"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installping"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// TestInstallPingSendsOnlyWhenOn: pfm install sends its ping when enabled,
// and not under an off switch, a config error or --offline.
func TestInstallPingSendsOnlyWhenOn(t *testing.T) {
	for _, tc := range []struct {
		name      string
		env       map[string]string
		offline   bool
		configOff bool
		configErr error
		wantSent  int
	}{
		{name: "installPing false", env: map[string]string{}, configOff: true},
		{name: "installPing false under --offline", env: map[string]string{}, configOff: true, offline: true},
		{name: "unreadable config", env: map[string]string{}, configErr: errors.New("parse config /x at byte 3")},
		{name: "on", env: map[string]string{}, wantSent: 1},
		{name: "PFM_INSTALL_PING_OFFLINE=1", env: map[string]string{"PFM_INSTALL_PING_OFFLINE": "1"}},
		{name: "DO_NOT_TRACK=1", env: map[string]string{"DO_NOT_TRACK": "1"}},
		{name: "--offline", env: map[string]string{}, offline: true},
		{name: "--offline under DO_NOT_TRACK=1", env: map[string]string{"DO_NOT_TRACK": "1"}, offline: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previous := installPingInstall
			t.Cleanup(func() { installPingInstall = previous })
			sent := 0
			installPingInstall = func(context.Context, installping.Options) error {
				sent++
				return errors.New("counter unreachable")
			}
			runtime := config.Runtime{
				Version: "1.2.3", Config: config.Config{InstallPing: !tc.configOff}, ConfigError: tc.configErr,
			}
			InstallPing(context.Background(), tc.offline, runtime, &paths.MapEnv{Values: tc.env})
			if sent != tc.wantSent {
				t.Fatalf("sent=%d, want %d", sent, tc.wantSent)
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
