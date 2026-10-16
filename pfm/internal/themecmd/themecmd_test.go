package themecmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/themesync"
)

// call records what the verb asked of themesync.
type call struct {
	verb    string
	palette string
	mode    themesync.Mode
	force   bool
}

type fakeThemesync struct {
	on       bool
	report   themesync.Report
	err      error
	palettes []themesync.Palette
	calls    []call
}

func (fake *fakeThemesync) deps(color bool) Deps {
	record := func(verb string) (themesync.Report, error) {
		fake.calls = append(fake.calls, call{verb: verb})
		return fake.report, fake.err
	}
	return Deps{
		Env:   func() (themesync.Env, error) { return themesync.Env{StateDir: "/state"}, nil },
		Color: color,
		Ops: Ops{
			Set: func(_ context.Context, _ themesync.Env, request themesync.Request) (themesync.Report, error) {
				fake.calls = append(fake.calls, call{verb: "set", palette: request.Palette, mode: request.Mode})
				return fake.report, fake.err
			},
			Sync: func(_ context.Context, _ themesync.Env, force bool) (themesync.Report, error) {
				fake.calls = append(fake.calls, call{verb: "sync", force: force})
				return fake.report, fake.err
			},
			ReadBack: func(context.Context, themesync.Env) (themesync.Report, error) { return record("status") },
			Off:      func(context.Context, themesync.Env) (themesync.Report, error) { return record("off") },
			Palettes: func() []themesync.Palette { return fake.palettes },
			PaletteByName: func(name string) (themesync.Palette, error) {
				for i := range fake.palettes {
					palette := fake.palettes[i]
					if palette.Name == name {
						return palette, nil
					}
				}
				return themesync.Palette{}, errors.New("unknown palette")
			},
			SyncOn: func(string) bool { return fake.on },
		},
	}
}

func variant(base string) themesync.Variant {
	value := themesync.Variant{
		Background: "#" + base + "0000", Foreground: "#00" + base + "00", Accent: "#0000" + base,
		State: themesync.StateColors{Blocked: "#ff0000", Working: "#ffaa00", Done: "#00ff00", Idle: "#888888"},
	}
	for index := range value.ANSI {
		value.ANSI[index] = "#" + base + base + base
	}
	return value
}

func twoPalettes() []themesync.Palette {
	return []themesync.Palette{
		{Name: "tokyo", Title: "Tokyo Night", Dark: variant("11"), Light: variant("ee")},
		{Name: "gold", Title: "Professor Gold", Dark: variant("22"), Light: variant("dd")},
	}
}

func matrix(verdicts ...themesync.Verdict) themesync.Report {
	report := themesync.Report{
		Enabled: true, Palette: "tokyo", Mode: themesync.ModeAuto, Appearance: themesync.Dark, Generation: 7,
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
			fake:     fakeThemesync{on: true, report: matrix(themesync.VerdictInSync, themesync.VerdictInSync)},
			wantCode: 0, wantCalls: []call{{verb: "status"}},
			wantStdout: []string{
				"pfm theme · tokyo · auto → dark · gen 7",
				"claude", "IN-SYNC", "/p/IN-SYNC", "d-IN-SYNC",
				"in sync · 2 of 2 surfaces at gen 7",
			},
		},
		{
			name: "status with the sync off prints the off line and reads nothing", args: []string{"status"},
			fake:     fakeThemesync{on: false},
			wantCode: 0, wantStdout: []string{"pfm theme: off — pfm theme set <palette> turns it on"},
		},
		{
			name: "status exits 1 on drift and names the surface", args: []string{"status"},
			fake:     fakeThemesync{on: true, report: matrix(themesync.VerdictInSync, themesync.VerdictDrift)},
			wantCode: 1, wantCalls: []call{{verb: "status"}},
			wantStdout: []string{"DRIFT", "OUT OF SYNC · tmux DRIFT"},
		},
		{
			name:       "status exits 1 on error",
			args:       []string{"status"},
			fake:       fakeThemesync{on: true, report: matrix(themesync.VerdictError)},
			wantCode:   1,
			wantCalls:  []call{{verb: "status"}},
			wantStdout: []string{"ERROR", "OUT OF SYNC · claude ERROR"},
		},
		{
			name: "pending and absent keep exit 0 and are named", args: []string{"status"},
			fake: fakeThemesync{on: true, report: matrix(
				themesync.VerdictInSync, themesync.VerdictPending, themesync.VerdictAbsent)},
			wantCode: 0, wantCalls: []call{{verb: "status"}},
			wantStdout: []string{"PENDING", "ABSENT", "in sync · 1 of 3 surfaces at gen 7 · 1 pending · 1 absent"},
		},
		{
			name: "status read failure is an error not an off line", args: []string{"status"},
			fake:     fakeThemesync{on: true, err: errors.New("state unreadable")},
			wantCode: 1, wantCalls: []call{{verb: "status"}}, wantStderr: []string{"pfm theme: state unreadable"},
		},
		{
			name: "set defaults to auto", args: []string{"set", "tokyo"},
			fake:     fakeThemesync{palettes: twoPalettes(), report: matrix(themesync.VerdictApplied)},
			wantCode: 0, wantCalls: []call{{verb: "set", palette: "tokyo", mode: themesync.ModeAuto}},
			wantStdout: []string{"APPLIED"},
		},
		{
			name:       "set takes the appearance flag after the palette",
			args:       []string{"set", "gold", "--appearance", "light"},
			fake:       fakeThemesync{palettes: twoPalettes(), report: matrix(themesync.VerdictApplied)},
			wantCode:   0,
			wantCalls:  []call{{verb: "set", palette: "gold", mode: themesync.ModeLight}},
			wantStdout: []string{"APPLIED"},
		},
		{
			name:       "set takes the appearance flag before the palette",
			args:       []string{"set", "--appearance=dark", "gold"},
			fake:       fakeThemesync{palettes: twoPalettes(), report: matrix(themesync.VerdictApplied)},
			wantCode:   0,
			wantCalls:  []call{{verb: "set", palette: "gold", mode: themesync.ModeDark}},
			wantStdout: []string{"APPLIED"},
		},
		{
			name: "unknown palette names the known ones and exits 2", args: []string{"set", "nope"},
			fake:     fakeThemesync{palettes: twoPalettes()},
			wantCode: 2, wantStderr: []string{`unknown palette "nope"`, "known: tokyo, gold"},
		},
		{
			name: "set without a palette is usage",
			args: []string{"set"},
			fake: fakeThemesync{
				palettes: twoPalettes(),
			},
			wantCode:   2,
			wantStderr: []string{"usage: pfm theme set <palette>"},
		},
		{
			name: "bad appearance exits 2",
			args: []string{"set", "tokyo", "--appearance", "dusk"},
			fake: fakeThemesync{
				palettes: twoPalettes(),
			},
			wantCode:   2,
			wantStderr: []string{`appearance "dusk"`, "auto, dark or light"},
		},
		{
			name: "set failure exits 1 with the cause", args: []string{"set", "tokyo"},
			fake:     fakeThemesync{palettes: twoPalettes(), err: errors.New("herdr config is read-only")},
			wantCode: 1, wantCalls: []call{{verb: "set", palette: "tokyo", mode: themesync.ModeAuto}},
			wantStderr: []string{"pfm theme: herdr config is read-only"},
		},
		{
			name: "sync", args: []string{"sync"},
			fake:     fakeThemesync{on: true, report: matrix(themesync.VerdictInSync)},
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
