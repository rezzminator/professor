package hookentry

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestLaunchPassthroughRemoteControl(t *testing.T) {
	previous := LaunchExec
	t.Cleanup(func() { LaunchExec = previous })
	for _, scenario := range []string{"forced", "managed tmux", "named", "account off", "print", "help", "subcommand"} {
		t.Run(scenario, func(t *testing.T) {
			root := testjail.Fleet(t)
			account := filepath.Join(root, "account")
			if err := os.MkdirAll(account, 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CLAUDE_CONFIG_DIR", account)
			t.Setenv("PFM_LAUNCH_PASSTHROUGH", "1")
			t.Setenv("TMUX", "")
			machine := config.Defaults(root, nil)
			machine.Claude.CostLedger = false
			args := []string{"--resume", "session"}
			want, name := true, ""
			switch scenario {
			case "managed tmux":
				t.Setenv("PFM_LAUNCH_PASSTHROUGH", "")
				t.Setenv("TMUX", "/fixture/cc-1,123,0")
			case "named":
				args = append(args, "--name", "desk")
				name = "desk"
			case "account off":
				prefs := machine.Claude
				prefs.RemoteControl = false
				machine.Accounts = []config.Account{{ID: 7, ConfigDir: account, Claude: &prefs}}
				args = append(args, "--remote-control", "stale")
				want = false
			case "print":
				args, want = []string{"--print", "query"}, false
			case "help":
				args, want = []string{"--help"}, false
			case "subcommand":
				args, want = []string{"mcp", "list"}, false
			}
			var got []string
			LaunchExec = func(_ string, argv, _ []string) error { got = slices.Clone(argv[1:]); return nil }
			var output bytes.Buffer
			code := Launch(
				append([]string{"--real", "/bin/echo", "--"}, args...),
				&output,
				&output,
				config.Runtime{Config: machine},
				paths.OSEnv{},
			)
			if code != 0 {
				t.Fatalf("launch code=%d: %s", code, output.String())
			}
			at := slices.Index(got, "--remote-control")
			if (at >= 0) != want {
				t.Fatalf("pass-through remote control present=%t want=%t: %q", at >= 0, want, got)
			}
			if want && (at+1 >= len(got) || (name != "" && got[at+1] != name) || (name == "" && got[at+1][0] != '-')) {
				t.Fatalf("unsafe or missing Remote Control name: %q", got)
			}
			if (scenario == "help" || scenario == "subcommand" || scenario == "print") &&
				!reflect.DeepEqual(got, args) {
				t.Fatalf("noninteractive pass-through changed: %q", got)
			}
		})
	}
}
