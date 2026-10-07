package installer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUnitStateRunningReadsEveryState(t *testing.T) {
	for state, want := range map[string][2]bool{
		"active": {true, true}, "activating": {true, true}, "deactivating": {true, true},
		"reloading": {true, true}, "refreshing": {true, true},
		"inactive": {false, true}, "failed": {false, true}, "": {false, false}, "maintenance": {false, false},
	} {
		running, known := unitStateRunning(state)
		if running != want[0] || known != want[1] {
			t.Fatalf("unitStateRunning(%q) = (%v, %v), want %v", state, running, known, want)
		}
	}
}

func TestVerifyFleetUnitsActiveNamesUnreadableState(t *testing.T) {
	err := verifyFleetUnitsActive(
		context.Background(),
		stateRunner{err: errors.New("unreadable state")},
		[]string{mcpUnitName},
	)
	if err == nil || !strings.Contains(err.Error(), "fleet unit pfm-mcp.service state unreadable after start: ") {
		t.Fatalf("verify error = %v", err)
	}
}

func TestInstallReportsMCPRestartThatDoesNotComeBack(t *testing.T) {
	t.Parallel()
	if schedulerIsLaunchd {
		return
	}
	want := "fleet unit pfm-mcp.service is failed 3s after start — journalctl --user -u pfm-mcp.service -n 20"
	for _, laterFails := range []bool{false, true} {
		t.Run(fmt.Sprintf("later step fails=%v", laterFails), func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			runner := &fakeRunner{manager: true, mcpState: "failed"}
			var output bytes.Buffer
			settled := time.Duration(0)
			_, err := Run(context.Background(), Options{
				MCPConfigPath: testConfigPath(t),
				Mode:          ModeApply, Home: home, Runner: runner, Stdout: &output,
				MCPEnabled: map[string]bool{"chat": true},
				Sleep: func(d time.Duration) {
					settled += d
					if laterFails {
						// The ledger tears during the settle, after the plan read
						// it: wireVSCode, a later step, fails.
						writeFixture(t, filepath.Join(managedRootForHome(home), vscodeOwnershipName), "{")
					}
				},
			})
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("install error = %v, want %q\n%s", err, want, output.String())
			}
			if settled < fleetUnitSettle {
				t.Fatalf("restart verified after %s, want the %s settle", settled, fleetUnitSettle)
			}
			text := output.String()
			if strings.Contains(text, "skip    systemctl --user restart") {
				t.Fatalf("restart failure printed as a skip:\n%s", text)
			}
			failed := strings.Index(text, want)
			if !laterFails && (failed < 0 || !strings.Contains(text[failed:], "zshrc")) {
				t.Fatalf("later steps did not run after the failed restart:\n%s", text)
			}
		})
	}
}
