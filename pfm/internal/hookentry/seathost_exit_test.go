package hookentry

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// TestSeatEngineExitLogsAndJudgesAResumedStart: an engine's non-zero exit is
// logged with the recipe's path words, the exit facts and the pane's last
// lines; a resumed engine that exited inside the start window failed its
// start, its cause the pane's error; a clean exit and a hangup log nothing.
func TestSeatEngineExitLogsAndJudgesAResumedStart(t *testing.T) {
	dir := t.TempDir()
	prompt := filepath.Join(dir, "prompt.md")
	if err := os.WriteFile(prompt, []byte("fixture prompt"), 0o600); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(dir, "claude-mcp-gone.json")
	resumed := []string{
		"--mcp-config",
		gone,
		"--system-prompt-file",
		prompt,
		"--resume",
		"11111111-2222-4333-8444-555555555555",
	}
	fresh := []string{
		"--mcp-config",
		gone,
		"--system-prompt-file",
		prompt,
		"--session-id",
		"11111111-2222-4333-8444-555555555555",
	}
	screen := "$ pfm chat open --restored fixture\n\n  Error: Invalid MCP configuration:  \n  MCP config file not found: " +
		gone + "\n\n"
	words := "--mcp-config " + gone + " (missing); --system-prompt-file " + prompt + " (present)"
	for _, row := range []struct {
		name      string
		args      []string
		status    int
		ran       time.Duration
		window    time.Duration // zero: the window opened at the engine's start, ran
		ending    bool
		screenErr error
		want      string
		logged    bool
		cause     string
	}{
		{
			name: "resumed start dies", args: resumed, status: 1, ran: 300 * time.Millisecond, logged: true,
			want: "the engine exited 1 after 300ms, inside its 10s start window: $ pfm chat open --restored fixture | " +
				"  Error: Invalid MCP configuration: |   MCP config file not found: " + gone,
			cause: "$ pfm chat open --restored fixture\n  Error: Invalid MCP configuration:\n  MCP config file not found: " + gone,
		},
		{
			name: "resumed start dies at the window's last millisecond", args: resumed, status: 1,
			ran: 10*time.Second - time.Millisecond, logged: true,
			want: "the engine exited 1 after 9.999s, inside its 10s start window: $ pfm chat open --restored fixture | " +
				"  Error: Invalid MCP configuration: |   MCP config file not found: " + gone,
			cause: "$ pfm chat open --restored fixture\n  Error: Invalid MCP configuration:\n  MCP config file not found: " + gone,
		},
		{
			name: "resumed engine exits at the window", args: resumed, status: 1, ran: 10 * time.Second, logged: true,
			cause: "$ pfm chat open --restored fixture\n  Error: Invalid MCP configuration:\n  MCP config file not found: " + gone,
		},
		{
			name: "fresh start dies", args: fresh, status: 2, ran: 300 * time.Millisecond, logged: true,
			cause: "$ pfm chat open --restored fixture\n  Error: Invalid MCP configuration:\n  MCP config file not found: " + gone,
		},
		{
			name: "pane unreadable", args: resumed, status: 1, ran: time.Second, logged: true,
			screenErr: errors.New("herdr capture refused"),
			want:      "the engine exited 1 after 1s, inside its 10s start window: pane unreadable: herdr capture refused",
			cause:     "pane unreadable: herdr capture refused",
		},
		{
			name: "resumed start dies past the seat host's own window", args: resumed, status: 1,
			ran: 9 * time.Second, window: 10 * time.Second, logged: true,
			cause: "$ pfm chat open --restored fixture\n  Error: Invalid MCP configuration:\n  MCP config file not found: " + gone,
		},
		{
			name: "resumed engine ended by a forwarded TERM", args: resumed, status: 143, ran: time.Second, logged: true,
			cause: "$ pfm chat open --restored fixture\n  Error: Invalid MCP configuration:\n  MCP config file not found: " + gone,
		},
		{
			name: "resumed engine exits under a pending end", args: resumed, status: 1, ran: time.Second, ending: true,
			logged: true,
			cause:  "$ pfm chat open --restored fixture\n  Error: Invalid MCP configuration:\n  MCP config file not found: " + gone,
		},
		{
			name: "resumed engine exits as its Herdr server stops", args: resumed, status: 1, ran: time.Second,
			logged: true, screenErr: &herdr.Refusal{Request: "pane.read", Code: "server_unavailable", Message: "stopping"},
			cause: "pane unreadable: " + (&herdr.Refusal{
				Request: "pane.read", Code: "server_unavailable", Message: "stopping",
			}).Error(),
		},
		{name: "clean exit", args: resumed, status: 0, ran: time.Second},
		{name: "terminal hangup", args: resumed, status: 129, ran: time.Second},
	} {
		t.Run(row.name, func(t *testing.T) {
			ctx, recorder := obs.Test(t)
			read := 0
			exit := seatExit{
				run:    fleetdb.SeatRun{Args: row.args},
				seat:   fleetdb.Seat{SeatID: "fixture-seat", Engine: "cc", HerdrPane: "w2:p4"},
				status: row.status,
				ran:    row.ran,
				window: row.window,
				ending: row.ending,
			}
			if exit.window == 0 {
				exit.window = row.ran
			}
			err := seatEngineExit(ctx, exit, func(context.Context) (string, error) {
				read++
				return screen, row.screenErr
			})
			got := ""
			if err != nil {
				got = err.Error()
			}
			if got != row.want {
				t.Fatalf("seatEngineExit = %q, want %q", got, row.want)
			}
			records := recorder.Records()
			if !row.logged {
				if len(records) != 0 || read != 0 {
					t.Fatalf("records = %+v, screen reads %d; want none for exit %d", records, read, row.status)
				}
				return
			}
			if len(records) != 1 || records[0].Level != "WARN" ||
				records[0].Message != "seat-host: engine exited non-zero" {
				t.Fatalf("records = %+v, want one WARN seat-host: engine exited non-zero", records)
			}
			for key, want := range map[string]any{
				obs.FieldSeat: "fixture-seat", obs.FieldEngine: "cc", "pane": "w2:p4", obs.FieldExit: float64(row.status),
				obs.FieldDur: float64(row.ran.Milliseconds()), "args": words, "cause": row.cause,
			} {
				if value, _ := records[0].Field(key); value != want {
					t.Fatalf("log field %s = %#v, want %#v; record %+v", key, value, want, records[0])
				}
			}
		})
	}
}

// TestSeatStartWindowOpened: a start inside the seat host's first window
// counts from the host's whole-second birth, the instant pfm restart reads it
// back from; a later start, or a host with no birth stamp, from itself.
func TestSeatStartWindowOpened(t *testing.T) {
	birth := time.Unix(1_000, 0)
	for _, row := range []struct {
		name    string
		birth   int64
		started time.Time
		want    time.Time
	}{
		{"first start after birth", 1_000, birth.Add(1500 * time.Millisecond), birth},
		{"start at the window's last instant", 1_000, birth.Add(10*time.Second - time.Nanosecond), birth},
		{"start once the window closed", 1_000, birth.Add(10 * time.Second), birth.Add(10 * time.Second)},
		{"no birth stamp", 0, birth.Add(time.Second), birth.Add(time.Second)},
	} {
		t.Run(row.name, func(t *testing.T) {
			got := seatStartWindowOpened(fleetdb.Seat{SeatHostBirth: row.birth}, row.started)
			if !got.Equal(row.want) {
				t.Fatalf(
					"seatStartWindowOpened(birth %d, started %s) = %s, want %s",
					row.birth,
					row.started,
					got,
					row.want,
				)
			}
		})
	}
}
