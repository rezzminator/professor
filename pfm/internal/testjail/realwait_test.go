//go:build !jail && !e2e && !journey

package testjail

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

// realWaitCurrent is the process clock as production code reads it at the moment of a call.
func realWaitCurrent() clock.Clock {
	current := testwait.SwapProcessClock(nil)
	testwait.SwapProcessClock(current)
	return current
}

// realWaitStart starts f on a goroutine from a function that is not a test, so the tripwire must
// follow the goroutine to the test that called it.
func realWaitStart(f func()) { go f() }

func TestRealWaitTripwireNamesTheTestAndTheSiteOfEachRealWait(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(realWaitEnv, dir)
	wire := armRealWait()
	if wire == nil {
		t.Fatalf("armRealWait with %s=%s = nil, want the tripwire armed", realWaitEnv, dir)
	}
	outer := wire.next
	t.Cleanup(func() { testwait.SwapProcessClock(outer) })
	// The tripwire waits on a fake: no wait below takes real time, and none reaches a tripwire a
	// listing armed around this whole process.
	fake := clock.NewFake(time.Unix(0, 0))
	wire.next = fake
	if current := realWaitCurrent(); current != clock.Clock(wire) {
		t.Fatalf("process clock after arming = %v, want the tripwire", current)
	}
	if value, set := (paths.OSEnv{}).Lookup(realWaitEnv); set {
		t.Errorf("%s after arming = %q, want it cleared for the processes a test starts", realWaitEnv, value)
	}

	_ = realWaitCurrent().Now()
	_ = realWaitCurrent().Sleep(context.Background(), 0)
	_ = realWaitCurrent().Sleep(context.Background(), 0)
	realWaitCurrent().NewTimer(time.Hour).Stop()
	realWaitCurrent().NewTicker(time.Hour).Stop()
	_, unbound := clock.WithTimeout(context.Background(), realWaitCurrent(), time.Hour)
	unbound()
	started := make(chan struct{})
	go func() {
		defer close(started)
		<-realWaitCurrent().After(0)
	}()
	testwait.Receive(t, "the goroutine the test started to wait", started)
	followed := make(chan struct{})
	realWaitStart(func() {
		defer close(followed)
		<-realWaitCurrent().After(0)
	})
	testwait.Receive(t, "the goroutine a helper started to wait", followed)
	t.Run("on a fake", func(t *testing.T) {
		testwait.ProcessClock(t, clock.NewFake(time.Unix(0, 0)))
		_ = realWaitCurrent().Sleep(context.Background(), 0)
		<-realWaitCurrent().After(0)
	})

	const test = "internal/testjail TestRealWaitTripwireNamesTheTestAndTheSiteOfEachRealWait "
	const file = "internal/testjail/realwait_test.go:"
	want := []string{
		test + file + "50", test + file + "51", test + file + "52", test + file + "53", test + file + "54",
		test + file + "59", test + file + "65",
	}
	wire.mu.Lock()
	got := append([]string(nil), wire.ordered...)
	wire.mu.Unlock()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("recorded waits:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	if code := wire.verdict(0); code != 1 {
		t.Errorf("verdict(0) with %d waits = %d, want 1", len(want), code)
	}
	if current := realWaitCurrent(); current != clock.Clock(fake) {
		t.Errorf("process clock after the verdict = %v, want the clock the tripwire replaced", current)
	}
	report := filepath.Join(dir, "internal_testjail.txt")
	data, err := os.ReadFile(report)
	if err != nil {
		t.Fatalf("read the report: %v", err)
	}
	if text := strings.Join(want, "\n") + "\nchecked internal/testjail\n"; string(data) != text {
		t.Errorf("report %s:\n%s\nwant:\n%s", report, data, text)
	}
}

func TestRealWaitVerdictPassesACleanRunAndKeepsAFailedOne(t *testing.T) {
	cases := []struct {
		name     string
		code     int
		wantCode int
	}{
		{"a clean run with no wait", 0, 0},
		{"a failed run with no wait", 3, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report := filepath.Join(t.TempDir(), "internal_loud.txt")
			wire := newRealWaitClock(clock.NewFake(time.Unix(0, 0)), "internal/loud", "", report)
			outer := realWaitCurrent()
			t.Cleanup(func() { testwait.SwapProcessClock(outer) })
			if code := wire.verdict(tc.code); code != tc.wantCode {
				t.Errorf("verdict(%d) = %d, want %d", tc.code, code, tc.wantCode)
			}
			data, err := os.ReadFile(report)
			if err != nil {
				t.Fatalf("read the report: %v", err)
			}
			if string(data) != "checked internal/loud\n" {
				t.Errorf("report %s = %q, want %q", report, data, "checked internal/loud\n")
			}
		})
	}
	for _, unset := range []bool{false, true} {
		state := map[bool]string{false: "empty", true: "unset"}[unset]
		t.Run("a quiet tripwire with "+realWaitEnv+" "+state, func(t *testing.T) {
			t.Setenv(realWaitEnv, "")
			if unset {
				if err := os.Unsetenv(realWaitEnv); err != nil {
					t.Fatal(err)
				}
			}
			quiet := armRealWait()
			if quiet == nil {
				t.Fatalf("armRealWait with %s %s = nil, want the tripwire armed", realWaitEnv, state)
			}
			outer := quiet.next
			t.Cleanup(func() { testwait.SwapProcessClock(outer) })
			if quiet.report != "" {
				t.Errorf("armRealWait with %s %s: report %q, want \"\"", realWaitEnv, state, quiet.report)
			}
			if code := quiet.verdict(2); code != 2 {
				t.Errorf("a quiet armed verdict(2) = %d, want 2", code)
			}
		})
	}
}
