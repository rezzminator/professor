package claudelaunch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/loginshell"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// resetLoginPATH empties the process's login PATH cache for the test and again after it.
func resetLoginPATH(t *testing.T) {
	t.Helper()
	reset := func() {
		loginPATHCache.Lock()
		loginPATHCache.byShell = map[string]loginPATHResult{}
		loginPATHCache.Unlock()
	}
	reset()
	t.Cleanup(reset)
}

// pinLoginPATH points SHELL at a shell whose login PATH is already known to
// add nothing, so a test of something else neither runs a shell nor warns.
func pinLoginPATH(t *testing.T) {
	t.Helper()
	resetLoginPATH(t)
	const shell = "/pinned/bin/zsh"
	t.Setenv("SHELL", shell)
	loginPATHCache.Lock()
	loginPATHCache.byShell[shell] = loginPATHResult{}
	loginPATHCache.Unlock()
}

// envLookup is a lookup over fixed values.
func envLookup(values map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}

func TestPathEnvMergesTheLoginShellPATH(t *testing.T) {
	const shell = "/login/bin/zsh"
	argv := []string{shell, "-l", "-i", "-c", loginPATHScript}
	// via is the account lookup's first call, the one a failed lookup names, as this kernel spells it.
	via := fmt.Sprintf("%q", loginshell.ScriptAccount(&deps.FakeRunner{}, os.Getuid(), shell, nil)[0])
	printed := func(path string) deps.RunResult {
		return deps.RunResult{Stdout: []byte("motd " + loginPATHStart + "\nrc noise\n" + loginPATHStart + path +
			loginPATHEnd + "\nbye\n")}
	}
	for _, test := range []struct {
		name    string
		shell   string // the account's login shell and, unless env is set, $SHELL
		env     *string
		lookup  error // the account lookup's failure
		launch  *string
		answer  func(ctx context.Context, call deps.RunCall) (deps.RunResult, error)
		timeout bool
		want    []string
		warning string
	}{
		{
			name:   "missing shim dir appended",
			shell:  shell,
			launch: new("/usr/bin:/bin"),
			answer: answer(printed("/opt/mise/shims:/usr/bin"), nil),
			want:   []string{"PATH=/usr/bin:/bin:/opt/mise/shims"},
		},
		{
			name:   "every entry already present",
			shell:  shell,
			launch: new("/usr/bin:/opt/mise/shims:/bin"),
			answer: answer(printed("/opt/mise/shims:/usr/bin"), nil),
		},
		{
			name:   "launch order kept",
			shell:  shell,
			launch: new("/b:/a"),
			answer: answer(printed("/a:/c:/b"), nil),
			want:   []string{"PATH=/b:/a:/c"},
		},
		{
			name:   "duplicates and empty entries dropped",
			shell:  shell,
			launch: new("/a"),
			answer: answer(printed(":/c::/c:/d:/a:"), nil),
			want:   []string{"PATH=/a:/c:/d"},
		},
		{
			name:   "launch PATH unset",
			shell:  shell,
			answer: answer(printed("/c:/d"), nil),
			want:   []string{"PATH=/c:/d"},
		},
		{
			name:   "no markers",
			shell:  shell,
			launch: new("/a"),
			answer: answer(deps.RunResult{Stdout: []byte("/c:/d\n"), ExitCode: 1}, nil),
			warning: shell + ` -l -i exited 1 with no PATH between its markers (stdout "/c:/d\n"): ` +
				"the launch PATH is kept as it is",
		},
		{
			name:   "start marker without end",
			shell:  shell,
			launch: new("/a"),
			answer: answer(deps.RunResult{Stdout: []byte(loginPATHStart + "/c")}, nil),
			warning: shell + ` -l -i exited 0 with no PATH between its markers (stdout "` + loginPATHStart +
				`/c"): the launch PATH is kept as it is`,
		},
		{
			name:    "shell fails to start",
			shell:   shell,
			launch:  new("/a"),
			answer:  answer(deps.RunResult{ExitCode: -1}, errors.New("exec: no such file")),
			warning: shell + " -l -i: exec: no such file: the launch PATH is kept as it is",
		},
		{
			name:    "timeout",
			shell:   shell,
			launch:  new("/a"),
			timeout: true,
			warning: shell + " -l -i printed no PATH within 3s: the launch PATH is kept as it is",
		},
		{
			name:   "account shell beats SHELL",
			shell:  shell,
			env:    new("/usr/bin/bash"),
			launch: new("/usr/bin:/bin"),
			answer: answer(printed("/opt/mise/shims:/usr/bin"), nil),
			want:   []string{"PATH=/usr/bin:/bin:/opt/mise/shims"},
		},
		{
			name:   "account lookup failed, SHELL probed",
			env:    new(shell),
			lookup: errors.New("account database: not found"),
			launch: new("/usr/bin:/bin"),
			answer: answer(printed("/opt/mise/shims:/usr/bin"), nil),
			want:   []string{"PATH=/usr/bin:/bin:/opt/mise/shims"},
			warning: "login shell lookup: lookup login shell via " + via +
				": account database: not found; stderr: ; using SHELL",
		},
		{
			name:   "account lookup failed, SHELL probe fails",
			env:    new(shell),
			lookup: errors.New("account database: not found"),
			launch: new("/a"),
			answer: answer(deps.RunResult{ExitCode: -1}, errors.New("exec: no such file")),
			warning: shell + " -l -i: exec: no such file: the launch PATH is kept as it is (login shell lookup: " +
				"lookup login shell via " + via + ": account database: not found; stderr: ; using SHELL)",
		},
		{
			name:   "account lookup failed and SHELL unset",
			lookup: errors.New("account database: not found"),
			env:    new(""),
			launch: new("/a"),
			warning: "login shell lookup: lookup login shell via " + via +
				": account database: not found; stderr: , and SHELL is unset: the launch PATH is kept as it is",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			resetLoginPATH(t)
			t.Setenv("HOME", "/srv/planted")
			t.Setenv("LC_ALL", "C.UTF-8")
			t.Setenv("XDG_CONFIG_HOME", "/srv/planted/.config")
			t.Setenv("PFM_PLANTED_UNRELATED", "leaked")
			t.Setenv("PATH", "/pfm/own/bin")
			_, recorder := obs.Test(t)
			runner := &deps.FakeRunner{}
			fake := clock.NewFake(time.Unix(0, 0))
			if test.answer != nil {
				runner.Handle(argv, test.answer)
			}
			if test.timeout {
				runner.Handle(argv, func(ctx context.Context, _ deps.RunCall) (deps.RunResult, error) {
					fake.Advance(loginPATHTimeout)
					<-ctx.Done()
					return deps.RunResult{ExitCode: -1}, ctx.Err()
				})
			}
			accountCalls := loginshell.ScriptAccount(runner, os.Getuid(), test.shell, test.lookup)
			values := map[string]string{}
			if test.env != nil {
				values["SHELL"] = *test.env
			} else if test.shell != "" {
				values["SHELL"] = test.shell
			}
			if test.launch != nil {
				values["PATH"] = *test.launch
			}
			wantShell := test.shell
			if test.lookup != nil {
				wantShell = values["SHELL"]
			}
			loginPATH(context.Background(), fake, values["SHELL"], runner)
			got := pathEnvThrough(envLookup(values), runner)
			got = append(got, pathEnvThrough(envLookup(values), runner)...)
			want := append(slices.Clone(test.want), test.want...)
			if !slices.Equal(got, want) {
				t.Fatalf("PATH env over two launches = %q, want %q", got, want)
			}
			calls := []deps.RunCall{}
			lookups := 0
			for _, call := range runner.Calls() {
				if call.Argv[0] == accountCalls[0][0] {
					lookups++
				} else {
					calls = append(calls, call)
				}
			}
			wantCalls, wantLookups := 1, len(accountCalls)
			if test.lookup != nil && values["SHELL"] == "" {
				wantCalls = 0
			}
			if lookups != wantLookups {
				t.Fatalf("account lookups = %d, want %d: asked once per process", lookups, wantLookups)
			}
			if len(calls) != wantCalls {
				t.Fatalf("login shell runs = %d (%v), want %d: asked once per process",
					len(calls), calls, wantCalls)
			}
			wantArgv := []string{wantShell, "-l", "-i", "-c", loginPATHScript}
			if wantCalls == 1 && (!slices.Equal(calls[0].Argv, wantArgv) || !calls[0].Opts.Setsid) {
				t.Fatalf("login shell call = %q setsid=%v, want %q setsid=true",
					calls[0].Argv, calls[0].Opts.Setsid, wantArgv)
			}
			if wantCalls == 1 {
				// The jail seeds TMPDIR and XDG_* of its own, so the env is judged name by name.
				for _, entry := range []string{
					"HOME=/srv/planted", "LC_ALL=C.UTF-8",
					"XDG_CONFIG_HOME=/srv/planted/.config", "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "SHELL=" + wantShell,
				} {
					if !slices.Contains(calls[0].Opts.Env, entry) {
						t.Fatalf("login shell env = %q, want it to carry %q", calls[0].Opts.Env, entry)
					}
				}
				for _, entry := range calls[0].Opts.Env {
					name, _, _ := strings.Cut(entry, "=")
					if !slices.Contains([]string{"HOME", "USER", "LOGNAME", "LANG", "TMPDIR", "SHELL", "PATH"}, name) &&
						!strings.HasPrefix(name, "LC_") &&
						!strings.HasPrefix(name, "XDG_") ||
						entry == "PATH=/pfm/own/bin" {
						t.Fatalf("login shell env = %q, want only a clean login environment, found %q",
							calls[0].Opts.Env, entry)
					}
				}
			}
			warnings := []string{}
			for _, record := range recorder.Records() {
				if record.Message == "claudelaunch: login PATH" {
					value, _ := record.Field(obs.FieldErr)
					warnings = append(warnings, value.(string))
				}
			}
			wantWarnings := []string{}
			if test.warning != "" {
				wantWarnings = []string{test.warning}
			}
			if !slices.Equal(warnings, wantWarnings) {
				t.Fatalf("warnings = %q, want %q", warnings, wantWarnings)
			}
		})
	}
}

// answer is a fake login shell's fixed reply.
func answer(result deps.RunResult, err error) func(context.Context, deps.RunCall) (deps.RunResult, error) {
	return func(context.Context, deps.RunCall) (deps.RunResult, error) { return result, err }
}

// TestLoginPATHInATestProcessRunsNoShell: a launch rendered in a test process,
// with no runner of the test's own, asks no login shell, so this host's startup
// files add no folder to it; the probe's tests pass their runner.
func TestLoginPATHInATestProcessRunsNoShell(t *testing.T) {
	resetLoginPATH(t)
	_, recorder := obs.Test(t)
	got := loginPATH(context.Background(), clock.NewFake(time.Unix(0, 0)), "/bin/sh", nil)
	if got.problem != "" || got.lookupNote != "" || got.entries != nil {
		t.Fatalf("loginPATH in a test process = %+v, want the empty answer that adds nothing", got)
	}
	if path := pathEnvThrough(envLookup(map[string]string{"SHELL": "/bin/sh", "PATH": "/usr/bin"}), nil); path != nil {
		t.Fatalf("pathEnvThrough in a test process = %q, want no PATH assignment", path)
	}
	if records := recorder.Records(); len(records) != 0 {
		t.Fatalf("activity log = %v, want nothing warned", records)
	}
}
