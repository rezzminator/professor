package mcpserv

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// newFixtureService is the package's protocol-test service over the real
// verb layer: chat.Verbs on the default runtime, which reads the jailed paths
// setupBackendFixture laid down.
func newFixtureService(t *testing.T) *Service {
	t.Helper()
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewConfigured("test", io.Discard, Runtime{
		Paths:                resolved,
		Chat:                 chat.Verbs{Warnings: io.Discard},
		AllowAmbientIdentity: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func seedKeysHerdrOwner(t *testing.T, socket string) {
	t.Helper()
	t.Setenv(paths.EnvStateDB, filepath.Join(t.TempDir(), "state.db"))
	t.Setenv(paths.EnvProcRoot, t.TempDir())
	values, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	db := fleetdb.OpenSharedState(context.Background(), values)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	seat := fleetdb.Seat{
		SeatID:          "keys-native",
		Backend:         "herdr",
		Engine:          "cc",
		State:           "live",
		HerdrSocket:     socket,
		HerdrPane:       "w6:p3",
		HerdrWorkspace:  "w6",
		HerdrTerminalID: "fixture-w6:p3",
		SeatHostPID:     42,
		SeatHostStart:   100,
	}
	if err := db.PutSeat(context.Background(), seat); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(values.ProcRoot, "stat"), []byte("btime 1000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for pid, parent := range map[int]int{41: 1, 42: 41} {
		dir := filepath.Join(values.ProcRoot, fmt.Sprint(pid))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		fields := strings.Fields("S 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 100")
		fields[1] = fmt.Sprint(parent)
		argv := "/bin/sh\x00"
		if pid == 42 {
			argv = "/fixture/pfm\x00internal\x00seat-host\x00" + seat.SeatID + "\x00"
		}
		for name, body := range map[string]string{"stat": fmt.Sprintf("%d (pfm) %s", pid, strings.Join(fields, " ")), "cmdline": argv} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
}
