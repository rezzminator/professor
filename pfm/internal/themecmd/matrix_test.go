package themecmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/themesync"
)

func TestStatusColoursVerdictsOnlyOnAColourTerminal(t *testing.T) {
	report := matrix(themesync.VerdictInSync, themesync.VerdictPending, themesync.VerdictAbsent, themesync.VerdictDrift)
	want := map[string]string{
		"IN-SYNC": "\x1b[32mIN-SYNC", "PENDING": "\x1b[33mPENDING",
		"ABSENT": "\x1b[2mABSENT", "DRIFT": "\x1b[31mDRIFT",
	}
	for _, color := range []bool{true, false} {
		fake := fakeThemesync{on: true, report: report}
		var stdout, stderr bytes.Buffer
		RunVerb(context.Background(), []string{"status"}, fake.deps(color), &stdout, &stderr)
		for verdict, sequence := range want {
			if got := strings.Contains(stdout.String(), sequence); got != color {
				t.Errorf("color=%v: %q present = %v in\n%q", color, sequence, got, stdout.String())
			}
			if !strings.Contains(stdout.String(), verdict) {
				t.Errorf("color=%v: verdict %s not named in %q", color, verdict, stdout.String())
			}
		}
		if !color && strings.Contains(stdout.String(), "\x1b[") {
			t.Errorf("an escape reached a plain stream: %q", stdout.String())
		}
	}
}

func TestJSONPrintsTheReport(t *testing.T) {
	for _, args := range [][]string{{"status", "--json"}, {"on", "--json"}, {"sync", "--json"}, {"off", "--json"}} {
		fake := fakeThemesync{
			on:     true,
			report: matrix(themesync.VerdictInSync, themesync.VerdictDrift),
		}
		var stdout, stderr bytes.Buffer
		code := RunVerb(context.Background(), args, fake.deps(true), &stdout, &stderr)
		var got themesync.Report
		if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
			t.Fatalf("%v: stdout is not the report JSON: %v\n%s", args, err, stdout.String())
		}
		if got.Generation != 7 || len(got.Surfaces) != 2 || got.Surfaces[1].Verdict != themesync.VerdictDrift {
			t.Errorf("%v: report %+v", args, got)
		}
		if code != 1 {
			t.Errorf("%v: exit %d, want 1 on drift", args, code)
		}
	}
}

func TestStatusJSONWithTheSyncOff(t *testing.T) {
	fake := fakeThemesync{on: false}
	var stdout, stderr bytes.Buffer
	code := RunVerb(context.Background(), []string{"status", "--json"}, fake.deps(false), &stdout, &stderr)
	var got themesync.Report
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil || got.Enabled || code != 0 {
		t.Fatalf("exit %d, report %+v, err %v, stdout %s", code, got, err, stdout.String())
	}
}

func TestStatusEndsOnTheMeasuredLastFlip(t *testing.T) {
	ack := 1.5
	report := matrix(themesync.VerdictSource, themesync.VerdictInSync)
	report.LastFlip = &themesync.Flip{
		At:     "2026-10-16T03:00:00.250Z",
		Source: "/h/.claude/settings.json",
		Tmux:   0.25,
		Herdr:  0.5,
		VSCode: 0.75,
		Ack:    &ack,
	}
	fake := fakeThemesync{on: true, report: report}
	var stdout, stderr bytes.Buffer
	RunVerb(context.Background(), []string{"status"}, fake.deps(false), &stdout, &stderr)
	want := "last flip 2026-10-16T03:00:00Z /h/.claude/settings.json: tmux +0.25 s · herdr +0.50 s · " +
		"vscode contract +0.75 s, ack +1.50 s\n"
	if !strings.HasSuffix(stdout.String(), want) {
		t.Fatalf("status does not end on %q:\n%s", want, stdout.String())
	}
	report.LastFlip.Ack = nil
	fake = fakeThemesync{on: true, report: report}
	stdout.Reset()
	RunVerb(context.Background(), []string{"status"}, fake.deps(false), &stdout, &stderr)
	if strings.Contains(stdout.String(), "ack") {
		t.Errorf("a flip without an ack names one:\n%s", stdout.String())
	}
}

// The follower's launchd job runs `sync` quietly: nothing when nothing changed,
// one line per apply, one line per failure and the same failure printed once.
func TestQuietFollowerSync(t *testing.T) {
	changed := matrix(themesync.VerdictSource, themesync.VerdictInSync)
	changed.Changed, changed.Servers = true, 2
	drifted := matrix(themesync.VerdictSource, themesync.VerdictDrift)
	cases := []struct {
		name       string
		fake       fakeThemesync
		wantCode   int
		wantStdout string
		wantStderr string
		wantLogged []string
	}{
		{name: "sync off prints nothing and records nothing", fake: fakeThemesync{on: false}},
		{
			name: "nothing changed prints nothing and clears the log line",
			fake: fakeThemesync{
				on:     true,
				report: matrix(themesync.VerdictSource, themesync.VerdictInSync),
			},
			wantLogged: []string{""},
		},
		{
			name:       "an apply prints one line",
			fake:       fakeThemesync{on: true, report: changed},
			wantStdout: "theme sync gen 7: custom:hibiscus-night (dark) from /h/.claude/settings.json · tmux 2 · herdr · vscode contract\n",
			wantLogged: []string{""},
		},
		{
			name: "a drifted surface fails once", fake: fakeThemesync{on: true, report: drifted}, wantCode: 1,
			wantStderr: "theme sync gen 7 failed: tmux DRIFT d-DRIFT\n",
			wantLogged: []string{"theme sync gen 7 failed: tmux DRIFT d-DRIFT"},
		},
		{
			name:       "a run error fails once",
			fake:       fakeThemesync{on: true, err: errors.New("no Claude account\nsettings.json")},
			wantCode:   1,
			wantStderr: "theme sync: no Claude account settings.json\n",
			wantLogged: []string{"theme sync: no Claude account settings.json"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := tc.fake
			fake.job = themesync.FollowerLabel
			var stdout, stderr bytes.Buffer
			code := RunVerb(context.Background(), []string{"sync"}, fake.deps(false), &stdout, &stderr)
			if code != tc.wantCode || stdout.String() != tc.wantStdout || stderr.String() != tc.wantStderr ||
				strings.Join(fake.logged, "|") != strings.Join(tc.wantLogged, "|") {
				t.Fatalf("exit %d stdout %q stderr %q logged %q", code, stdout.String(), stderr.String(), fake.logged)
			}
		})
	}
	t.Run("the same failure twice prints once", func(t *testing.T) {
		fake := fakeThemesync{on: true, report: drifted, job: themesync.FollowerLabel}
		var firstOut, firstErr, secondOut, secondErr bytes.Buffer
		deps := fake.deps(false)
		RunVerb(context.Background(), []string{"sync"}, deps, &firstOut, &firstErr)
		code := RunVerb(context.Background(), []string{"sync"}, deps, &secondOut, &secondErr)
		if firstErr.Len() == 0 || secondErr.Len() != 0 || secondOut.Len() != 0 || code != 1 {
			t.Fatalf(
				"first stderr %q; second exit %d stdout %q stderr %q",
				firstErr.String(),
				code,
				secondOut.String(),
				secondErr.String(),
			)
		}
	})
	t.Run("a shell run is not quiet", func(t *testing.T) {
		fake := fakeThemesync{on: true, report: matrix(themesync.VerdictSource, themesync.VerdictInSync)}
		var stdout, stderr bytes.Buffer
		RunVerb(context.Background(), []string{"sync"}, fake.deps(false), &stdout, &stderr)
		if !strings.Contains(stdout.String(), "IN-SYNC") || len(fake.logged) != 0 {
			t.Fatalf("stdout %q logged %v", stdout.String(), fake.logged)
		}
	})
}
