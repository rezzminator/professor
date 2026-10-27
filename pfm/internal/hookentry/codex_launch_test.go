package hookentry

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// A Codex run that is not the interactive chat of a pane — every subcommand
// that is no session (login, app-server, ...) and the non-interactive turns
// exec and review, which fire the Codex hooks — loses the four Herdr names and
// the tmux pane names, so no reporter it fires finds the parent's pane. An
// interactive launch, a resume included, keeps its whole environment.
func TestCodexLaunchDropsThePaneOfAChildThatIsNoInteractiveChat(t *testing.T) {
	root := testjail.ShortRoot(t)
	binDir := filepath.Join(root, "bin")
	binary := filepath.Join(binDir, "codex")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := testjail.WriteExecutable(binary, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	t.Setenv("PATH", binDir)
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_PANE_ID", "w1:p1")
	t.Setenv("HERDR_SOCKET_PATH", "/tmp/h.sock")
	t.Setenv("HERDR_BIN_PATH", "/usr/local/bin/herdr")
	t.Setenv("TMUX", "/tmp/tmux-1000/default,1,0")
	t.Setenv("TMUX_PANE", "%3")
	t.Setenv("PFM_KEEP", "kept")
	previous := LaunchExec
	t.Cleanup(func() { LaunchExec = previous })
	paneNames := []string{"HERDR_ENV", "HERDR_PANE_ID", "HERDR_SOCKET_PATH", "HERDR_BIN_PATH", "TMUX", "TMUX_PANE"}
	for _, test := range []struct {
		name     string
		args     []string
		stripped bool
	}{
		{"interactive", nil, false},
		{"interactive with a prompt", []string{"fix the build"}, false},
		{"interactive resume", []string{"resume", "--last"}, false},
		{"interactive fork", []string{"fork", "--last"}, false},
		{"interactive in a directory named like a subcommand", []string{"--cd", "sandbox"}, false},
		{"interactive on a model named like a subcommand", []string{"-m", "review", "fix it"}, false},
		{"exec after options", []string{"-c", "model=o3", "exec", "say hi"}, true},
		{"app-server", []string{"app-server"}, true},
		{"login", []string{"login"}, true},
		{"mcp", []string{"mcp", "list"}, true},
		{"version", []string{"--version"}, true},
		{"help flag", []string{"-h"}, true},
		{"exec", []string{"exec", "say hi"}, true},
		{"exec resume", []string{"exec", "resume", "--last"}, true},
		{"review", []string{"review", "--uncommitted"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var gotEnv []string
			LaunchExec = func(_ string, _, environment []string) error {
				gotEnv = append([]string(nil), environment...)
				return nil
			}
			runtime := config.Runtime{Paths: paths.Values{
				Home:    root,
				StateDB: filepath.Join(root, "state", "pfm.db"),
				SIDDir:  filepath.Join(root, "sid"),
				TmuxDir: filepath.Join(root, "tmux"),
			}}
			runtime.Config.Extensions = map[string]config.Extension{
				"ponytail": {
					Enabled: true,
					Engines: []string{"codex"},
					Env:     map[string]string{"PONYTAIL_SUBAGENT_MATCHER": "^worker$"},
				},
			}
			var stderr bytes.Buffer
			if code := CodexLaunch(test.args, &stderr, runtime); code != 0 {
				t.Fatalf("CodexLaunch code=%d stderr=%q", code, stderr.String())
			}
			if !slices.Contains(gotEnv, "PONYTAIL_SUBAGENT_MATCHER=^worker$") {
				t.Errorf("extension env lost at Codex exec: %q", gotEnv)
			}
			if gotEnv == nil {
				t.Fatalf("CodexLaunch never reached exec")
			}
			names := make([]string, 0, len(gotEnv))
			for _, entry := range gotEnv {
				name, _, _ := strings.Cut(entry, "=")
				names = append(names, name)
			}
			for _, name := range paneNames {
				if present := slices.Contains(names, name); present == test.stripped {
					t.Errorf("%s present=%t in the exec env, want %t", name, present, !test.stripped)
				}
			}
			if !slices.Contains(names, "PFM_KEEP") {
				t.Errorf("an unrelated variable left the exec env: %q", gotEnv)
			}
		})
	}
}

// Every Codex chat pfm starts runs in-process, off the shared app-server daemon, whose hooks reach
// no pane: a launch that opens the TUI gets --no-daemon, unless the user's own arguments already pick
// a transport (codex-cli 0.162.1 refuses --no-daemon beside --remote, and twice). A call that opens no
// TUI gets nothing — `agents` browses the daemon itself.
func TestCodexTransportArgs(t *testing.T) {
	inProcess := []string{"--no-daemon"}
	for _, test := range []struct {
		name string
		args []string
		want []string
	}{
		{"bare chat", nil, inProcess},
		{"a prompt", []string{"fix the build"}, inProcess},
		{"options and a prompt", []string{"-m", "gpt-5", "--cd", "/work", "fix it"}, inProcess},
		{"a model named like a subcommand", []string{"-m", "review", "fix it"}, inProcess},
		{"resume", []string{"resume", "--last"}, inProcess},
		{"fork", []string{"fork", "019ff700-0000-7000-8000-000000000001"}, inProcess},
		{"a prompt after --", []string{"--", "agents"}, inProcess},
		{"--remote after -- is a prompt", []string{"--", "--remote"}, inProcess},
		{"the user's own --no-daemon", []string{"--no-daemon"}, nil},
		{"the user's own --no-daemon after resume", []string{"resume", "--last", "--no-daemon"}, nil},
		{"--remote ADDR", []string{"--remote", "ws://127.0.0.1:4500"}, nil},
		{"--remote=ADDR", []string{"--remote=unix:///tmp/codex.sock"}, nil},
		{"--remote on resume", []string{"resume", "--remote", "ws://h:1", "--last"}, nil},
		{"--remote-auth-token-env", []string{"--remote-auth-token-env", "TOKEN", "--remote", "wss://h"}, nil},
		{"--remote-auth-token-env=ENV", []string{"--remote-auth-token-env=TOKEN"}, nil},
		{"agents browses the daemon", []string{"agents"}, nil},
		{"exec", []string{"exec", "say hi"}, nil},
		{"exec alias", []string{"e", "say hi"}, nil},
		{"review", []string{"review", "--uncommitted"}, nil},
		{"login", []string{"login"}, nil},
		{"app-server", []string{"app-server"}, nil},
		{"remote-control", []string{"remote-control"}, nil},
		{"apply alias", []string{"a"}, nil},
		{"features", []string{"features", "list"}, nil},
		{"version", []string{"--version"}, nil},
		{"help flag", []string{"-h"}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := CodexTransportArgs(test.args); !slices.Equal(got, test.want) {
				t.Fatalf("CodexTransportArgs(%q) = %q, want %q", test.args, got, test.want)
			}
		})
	}
}

// `cx` (and so the picker's fresh Codex chat) execs Codex with the transport decision right after the
// binary, before every other argument; the user's own transport reaches Codex untouched.
func TestCodexLaunchRunsTheChatInProcess(t *testing.T) {
	root := testjail.ShortRoot(t)
	binDir := filepath.Join(root, "bin")
	binary := filepath.Join(binDir, "codex")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := testjail.WriteExecutable(binary, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	t.Setenv("PATH", binDir)
	previous := LaunchExec
	t.Cleanup(func() { LaunchExec = previous })
	for _, test := range []struct {
		name string
		args []string
		want []string
	}{
		{"resume", []string{"resume", "--last"}, []string{binary, "--no-daemon", "resume", "--last"}},
		{"the user's remote", []string{"--remote", "ws://h:1"}, []string{binary, "--remote", "ws://h:1"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var gotArgv []string
			LaunchExec = func(_ string, argv, _ []string) error {
				gotArgv = append([]string(nil), argv...)
				return nil
			}
			runtime := config.Runtime{Paths: paths.Values{
				Home:    root,
				StateDB: filepath.Join(root, "state", "pfm.db"),
				SIDDir:  filepath.Join(root, "sid"),
				TmuxDir: filepath.Join(root, "tmux"),
			}}
			var stderr bytes.Buffer
			if code := CodexLaunch(test.args, &stderr, runtime); code != 0 {
				t.Fatalf("CodexLaunch code=%d stderr=%q", code, stderr.String())
			}
			if !slices.Equal(gotArgv, test.want) {
				t.Fatalf("CodexLaunch(%q) exec argv = %q, want %q", test.args, gotArgv, test.want)
			}
		})
	}
}

func TestCodexLaunchExtensionOwnership(t *testing.T) {
	root := testjail.ShortRoot(t)
	binary := filepath.Join(root, "codex")
	if err := testjail.WriteExecutable(binary, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	t.Setenv("PATH", root)
	t.Setenv("PONYTAIL_USER_EXPORT", "user")
	t.Setenv("PONYTAIL_SUBAGENT_MATCHER", "inherited")
	previous := LaunchExec
	t.Cleanup(func() { LaunchExec = previous })
	for _, mode := range []string{"on", "off", "dropped"} {
		t.Run(mode, func(t *testing.T) {
			ext := config.Extension{
				Enabled: mode != "off",
				Engines: []string{"codex"},
				Env:     map[string]string{"PONYTAIL_SUBAGENT_MATCHER": "configured"},
			}
			if mode == "dropped" {
				ext.Engines = nil
			}
			var env []string
			LaunchExec = func(_ string, _, environment []string) error { env = environment; return nil }
			runtime := config.Runtime{
				Config: config.Config{Extensions: map[string]config.Extension{"ponytail": ext}},
				Paths: paths.Values{
					Home:    root,
					StateDB: filepath.Join(root, "state", "pfm.db"),
					SIDDir:  filepath.Join(root, "sid"),
					TmuxDir: filepath.Join(root, "tmux"),
				},
			}
			var stderr bytes.Buffer
			if code := CodexLaunch(nil, &stderr, runtime); code != 0 {
				t.Fatalf("launch code=%d: %s", code, &stderr)
			}
			if !slices.Contains(env, "PONYTAIL_USER_EXPORT=user") {
				t.Errorf("exec overlay lost unowned export: %v", env)
			}
			want := "inherited"
			if mode == "on" {
				want = "configured"
			}
			if !slices.Contains(env, "PONYTAIL_SUBAGENT_MATCHER="+want) {
				t.Errorf("exec overlay precedence wrong: %v", env)
			}
		})
	}
}
