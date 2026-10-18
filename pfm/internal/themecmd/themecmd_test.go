package themecmd

import (
	"bytes"
	"context"
	"encoding/json"
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
	name  string
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
	// list and use are what the list and use verbs get back from themesync.
	list    themesync.ThemeList
	listErr error
	use     themesync.UseResult
	useErr  error
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
			List: func(themesync.Env) (themesync.ThemeList, error) {
				fake.calls = append(fake.calls, call{verb: "list"})
				return fake.list, fake.listErr
			},
			Use: func(_ themesync.Env, name string) (themesync.UseResult, error) {
				fake.calls = append(fake.calls, call{verb: "use", name: name})
				return fake.use, fake.useErr
			},
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
			Account: "~/.claude", Path: "/h/.claude/settings.json", Value: "custom:hibiscus-night",
			Title: "Hibiscus Night", Side: themesync.Dark,
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

// twoAccounts are the settings.json paths of a two-account host.
var twoAccounts = [2]string{"/h/.claude/settings.json", "/h/.cc/1/settings.json"}

func TestRunVerbs(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		fake       fakeThemesync
		wantCode   int
		wantCalls  []call
		wantStdout []string
		wantStderr []string
		notStdout  []string
	}{
		{
			name: "bare theme is status", args: nil,
			fake:     fakeThemesync{on: true, report: matrix(themesync.VerdictSource, themesync.VerdictInSync)},
			wantCode: 0, wantCalls: []call{{verb: "status"}},
			wantStdout: []string{
				"pfm theme · custom:hibiscus-night (Hibiscus Night) · dark · gen 7",
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
				err: errors.New(
					"custom theme hibiscus-night: open /h/.claude/themes/hibiscus-night.json: no such file or directory",
				),
			},
			wantCode:   1,
			wantCalls:  []call{{verb: "on"}},
			wantStderr: []string{"pfm theme: custom theme hibiscus-night"},
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
			name: "use sets the theme, syncs once and tells new chats to follow",
			args: []string{"use", "hibiscus-night"},
			fake: fakeThemesync{
				on:     true,
				report: matrix(themesync.VerdictSource, themesync.VerdictInSync),
				use: themesync.UseResult{
					Value:   "custom:hibiscus-night",
					Written: []string{twoAccounts[0], twoAccounts[1]},
				},
			},
			wantCode:  0,
			wantCalls: []call{{verb: "use", name: "hibiscus-night"}, {verb: "sync"}},
			wantStdout: []string{
				"wrote " + twoAccounts[0], "wrote " + twoAccounts[1], "IN-SYNC",
				"new chats follow; run /theme in open chats",
			},
		},
		{
			name: "use with the sync off says how to repaint", args: []string{"use", "hibiscus-night"},
			fake: fakeThemesync{
				on:  false,
				use: themesync.UseResult{Value: "custom:hibiscus-night", Written: []string{twoAccounts[0]}},
			},
			wantCode:  0,
			wantCalls: []call{{verb: "use", name: "hibiscus-night"}},
			wantStdout: []string{
				"wrote " + twoAccounts[0],
				"theme sync is off: run pfm theme on to repaint tmux, herdr and VS Code",
				"new chats follow; run /theme in open chats",
			},
		},
		{
			name: "a refused account exits 1 naming the row on stderr, and the written one still syncs",
			args: []string{"use", "custom:hibiscus-night"},
			fake: fakeThemesync{
				on: true, report: matrix(themesync.VerdictSource, themesync.VerdictInSync),
				use: themesync.UseResult{
					Value: "custom:hibiscus-night", Written: []string{twoAccounts[0]},
					Refused: []themesync.ThemeListError{{
						Path: twoAccounts[1], Error: "theme file hibiscus-night.json is missing from /h/.cc/1/themes",
					}},
				},
			},
			wantCode:   1,
			wantCalls:  []call{{verb: "use", name: "custom:hibiscus-night"}, {verb: "sync"}},
			wantStdout: []string{"wrote " + twoAccounts[0], "IN-SYNC"},
			wantStderr: []string{
				"refused " + twoAccounts[1] + ": theme file hibiscus-night.json is missing from /h/.cc/1/themes",
			},
		},
		{
			name: "no account written exits 1 with the cause and the refusals, and syncs nothing",
			args: []string{"use", "light"},
			fake: fakeThemesync{
				on: true,
				use: themesync.UseResult{
					Value:   "light",
					Refused: []themesync.ThemeListError{{Path: twoAccounts[0], Error: "disk full"}},
				},
				useErr: errors.New("no account settings.json was written for light"),
			},
			wantCode:  1,
			wantCalls: []call{{verb: "use", name: "light"}},
			wantStderr: []string{
				"refused " + twoAccounts[0] + ": disk full",
				"pfm theme: no account settings.json was written",
			},
			notStdout: []string{"new chats follow", "wrote"},
		},
		{
			name: "an unknown name exits 1 with the valid names",
			args: []string{"use", "nonsense"},
			fake: fakeThemesync{
				on:     true,
				useErr: errors.New(`unknown theme "nonsense"; valid names: auto, dark`),
			},
			wantCode:   1,
			wantCalls:  []call{{verb: "use", name: "nonsense"}},
			wantStderr: []string{`pfm theme: unknown theme "nonsense"; valid names: auto, dark`},
			notStdout:  []string{"new chats follow"},
		},
		{
			name: "a sync that errors exits 1 naming the cause, the written accounts still told",
			args: []string{"use", "dark"},
			fake: fakeThemesync{
				on: true, err: errors.New("state unreadable"),
				use: themesync.UseResult{Value: "dark", Written: []string{twoAccounts[0]}},
			},
			wantCode:   1,
			wantCalls:  []call{{verb: "use", name: "dark"}, {verb: "sync"}},
			wantStdout: []string{"wrote " + twoAccounts[0], "new chats follow"},
			wantStderr: []string{"pfm theme: sync: state unreadable"},
		},
		{
			name: "a surface out of sync after use exits 1 and names it on stderr", args: []string{"use", "dark"},
			fake: fakeThemesync{
				on: true, report: matrix(themesync.VerdictSource, themesync.VerdictDrift),
				use: themesync.UseResult{Value: "dark", Written: []string{twoAccounts[0]}},
			},
			wantCode:   1,
			wantCalls:  []call{{verb: "use", name: "dark"}, {verb: "sync"}},
			wantStdout: []string{"DRIFT", "new chats follow"},
			wantStderr: []string{"sync left surfaces out of sync: tmux DRIFT d-DRIFT"},
		},
		{
			name: "use without a name is usage", args: []string{"use"},
			fake: fakeThemesync{on: true}, wantCode: 2,
			wantStderr: []string{"needs exactly one theme name", "usage: pfm theme", "use {name}"},
		},
		{
			name: "use with two names is usage", args: []string{"use", "hibiscus-night", "ember"},
			fake: fakeThemesync{on: true}, wantCode: 2,
			wantStderr: []string{`unexpected argument "ember"`, "usage: pfm theme"},
		},
		{
			name: "list draws the rows with the sync off and exits 0", args: []string{"list"},
			fake: fakeThemesync{on: false, list: themesync.ThemeList{
				Current: "dark",
				Themes: []themesync.ThemeChoice{
					{Name: "dark", Value: "dark", Title: "dark", Kind: themesync.KindBuiltin, Current: true},
				},
			}},
			wantCode: 0, wantCalls: []call{{verb: "list"}}, wantStdout: []string{"NAME", "VALUE", "TITLE", "* dark"},
		},
		{
			name: "list with a read failure exits 1", args: []string{"list"},
			fake:     fakeThemesync{listErr: errors.New("no Claude account settings.json under ~/.claude or ~/.cc")},
			wantCode: 1, wantCalls: []call{{verb: "list"}},
			wantStderr: []string{"pfm theme: no Claude account settings.json"},
		},
		{
			name: "list takes no positional", args: []string{"list", "hibiscus-night"},
			fake: fakeThemesync{}, wantCode: 2, wantStderr: []string{`unexpected argument "hibiscus-night"`},
		},
		{
			name: "the retired set verb is an unknown subcommand", args: []string{"set", "tokyo-night"},
			fake: fakeThemesync{}, wantCode: 2,
			wantStderr: []string{`unknown subcommand "set"`, "usage: pfm theme [status|sync|on|off|list|use]"},
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
			for _, unwanted := range tc.notStdout {
				if strings.Contains(stdout.String(), unwanted) {
					t.Errorf("stdout holds %q:\n%s", unwanted, stdout.String())
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

func TestListDrawsTheRowsErrorsAndMissingAccountsExactly(t *testing.T) {
	fake := fakeThemesync{list: themesync.ThemeList{
		Current: "custom:hibiscus-night",
		Themes: []themesync.ThemeChoice{
			{
				Name:    "hibiscus-night",
				Value:   "custom:hibiscus-night",
				Title:   "Hibiscus · Night",
				Kind:    themesync.KindCustom,
				Current: true,
				Missing: []string{"~/.cc/1", "~/.cc/2"},
			},
			{Name: "dark", Value: "dark", Title: "dark", Kind: themesync.KindBuiltin},
		},
		Errors: []themesync.ThemeListError{{Path: "/h/.claude/themes", Error: "not a directory"}},
	}}
	var stdout, stderr bytes.Buffer
	code := RunVerb(context.Background(), []string{"list"}, fake.deps(false), &stdout, &stderr)
	want := "  NAME            VALUE                  TITLE\n" +
		"* hibiscus-night  custom:hibiscus-night  Hibiscus · Night  missing in ~/.cc/1, ~/.cc/2\n" +
		"  dark            dark                   dark\n" +
		"ERROR /h/.claude/themes: not a directory\n"
	if code != 1 || stdout.String() != want {
		t.Fatalf(
			"exit %d, want 1 for the Errors row\nstdout:\n%s\nwant:\n%s\nstderr: %s",
			code,
			stdout.String(),
			want,
			stderr.String(),
		)
	}
}

// decodeObject is stdout decoded as a JSON object, its values left raw.
func decodeObject(t *testing.T, stdout string) map[string]json.RawMessage {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &object); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, stdout)
	}
	return object
}

func TestListJSONPrintsEmptyArraysNeverNull(t *testing.T) {
	fake := fakeThemesync{} // themesync answers nil slices
	var stdout, stderr bytes.Buffer
	if code := RunVerb(
		context.Background(),
		[]string{"list", "--json"},
		fake.deps(false),
		&stdout,
		&stderr,
	); code != 0 {
		t.Fatalf("exit %d\nstderr: %s", code, stderr.String())
	}
	object := decodeObject(t, stdout.String())
	for _, key := range []string{"themes", "errors"} {
		if got := strings.TrimSpace(string(object[key])); got != "[]" {
			t.Errorf("%s = %s, want []", key, got)
		}
	}
}

func TestListJSONCarriesTheListAndExits1OnErrorRows(t *testing.T) {
	fake := fakeThemesync{list: themesync.ThemeList{
		Current: "dark",
		Themes: []themesync.ThemeChoice{
			{Name: "dark", Value: "dark", Title: "dark", Kind: themesync.KindBuiltin, Current: true},
		},
		Errors: []themesync.ThemeListError{{Path: "/h/.claude/themes", Error: "not a directory"}},
	}}
	var stdout, stderr bytes.Buffer
	code := RunVerb(context.Background(), []string{"list", "--json"}, fake.deps(false), &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit %d, want 1 with an Errors row\nstderr: %s", code, stderr.String())
	}
	var got themesync.ThemeList
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("list --json is not a ThemeList: %v\n%s", err, stdout.String())
	}
	if got.Current != "dark" || len(got.Themes) != 1 || !got.Themes[0].Current ||
		len(got.Errors) != 1 || got.Errors[0].Path != "/h/.claude/themes" {
		t.Fatalf("list --json = %+v", got)
	}
}

func TestUseJSONPrintsTheResultAndTheReportOrNull(t *testing.T) {
	cases := []struct {
		name       string
		fake       fakeThemesync
		wantCode   int
		wantReport bool
	}{
		{
			name: "sync on",
			fake: fakeThemesync{
				on: true, report: matrix(themesync.VerdictSource, themesync.VerdictInSync),
				use: themesync.UseResult{Value: "dark", Written: []string{twoAccounts[0]}},
			},
			wantCode: 0, wantReport: true,
		},
		{
			name:     "sync off",
			fake:     fakeThemesync{use: themesync.UseResult{Value: "dark", Written: []string{twoAccounts[0]}}},
			wantCode: 0,
		},
		{
			name: "a refusal",
			fake: fakeThemesync{
				on: true, report: matrix(themesync.VerdictSource, themesync.VerdictInSync),
				use: themesync.UseResult{
					Value: "dark", Written: []string{twoAccounts[0]},
					Refused: []themesync.ThemeListError{{Path: twoAccounts[1], Error: "disk full"}},
				},
			},
			wantCode: 1, wantReport: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			fake := tc.fake
			code := RunVerb(context.Background(), []string{"use", "dark", "--json"}, fake.deps(false), &stdout, &stderr)
			if code != tc.wantCode {
				t.Fatalf(
					"exit %d, want %d\nstdout: %s\nstderr: %s",
					code,
					tc.wantCode,
					stdout.String(),
					stderr.String(),
				)
			}
			object := decodeObject(t, stdout.String())
			var use themesync.UseResult
			if err := json.Unmarshal(object["use"], &use); err != nil || use.Value != "dark" || len(use.Written) != 1 {
				t.Fatalf("use = %s (%v)", object["use"], err)
			}
			if strings.Contains(string(object["use"]), "null") {
				t.Errorf("use holds a null: %s", object["use"])
			}
			hasReport := strings.TrimSpace(string(object["report"])) != "null"
			if hasReport != tc.wantReport {
				t.Errorf("report = %s, want present %v", object["report"], tc.wantReport)
			}
			if tc.wantCode == 1 && !strings.Contains(stderr.String(), "refused "+twoAccounts[1]+": disk full") {
				t.Errorf("stderr lacks the cause:\n%s", stderr.String())
			}
		})
	}
}
