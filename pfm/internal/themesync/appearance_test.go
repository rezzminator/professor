package themesync

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

func TestAutoAppearanceUnsupportedPlatforms(t *testing.T) {
	platforms := []string{"linux", "freebsd", "windows"}
	if runtime.GOOS != "darwin" {
		platforms = append(platforms, "") // production defaults to runtime.GOOS
	}
	operations := []struct {
		name string
		run  func(context.Context, Env) (Report, error)
	}{
		{"On", On},
		{"Sync", func(ctx context.Context, env Env) (Report, error) { return Sync(ctx, env, false) }},
		{"ForceSync", func(ctx context.Context, env Env) (Report, error) { return Sync(ctx, env, true) }},
		{"ReadBack", ReadBack},
	}
	for _, goos := range platforms {
		for _, op := range operations {
			t.Run(goos+"/"+op.name, func(t *testing.T) {
				h := newLinuxHarness(t)
				stateBefore := ""
				if op.name != "On" {
					h.on() // explicit themes remain supported on Linux
					stateBefore = h.read(".local/state/pfm/theme/state.json")
				}
				h.write(settingsRel, `{"theme":"auto"}`)
				h.rearm() // defaults would misleadingly succeed with Dark
				h.env.GOOS = goos
				herdrBefore, callsBefore := h.read(".config/herdr/config.toml"), h.tmux.applyCalls
				report, err := op.run(context.Background(), h.env)
				want := "auto appearance detection unsupported; choose an explicit theme/appearance"
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("%s = %+v, %v; want actionable unsupported-auto error", op.name, report, err)
				}
				if !reflect.DeepEqual(report, Report{}) {
					t.Errorf("%s returned a guessed appearance or report: %+v", op.name, report)
				}
				if calls := h.runner.Calls(); len(calls) != 0 {
					t.Errorf("unsupported detection ran external commands: %+v", calls)
				}
				if h.tmux.applyCalls != callsBefore || h.read(".config/herdr/config.toml") != herdrBefore {
					t.Error("unsupported auto changed a surface")
				}
				if stateBefore == "" {
					if h.exists(".local/state/pfm/theme/state.json") {
						t.Error("unsupported auto created theme state")
					}
				} else if h.read(".local/state/pfm/theme/state.json") != stateBefore {
					t.Error("unsupported auto changed theme state")
				}
			})
		}
	}
}

func TestAppearanceForExplicitTheme(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "freebsd", "windows", ""} {
		for _, side := range []Appearance{Dark, Light} {
			t.Run(goos+"/"+string(side), func(t *testing.T) {
				env := Env{GOOS: goos, Runner: new(deps.FakeRunner)}
				got, err := appearanceFor(context.Background(), env, Applied{Side: side})
				if err != nil || got != side {
					t.Fatalf("explicit appearance = %q, %v; want %q", got, err, side)
				}
			})
		}
	}
}

func TestDetectAppearance(t *testing.T) {
	defaults := []string{"defaults", "read", "-g", "AppleInterfaceStyle"}
	cases := []struct {
		name    string
		result  deps.RunResult
		runErr  error
		want    Appearance
		wantErr string
	}{
		{name: "Dark is dark", result: deps.RunResult{Stdout: []byte("Dark\n")}, want: Dark},
		{
			name: "missing key is light",
			result: deps.RunResult{
				ExitCode: 1,
				Stderr: []byte(
					"The domain/default pair of (kCFPreferencesAnyApplication, AppleInterfaceStyle) does not exist\n",
				),
			},
			want: Light,
		},
		{
			name:    "any other failure is an error",
			result:  deps.RunResult{ExitCode: 1, Stderr: []byte("boom")},
			wantErr: "boom",
		},
		{name: "an unknown value is an error", result: deps.RunResult{Stdout: []byte("Sepia\n")}, wantErr: "Sepia"},
		{name: "a start failure is an error", runErr: errors.New("no such file"), wantErr: "no such file"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := new(deps.FakeRunner)
			runner.Script(defaults, tc.result, tc.runErr)
			got, err := DetectAppearance(context.Background(), Env{GOOS: "darwin", Runner: runner})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want one containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("DetectAppearance = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}
