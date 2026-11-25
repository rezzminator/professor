package hookentry

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestStartSeatEngineStartsThroughItsRunner pins the seam SeatHostWith hands
// down: a non-Claude seat engine starts through the runner the entry received,
// with the argv and options the host built, and its process comes back.
func TestStartSeatEngineStartsThroughItsRunner(t *testing.T) {
	fake := &deps.FakeRunner{}
	argv := []string{"/fixture/codex", "--no-daemon"}
	options := deps.StartOptions{Argv0: "codex", Dir: "/fixture/work", Env: []string{"KEEP=engine"}}
	process, err := startSeatEngine(fake, argv, options)
	if err != nil {
		t.Fatal(err)
	}
	if pid := process.Pid(); pid != 4242 {
		t.Fatalf("process pid = %d, want the fake runner's 4242", pid)
	}
	starts := fake.Starts()
	if len(starts) != 1 || !slices.Equal(starts[0].Argv, argv) || starts[0].Opts.Dir != "/fixture/work" ||
		!slices.Equal(starts[0].Opts.Env, []string{"KEEP=engine"}) {
		t.Fatalf("runner starts = %+v, want one start of %q in /fixture/work with KEEP=engine", starts, argv)
	}
}

// The seat host's door: a recipe over the host's own environment, the pane's
// identity taken from the host whatever the recipe holds of it.
func TestSeatRunEnvironment(t *testing.T) {
	host := []string{
		"HOME=/seat/home", "CLAUDE_PID=7", "CLAUDE_CONFIG_DIR=/store/1", "HERDR_ENV=1",
		"HERDR_PANE_ID=w9:p2", "TERM=xterm-256color", "LC_CTYPE=C.UTF-8",
	}
	for _, tc := range []struct {
		name string
		run  fleetdb.SeatRun
		host []string
		want []string
	}{
		{
			name: "delta-over-host",
			run: fleetdb.SeatRun{
				EnvDelta: true, Unset: []string{"CLAUDE_PID", "CLAUDE_CONFIG_DIR"},
				Env: []string{"CLAUDE_CONFIG_DIR=/store/2", "HERDR_PANE_ID=w1:p1", "TERM=dumb"},
			},
			want: []string{
				"HOME=/seat/home", "CLAUDE_CONFIG_DIR=/store/2",
				"HERDR_ENV=1", "HERDR_PANE_ID=w9:p2", "TERM=xterm-256color", "LC_CTYPE=C.UTF-8",
			},
		},
		{
			name: "user-pager-kept-on-a-plain-host",
			run:  fleetdb.SeatRun{EnvDelta: true, Env: []string{"CLAUDE_CONFIG_DIR=/store/2"}},
			host: []string{"HOME=/seat/home", "NO_COLOR=1", "PAGER=less"},
			want: []string{"HOME=/seat/home", "NO_COLOR=1", "PAGER=less", "CLAUDE_CONFIG_DIR=/store/2"},
		},
		{
			name: "codex-tool-shell-host-stripped",
			run:  fleetdb.SeatRun{EnvDelta: true, Env: []string{"CLAUDE_CONFIG_DIR=/store/2"}},
			host: []string{"HOME=/seat/home", "CODEX_TOOL_CALL_ID=call-1", "NO_COLOR=1", "PAGER=cat"},
			want: []string{"HOME=/seat/home", "CLAUDE_CONFIG_DIR=/store/2"},
		},
		{
			name: "full-copy-as-stored",
			run: fleetdb.SeatRun{Env: []string{
				"AUTO_EFFORT_CONTROL_JUDGE=true", "CLAUDE_CONFIG_DIR=/store/1", "HERDR_PANE_ID=w1:p1", "LC_ALL=POSIX",
			}},
			want: []string{
				"AUTO_EFFORT_CONTROL_JUDGE=true", "CLAUDE_CONFIG_DIR=/store/1",
				"HERDR_ENV=1", "HERDR_PANE_ID=w9:p2", "TERM=xterm-256color", "LC_CTYPE=C.UTF-8",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host := host
			if tc.host != nil {
				host = tc.host
			}
			if got := seatRunEnvironment(tc.run, host); !slices.Equal(got, tc.want) {
				t.Fatalf("seatRunEnvironment(%+v, %q) = %q, want %q", tc.run, host, got, tc.want)
			}
		})
	}
}

func TestSeatCommandRefreshesFullPaneContext(t *testing.T) {
	for _, saved := range [][]string{
		{"KEEP=engine"},
		{"KEEP=engine", "HERDR_WORKSPACE_ID=w1", "HERDR_TAB_ID=w1:t1", "HERDR_PANE_ID=w1:p1"},
	} {
		t.Run(strings.Join(saved, ";"), func(t *testing.T) {
			testjail.Fleet(t)
			t.Setenv("HERDR_ENV", "1")
			t.Setenv("HERDR_SOCKET_PATH", "/fixture/herdr.sock")
			t.Setenv("HERDR_WORKSPACE_ID", "w9")
			t.Setenv("HERDR_TAB_ID", "w9:t4")
			t.Setenv("HERDR_PANE_ID", "w9:p2")
			file, err := os.Open(os.DevNull)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = file.Close() }()
			_, options, err := seatCommand(
				fleetdb.SeatRun{Binary: "/fixture/claude", CWD: "/fixture/work", Env: saved},
				"claude",
				file,
				nil,
				nil,
			)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"KEEP=engine", "HERDR_WORKSPACE_ID=w9", "HERDR_TAB_ID=w9:t4", "HERDR_PANE_ID=w9:p2"} {
				if !slices.Contains(options.Env, want) {
					t.Errorf("seat env=%v, missing %s", options.Env, want)
				}
			}
			for _, entry := range options.Env {
				if strings.HasPrefix(entry, "HERDR_WORKSPACE_ID=") && entry != "HERDR_WORKSPACE_ID=w9" ||
					strings.HasPrefix(entry, "HERDR_TAB_ID=") && entry != "HERDR_TAB_ID=w9:t4" {
					t.Errorf("seat env retained stale identity %s", entry)
				}
			}
		})
	}
}

// seatPaneCommand builds the exec'd command for saved inside a fixture Herdr
// pane, the way seat-host does at every door that runs a recipe.
func seatPaneCommand(t *testing.T, saved []string) []string {
	t.Helper()
	testjail.Fleet(t)
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_SOCKET_PATH", "/fixture/herdr.sock")
	t.Setenv("HERDR_PANE_ID", "w9:p2")
	file, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	_, options, err := seatCommand(
		fleetdb.SeatRun{Binary: "/fixture/claude", CWD: "/fixture/work", Env: saved},
		"claude",
		file,
		nil,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	return options.Env
}

// A recipe captured in the launchd daemon carries no terminal; the chat takes
// the terminal identity of the pane seat-host runs in.
func TestSeatCommandTakesPaneTerminalIdentity(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("COLORTERM", "truecolor")
	t.Setenv("TERM_PROGRAM", "herdr")
	env := seatPaneCommand(t, []string{"KEEP=engine", "XPC_SERVICE_NAME=com.fixture.daemon"})
	for _, want := range []string{
		"TERM=xterm-256color", "COLORTERM=truecolor", "TERM_PROGRAM=herdr",
		"KEEP=engine", "XPC_SERVICE_NAME=com.fixture.daemon",
	} {
		if !slices.Contains(env, want) {
			t.Errorf("seat env=%v, missing %s", env, want)
		}
	}
}

// The pane's terminal wins over the recipe's, and a terminal variable the pane
// does not set is dropped rather than revived from the recipe.
func TestSeatCommandPaneTerminalBeatsRecipe(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("LANG", "C.UTF-8")
	t.Setenv("LC_CTYPE", "en_US.UTF-8")
	t.Setenv("COLORTERM", "")
	if err := os.Unsetenv("COLORTERM"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LC_ALL", "")
	if err := os.Unsetenv("LC_ALL"); err != nil {
		t.Fatal(err)
	}
	env := seatPaneCommand(t, []string{
		"TERM=dumb", "LANG=en_GB.ISO8859-1", "LC_ALL=POSIX", "COLORTERM=24bit", "KEEP=engine",
	})
	want := []string{"KEEP=engine", "TERM=xterm-256color", "LANG=C.UTF-8", "LC_CTYPE=en_US.UTF-8"}
	for _, entry := range want {
		if !slices.Contains(env, entry) {
			t.Errorf("seat env=%v, missing %s", env, entry)
		}
	}
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "TERM", "LANG", "LC_CTYPE":
			if !slices.Contains(want, entry) {
				t.Errorf("seat env kept recipe terminal %s", entry)
			}
		case "COLORTERM", "LC_ALL":
			t.Errorf("seat env revived recipe terminal %s the pane does not set", entry)
		}
	}
}

func TestCurrentClaudeBinaryReplacesOnlyAPrunedBuild(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	versions := gather.ClaudeVersionsDir(home)
	kept := filepath.Join(versions, "2.1.316")
	if err := os.MkdirAll(versions, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := testjail.WriteExecutableNeverRun(kept, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ recorded, want string }{
		{filepath.Join(versions, "2.1.308"), kept},
		{kept, kept},
		{filepath.Join(home, "missing-engine"), filepath.Join(home, "missing-engine")},
	} {
		got, err := currentClaudeBinary(test.recorded, home, "")
		if err != nil || got != test.want {
			t.Fatalf("currentClaudeBinary(%q) = %q, %v; want %q", test.recorded, got, err, test.want)
		}
	}
}
