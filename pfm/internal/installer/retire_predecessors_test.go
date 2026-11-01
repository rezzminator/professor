package installer

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestMigrateLegacyCarrierUsesConfiguredStateDB(t *testing.T) {
	home := t.TempDir()
	writeFixture(t, filepath.Join(home, ".claude", ".cc-ls-hidden"), "retired-chat\n")
	statePath := filepath.Join(home, "operator-state", "pfm.db")
	installer := &engine{
		options: Options{
			MCPConfigPath: testConfigPath(t),
			Mode:          ModeApply,
			Home:          home,
			StateDB:       statePath,
			Stdout:        io.Discard,
		},
		apply: true,
	}
	if err := installer.migrateLegacyCarrier(context.Background()); err != nil {
		t.Fatal(err)
	}
	state := fleetdb.OpenSharedState(context.Background(), paths.Values{StateDB: statePath})
	killed, err := state.KilledAt(context.Background())
	closeErr := state.Close()
	if err != nil || closeErr != nil || killed["retired-chat"] != 0 || len(killed) != 1 {
		t.Fatalf("configured state killed=%v err=%v close=%v", killed, err, closeErr)
	}
}

// An install whose carrier merge into SQLite fails keeps the carrier, so the
// chats it records as killed stay killed for the next run's merge.
func TestInstallKeepsCarrierWhoseKillsDidNotMerge(t *testing.T) {
	home := t.TempDir()
	carrier := filepath.Join(home, ".claude", ".cc-ls-hidden")
	writeFixture(t, carrier, "retired-chat\n")
	blocker := filepath.Join(home, "not-a-dir")
	writeFixture(t, blocker, "")
	var output bytes.Buffer
	_, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t), Mode: ModeApply, Home: home, Runner: &fakeRunner{}, Stdout: &output,
		StateDB: filepath.Join(blocker, "pfm.db"),
	})
	if err == nil || !strings.Contains(err.Error(), "open shared store before carrier retirement") {
		t.Fatalf("install error=%v, want the failed carrier merge; output:\n%s", err, output.String())
	}
	if got := readFixture(t, carrier); got != "retired-chat\n" {
		t.Fatalf("carrier %s = %q, want the unmerged kill kept", carrier, got)
	}
	want := "  skip    keep retired kill carrier " + carrier + ": its kills did not merge into SQLite this run\n"
	if !strings.Contains(output.String(), want) {
		t.Fatalf("output lacks %q:\n%s", want, output.String())
	}
}
