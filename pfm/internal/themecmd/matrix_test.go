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
	for _, args := range [][]string{{"status", "--json"}, {"set", "tokyo", "--json"}, {"sync", "--json"}, {"off", "--json"}} {
		fake := fakeThemesync{
			on:       true,
			palettes: twoPalettes(),
			report:   matrix(themesync.VerdictInSync, themesync.VerdictDrift),
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

func TestLsShowsEveryPaletteWithItsSwatches(t *testing.T) {
	fake := fakeThemesync{on: true, palettes: twoPalettes(), report: matrix()}
	var stdout, stderr bytes.Buffer
	if code := RunVerb(context.Background(), []string{"ls"}, fake.deps(true), &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"* tokyo", "Tokyo Night", "  gold", "Professor Gold", "dark", "light",
		"blocked", "working", "done", "idle",
		"\x1b[38;2;0;17;0;48;2;17;0;0m", // tokyo dark foreground #001100 on background #110000
		"\x1b[48;2;255;0;0m",            // the blocked state colour
	} {
		if !strings.Contains(out, want) {
			t.Errorf("ls lacks %q:\n%s", want, out)
		}
	}
}

func TestLsPlainStreamCarriesHexInsteadOfEscapes(t *testing.T) {
	fake := fakeThemesync{palettes: twoPalettes()}
	var stdout, stderr bytes.Buffer
	if code := RunVerb(context.Background(), []string{"ls"}, fake.deps(false), &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	out := stdout.String()
	if strings.Contains(out, "\x1b[") || !strings.Contains(out, "#110000") ||
		!strings.Contains(out, "blocked #ff0000") {
		t.Fatalf("plain ls:\n%q", out)
	}
	if strings.Contains(out, "* ") {
		t.Errorf("a current marker without a sync on: %q", out)
	}
}

func TestLsWithNoPalettesIsAnError(t *testing.T) {
	fake := fakeThemesync{}
	var stdout, stderr bytes.Buffer
	code := RunVerb(context.Background(), []string{"ls"}, fake.deps(false), &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "no palettes") {
		t.Fatalf("exit %d, stderr %q", code, stderr.String())
	}
}

func TestLsNamesAFailedCurrentLookup(t *testing.T) {
	fake := fakeThemesync{on: true, palettes: twoPalettes(), err: errors.New("state unreadable")}
	var stdout, stderr bytes.Buffer
	code := RunVerb(context.Background(), []string{"ls"}, fake.deps(false), &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "tokyo") ||
		!strings.Contains(stderr.String(), "current palette unknown: state unreadable") {
		t.Fatalf("exit %d\nstdout %s\nstderr %s", code, stdout.String(), stderr.String())
	}
}
