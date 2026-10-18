package action

import (
	"context"
	"io"
	"path/filepath"
	"slices"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestHerdrPickerResumeSpawnsNewTab(t *testing.T) {
	for _, kind := range []compose.Kind{compose.ResumeClaude, compose.ResumeCodex, compose.ResumeOpenCode} {
		t.Run(kind.String(), func(t *testing.T) {
			testjail.Fleet(t)
			values, err := paths.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			socket := herdrPickerSocket(t)
			t.Setenv("HERDR_ENV", "1")
			t.Setenv("HERDR_SOCKET_PATH", socket)
			t.Setenv("HERDR_PANE_ID", "w6:p3")
			t.Setenv("TMUX", "")
			home := values.Home
			machine := testMachineConfig(home)
			machine.OpenCodeAccounts = []pfmconfig.OpenCodeAccount{{ID: 2, Home: home + "/opencode"}}
			binary := filepath.Join(home, "engine")
			if err := testjail.WriteExecutable(binary, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			machine.Claude.Binary, machine.Codex.Binary, machine.OpenCode.Binary = binary, binary, binary
			executor, err := New(Dependencies{Tmux: &fakeActionTmux{alive: map[string]bool{}}, Stderr: io.Discard})
			if err != nil {
				t.Fatal(err)
			}
			const id = "abcdef12-1234-1234-1234-123456789abc"
			line, err := executor.Open(context.Background(), Request{
				Row: compose.Row{Kind: kind, ID: id, CWD: home}, Home: home, PrimaryAccount: 2,
				FreshSocket: "unused", Config: machine,
			})
			if err != nil || line != "" {
				t.Fatalf("line=%q err=%v", line, err)
			}
			db := fleetdb.OpenSharedState(context.Background(), values)
			defer func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			}()
			seat, found, err := db.SeatBySession(context.Background(), id)
			if err != nil || !found || seat.HerdrPane != "w6:p9" {
				t.Fatalf("seat=%+v found=%v err=%v", seat, found, err)
			}
			run, err := db.ReadSeatRun(seat.SeatID)
			if err != nil || !slices.Contains(run.Args, id) {
				t.Fatalf("run=%+v err=%v", run, err)
			}
		})
	}
}
