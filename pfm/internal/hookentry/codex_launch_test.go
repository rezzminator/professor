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
			var stderr bytes.Buffer
			if code := CodexLaunch(test.args, &stderr, runtime); code != 0 {
				t.Fatalf("CodexLaunch code=%d stderr=%q", code, stderr.String())
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
		name       string
		args       []string
		want       []string
		primary    string
		wantStderr string
	}{
		{"resume", []string{"resume", "--last"}, []string{binary, "--no-daemon", "resume", "--last"}, "", ""},
		{"the user's remote", []string{"--remote", "ws://h:1"}, []string{binary, "--remote", "ws://h:1"}, "", ""},
		{
			"an unreadable primary account launches on the implicit one",
			[]string{"resume", "--last"},
			[]string{binary, "--no-daemon", "resume", "--last"},
			"garbage",
			"pfm: primary account unreadable (read primary account: parse primary account mirror: " +
				`strconv.Atoi: parsing "garbage": invalid syntax); launching on account 1` + "\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			mirror := filepath.Join(root, ".claude-primary")
			if test.primary == "" {
				if err := os.Remove(mirror); err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(mirror, []byte(test.primary), 0o600); err != nil {
				t.Fatal(err)
			}
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
			if stderr.String() != test.wantStderr {
				t.Fatalf("CodexLaunch(%q) stderr = %q, want %q", test.args, stderr.String(), test.wantStderr)
			}
		})
	}
}

// A Codex workbench persona whose mirror build or model alias fails launches
// with the rest of the persona and says what it left out; a workbench that
// does not enable Codex stays refused, naming its fix.
func TestCodexLaunchWorkbenchPersonaParts(t *testing.T) {
	root := testjail.ShortRoot(t)
	binDir := filepath.Join(root, "bin")
	binary := filepath.Join(binDir, "codex")
	engineSync := filepath.Join(root, "engine-sync")
	for path, body := range map[string]string{
		binary:     "#!/bin/sh\n",
		engineSync: "#!/bin/sh\nprintf '%s\\n' '{\"ok\":false,\"problems\":[\"mirror boom\"]}'\nexit 1\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := testjail.WriteExecutable(path, []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	agent := filepath.Join(root, ".claude", "agents", "astra.md")
	if err := os.MkdirAll(agent, 0o700); err != nil { // a directory: the alias cannot be read
		t.Fatal(err)
	}
	dir := filepath.Join(root, "acme", "docs", "scribe")
	t.Chdir(root)
	t.Setenv("PATH", binDir)
	t.Setenv("ENGINE_SYNC_BIN", engineSync)
	previous := LaunchExec
	t.Cleanup(func() { LaunchExec = previous })
	for _, test := range []struct {
		name, manifest, wantStderr string
		code                       int
		want                       []string
	}{
		{
			name:     "mirror and model unresolvable",
			manifest: `{"prompt":"scribe.md","engines":["codex"],"model":"astra","effort":"high"}`,
			want: []string{
				binary, "--no-daemon", "-c", `model_reasoning_effort="high"`,
				"-c", "developer_instructions=\"\"\"\nYou are scribe.\"\"\"",
			},
			wantStderr: "launch Codex: build the codex mirror of workbench " + dir +
				": mirror boom; launching on the mirror as it stands\n" +
				`launch Codex: codex model "astra": read agent persona ` + agent + ": read " + agent +
				": is a directory; launching on Codex's default model\n",
		},
		{
			name:     "codex not enabled",
			manifest: `{"prompt":"scribe.md"}`,
			code:     1,
			wantStderr: "launch Codex: workbench " + dir + ` does not enable codex: add "codex" to "engines" in ` +
				paths.WorkbenchManifest(dir) + "\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			for path, body := range map[string]string{
				filepath.Join(root, "acme", ".professor", "baseline.json"): "{}",
				paths.WorkbenchManifest(dir):                               test.manifest,
				filepath.Join(dir, ".professor", "scribe.md"):              "You are scribe.",
			} {
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			t.Chdir(dir)
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
			code := CodexLaunch(nil, &stderr, runtime)
			if code != test.code || !slices.Equal(gotArgv, test.want) || stderr.String() != test.wantStderr {
				t.Fatalf("CodexLaunch code=%d argv=%q stderr=%q\nwant code=%d argv=%q stderr=%q",
					code, gotArgv, stderr.String(), test.code, test.want, test.wantStderr)
			}
		})
	}
}
