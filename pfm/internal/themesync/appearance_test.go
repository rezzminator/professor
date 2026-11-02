package themesync

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// Auto on a platform with no system appearance to read draws dark (no side was
// applied before) rather than stopping the sync: every surface still follows the
// theme and the claude row names why the side was not read.
func TestAutoAppearanceUnsupportedPlatformsDrawsDarkAndNamesWhy(t *testing.T) {
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
	}
	for _, goos := range platforms {
		for _, op := range operations {
			t.Run(goos+"/"+op.name, func(t *testing.T) {
				h := newLinuxHarness(t)
				if op.name != "On" {
					h.on() // explicit themes remain supported on Linux
				}
				h.write(settingsRel, `{"theme":"auto"}`)
				h.rearm() // defaults would misleadingly succeed with Dark
				h.env.GOOS = goos
				report, err := op.run(context.Background(), h.env)
				if err != nil {
					t.Fatalf("%s: %v", op.name, err)
				}
				wantVerdict(t, report, "claude", VerdictSource, "system appearance unreadable, drawing dark: "+
					"themesync: auto appearance detection unsupported; choose an explicit theme/appearance")
				if report.Appearance != Dark || !report.Enabled || h.tmux.servers["cc-a"]["@pfm-c-bg"] != "#000000" {
					t.Errorf("%s = appearance %s enabled %v tmux bg %q, want the theme drawn dark #000000",
						op.name, report.Appearance, report.Enabled, h.tmux.servers["cc-a"]["@pfm-c-bg"])
				}
				if state := h.state(); state.Appearance != Dark {
					t.Errorf("stored appearance = %q, want dark", state.Appearance)
				}
			})
		}
	}
}

// A read-back writes nothing, so it never needs a guessed side: with the system
// appearance unreadable it still reads every surface, against the appearance last
// applied, and the claude row says it could not look.
func TestReadBackKeepsTheMatrixWhenTheAppearanceCannotBeRead(t *testing.T) {
	h := newLinuxHarness(t)
	h.on() // an explicit theme is supported on Linux
	stateBefore := h.read(".local/state/pfm/theme/state.json")
	h.write(settingsRel, `{"theme":"auto"}`)
	h.rearm()
	report, err := ReadBack(context.Background(), h.env)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Surfaces) != 5 {
		t.Fatalf("read-back rows = %+v, want all five surfaces", report.Surfaces)
	}
	wantVerdict(
		t,
		report,
		"claude",
		VerdictError,
		"themesync: auto appearance detection unsupported; choose an explicit theme/appearance",
	)
	if got := h.read(".local/state/pfm/theme/state.json"); got != stateBefore {
		t.Error("a read-back changed the theme state")
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
	defaults := []string{defaultsBinary, "read", "-g", "AppleInterfaceStyle"}
	cases := []struct {
		name            string
		defaultsMissing bool
		result          deps.RunResult
		runErr          error
		want            Appearance
		wantErr         string
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
		{
			name:            "a missing defaults is an error",
			defaultsMissing: true,
			result:          deps.RunResult{Stdout: []byte("Dark\n")},
			wantErr:         `themesync: resolve defaults: exec: "defaults": executable file not found in $PATH`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := new(deps.FakeRunner)
			if !tc.defaultsMissing {
				runner.ScriptLookPath("defaults", defaultsBinary, nil)
			}
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
