package themecmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/themesync"
)

// call records what the verb asked of themesync.
type call struct {
	verb  string
	force bool
}

type fakeThemesync struct {
	on      bool
	report  themesync.Report
	err     error
	job     string
	calls   []call
	logged  []string
	seen    map[string]bool
	logFail error
}

func (fake *fakeThemesync) deps(color bool) Deps {
	record := func(verb string) (themesync.Report, error) {
		fake.calls = append(fake.calls, call{verb: verb})
		return fake.report, fake.err
	}
	return Deps{
		Env:   func() (themesync.Env, error) { return themesync.Env{StateDir: "/state", LaunchdJob: fake.job}, nil },
		Color: color,
		Ops: Ops{
			On: func(context.Context, themesync.Env) (themesync.Report, error) { return record("on") },
			Sync: func(_ context.Context, _ themesync.Env, force bool) (themesync.Report, error) {
				fake.calls = append(fake.calls, call{verb: "sync", force: force})
				return fake.report, fake.err
			},
			ReadBack: func(context.Context, themesync.Env) (themesync.Report, error) { return record("status") },
			Off:      func(context.Context, themesync.Env) (themesync.Report, error) { return record("off") },
			SyncOn:   func(string) bool { return fake.on },
			LogOnce: func(_ themesync.Env, line string) (bool, error) {
				fake.logged = append(fake.logged, line)
				if fake.seen == nil {
					fake.seen = map[string]bool{}
				}
				fresh := !fake.seen[line]
				fake.seen = map[string]bool{line: true}
				return fresh, fake.logFail
			},
		},
	}
}

func matrix(verdicts ...themesync.Verdict) themesync.Report {
	report := themesync.Report{
		Enabled: true, Appearance: themesync.Dark, Generation: 7,
		Source: &themesync.SourceInfo{
			Account: "~/.claude", Path: "/h/.claude/settings.json", Value: "custom:professor-gold",
			Title: "Professor Gold · Tokyo Night", Side: themesync.Dark,
		},
	}
	for index, verdict := range verdicts {
		report.Surfaces = append(report.Surfaces, themesync.SurfaceResult{
			Surface: []string{"claude", "tmux", "herdr", "vscode"}[index], Verdict: verdict,
			Generation: 7, Path: "/p/" + string(verdict), Detail: "d-" + string(verdict),
		})
	}
	return report
}

func TestRunVerbs(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		fake       fakeThemesync
		wantCode   int
		wantCalls  []call
		wantStdout []string
		wantStderr []string
	}{
		{
			name: "bare theme is status", args: nil,
			fake:     fakeThemesync{on: true, report: matrix(themesync.VerdictSource, themesync.VerdictInSync)},
			wantCode: 0, wantCalls: []call{{verb: "status"}},
			wantStdout: []string{
				"pfm theme · custom:professor-gold (Professor Gold · Tokyo Night) · dark · gen 7",
				"claude", "SOURCE", "tmux", "IN-SYNC", "/p/IN-SYNC", "d-IN-SYNC",
				"in sync · 1 of 1 surfaces at gen 7",
			},
		},
		{
			name: "status with the sync off prints the off line and reads nothing", args: []string{"status"},
			fake:     fakeThemesync{on: false},
			wantCode: 0, wantStdout: []string{"pfm theme: off — pfm theme on turns it on"},
		},
		{
			name: "status exits 1 on drift and names the surface", args: []string{"status"},
			fake:     fakeThemesync{on: true, report: matrix(themesync.VerdictSource, themesync.VerdictDrift)},
			wantCode: 1, wantCalls: []call{{verb: "status"}},
			wantStdout: []string{"DRIFT", "OUT OF SYNC · tmux DRIFT"},
		},
		{
			name:       "a source error is an ERROR row and exits 1",
			args:       []string{"status"},
			fake:       fakeThemesync{on: true, report: matrix(themesync.VerdictError)},
			wantCode:   1,
			wantCalls:  []call{{verb: "status"}},
			wantStdout: []string{"ERROR", "OUT OF SYNC · claude ERROR"},
		},
		{
			name: "pending and absent keep exit 0 and are named", args: []string{"status"},
			fake: fakeThemesync{on: true, report: matrix(
				themesync.VerdictSource, themesync.VerdictInSync, themesync.VerdictPending, themesync.VerdictAbsent)},
			wantCode: 0, wantCalls: []call{{verb: "status"}},
			wantStdout: []string{"PENDING", "ABSENT", "in sync · 1 of 3 surfaces at gen 7 · 1 pending · 1 absent"},
		},
		{
			name: "status read failure is an error not an off line", args: []string{"status"},
			fake:     fakeThemesync{on: true, err: errors.New("state unreadable")},
			wantCode: 1, wantCalls: []call{{verb: "status"}}, wantStderr: []string{"pfm theme: state unreadable"},
		},
		{
			name: "on follows the source", args: []string{"on"},
			fake:     fakeThemesync{report: matrix(themesync.VerdictSource, themesync.VerdictInSync)},
			wantCode: 0, wantCalls: []call{{verb: "on"}}, wantStdout: []string{"IN-SYNC"},
		},
		{
			name: "on failure exits 1 with the cause",
			args: []string{"on"},
			fake: fakeThemesync{
				err: errors.New("custom theme gold: open /h/.claude/themes/gold.json: no such file or directory"),
			},
			wantCode:   1,
			wantCalls:  []call{{verb: "on"}},
			wantStderr: []string{"pfm theme: custom theme gold"},
		},
		{
			name: "sync", args: []string{"sync"},
			fake:     fakeThemesync{on: true, report: matrix(themesync.VerdictSource, themesync.VerdictInSync)},
			wantCode: 0, wantCalls: []call{{verb: "sync"}}, wantStdout: []string{"IN-SYNC"},
		},
		{
			name: "sync --force", args: []string{"sync", "--force"},
			fake:     fakeThemesync{on: true, report: matrix(themesync.VerdictApplied)},
			wantCode: 0, wantCalls: []call{{verb: "sync", force: true}}, wantStdout: []string{"APPLIED"},
		},
		{
			name: "sync with the sync off is a quiet success", args: []string{"sync"},
			fake:     fakeThemesync{on: false},
			wantCode: 0, wantStdout: []string{"pfm theme: off"},
		},
		{
			name: "off restores and prints the restored rows", args: []string{"off"},
			fake: fakeThemesync{report: themesync.Report{Surfaces: []themesync.SurfaceResult{
				{Surface: "tmux", Verdict: themesync.VerdictRestored, Path: "/s"},
			}}},
			wantCode: 0, wantCalls: []call{{verb: "off"}},
			wantStdout: []string{"pfm theme · off", "RESTORED"},
		},
		{
			name: "off with nothing to restore prints the off line", args: []string{"off"},
			fake:     fakeThemesync{},
			wantCode: 0, wantCalls: []call{{verb: "off"}}, wantStdout: []string{"pfm theme: off"},
		},
		{
			name: "off failure exits 1", args: []string{"off"},
			fake:     fakeThemesync{err: errors.New("snapshot missing")},
			wantCode: 1, wantCalls: []call{{verb: "off"}}, wantStderr: []string{"pfm theme: snapshot missing"},
		},
		{
			name: "the retired set verb is an unknown subcommand", args: []string{"set", "tokyo-night"},
			fake: fakeThemesync{}, wantCode: 2,
			wantStderr: []string{`unknown subcommand "set"`, "usage: pfm theme [status|sync|on|off]"},
		},
		{
			name: "the retired ls verb is an unknown subcommand", args: []string{"ls"},
			fake: fakeThemesync{}, wantCode: 2, wantStderr: []string{`unknown subcommand "ls"`},
		},
		{
			name: "unknown verb is usage", args: []string{"paint"},
			fake: fakeThemesync{}, wantCode: 2, wantStderr: []string{`unknown subcommand "paint"`, "usage: pfm theme"},
		},
		{
			name: "help exits 0", args: []string{"--help"},
			fake: fakeThemesync{}, wantCode: 0, wantStdout: []string{"usage: pfm theme"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			fake := tc.fake
			code := RunVerb(context.Background(), tc.args, fake.deps(false), &stdout, &stderr)
			if code != tc.wantCode {
				t.Fatalf(
					"exit %d, want %d\nstdout: %s\nstderr: %s",
					code,
					tc.wantCode,
					stdout.String(),
					stderr.String(),
				)
			}
			if len(fake.calls) != len(tc.wantCalls) {
				t.Fatalf("calls %+v, want %+v", fake.calls, tc.wantCalls)
			}
			for index, want := range tc.wantCalls {
				if fake.calls[index] != want {
					t.Fatalf("call %d = %+v, want %+v", index, fake.calls[index], want)
				}
			}
			for _, want := range tc.wantStdout {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("stdout lacks %q:\n%s", want, stdout.String())
				}
			}
			for _, want := range tc.wantStderr {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("stderr lacks %q:\n%s", want, stderr.String())
				}
			}
		})
	}
}

func TestEnvFailureExits1(t *testing.T) {
	fake := fakeThemesync{on: true}
	deps := fake.deps(false)
	deps.Env = func() (themesync.Env, error) { return themesync.Env{}, errors.New("no home") }
	var stdout, stderr bytes.Buffer
	if code := RunVerb(
		context.Background(),
		[]string{"status"},
		deps,
		&stdout,
		&stderr,
	); code != 1 ||
		!strings.Contains(stderr.String(), "pfm theme: no home") {
		t.Fatalf("exit %d stderr %q", code, stderr.String())
	}
}

// The theme verb tells themesync which launchd job it runs as, so the
// follower's own job never boots itself out.
func TestSurfaceEnvCarriesTheLaunchdJob(t *testing.T) {
	values := paths.Values{Home: "/h", TmuxDir: "/h/tmux"}
	for _, tc := range []struct{ name, job string }{
		{"in the follower job", "com.professor.pfm.theme"},
		{"in a shell", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := &paths.MapEnv{Values: map[string]string{}}
			if tc.job != "" {
				env.Values["XPC_SERVICE_NAME"] = tc.job
			}
			got, err := surfaceEnv(values, env)
			if err != nil {
				t.Fatal(err)
			}
			if got.LaunchdJob != tc.job {
				t.Errorf("LaunchdJob = %q, want %q", got.LaunchdJob, tc.job)
			}
			if got.PfmBinary != "/h/.local/bin/pfm" || got.StateDir != themesync.DefaultStateDir("/h") {
				t.Errorf("surfaceEnv = %+v", got)
			}
		})
	}
}
