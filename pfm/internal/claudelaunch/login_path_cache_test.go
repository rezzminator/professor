package claudelaunch

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

// probeWaiters counts the goroutines blocked in get on another caller's probe.
func probeWaiters() (int, string) {
	waiting := 0
	// A traceback elides runtime frames, so a waiter's innermost frame is get, blocked
	// receiving; the probing caller's is its probe.
	for _, goroutine := range testwait.Goroutines(true) {
		if goroutine.Status == "chan receive" && len(goroutine.Frames) > 0 &&
			strings.HasSuffix(goroutine.Frames[0].Function, "(*loginPATHMemo).get") {
			waiting++
		}
	}
	return waiting, fmt.Sprintf("%d callers waiting", waiting)
}

// Callers asking for one shell while its probe runs share that probe; a caller
// asking for another shell is not held behind it; a later caller reads the answer.
func TestLoginPATHMemoProbesEachShellOnce(t *testing.T) {
	const zsh, bash, callers = "/bin/zsh", "/bin/bash", 4
	memo := &loginPATHMemo{byShell: map[string]*loginPATHCall{}}
	fake := clock.NewFake(time.Unix(0, 0))
	want := loginPATHResult{entries: []string{"/opt/mise/shims"}}
	started, release := make(chan struct{}, callers), make(chan struct{})
	var probes atomic.Int32
	probe := func() loginPATHResult {
		probes.Add(1)
		started <- struct{}{}
		<-release
		return want
	}
	results := make(chan loginPATHResult, callers)
	go func() { results <- memo.get(zsh, fake, probe) }()
	testwait.Receive(t, "the first caller's probe starts", started)
	for range callers - 1 {
		go func() { results <- memo.get(zsh, fake, probe) }()
	}
	testwait.Until(t, "every later caller waits on the running probe", func() (bool, string) {
		waiting, state := probeWaiters()
		return waiting == callers-1, state
	})
	other := make(chan loginPATHResult, 1)
	go func() {
		other <- memo.get(bash, fake, func() loginPATHResult { return loginPATHResult{problem: "bash probed"} })
	}()
	answer := bash + " answers while the " + zsh + " probe runs"
	if got, _ := testwait.Receive(t, answer, other); got.problem != "bash probed" {
		t.Fatalf("%s answer = %+v, want its own probe's", bash, got)
	}
	close(release)
	for range callers {
		if got, _ := testwait.Receive(t, zsh+" answers its callers", results); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s answer = %+v, want %+v", zsh, got, want)
		}
	}
	if got := probes.Load(); got != 1 {
		t.Fatalf("%s probes = %d, want 1 shared by %d callers", zsh, got, callers)
	}
	later := memo.get(zsh, fake, func() loginPATHResult {
		t.Errorf("%s probed again after its answer was cached", zsh)
		return loginPATHResult{}
	})
	if !reflect.DeepEqual(later, want) {
		t.Fatalf("%s cached answer = %+v, want %+v", zsh, later, want)
	}
}

// A failed probe's answer stands for loginPATHRetry, then the next caller probes
// again; a successful probe's answer stands for the life of the process.
func TestLoginPATHMemoRetriesOnlyAFailedProbe(t *testing.T) {
	const shell = "/bin/zsh"
	failed := loginPATHResult{problem: "login shell /bin/zsh: no PATH within 3s"}
	found := loginPATHResult{entries: []string{"/opt/mise/shims"}}
	again := loginPATHResult{entries: []string{"/probed/again"}}
	for _, test := range []struct {
		name       string
		first      loginPATHResult
		after      time.Duration
		want       loginPATHResult
		wantProbes int
	}{
		{"failure inside the retry interval", failed, loginPATHRetry - time.Nanosecond, failed, 0},
		{"failure at the retry interval", failed, loginPATHRetry, again, 1},
		{"failure past the retry interval", failed, loginPATHRetry + time.Second, again, 1},
		{"success a day later", found, 24 * time.Hour, found, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			memo := &loginPATHMemo{byShell: map[string]*loginPATHCall{}}
			fake := clock.NewFake(time.Unix(0, 0))
			first := memo.get(shell, fake, func() loginPATHResult { return test.first })
			if !reflect.DeepEqual(first, test.first) {
				t.Fatalf("first answer = %+v, want %+v", first, test.first)
			}
			fake.Advance(test.after)
			probes := 0
			got := memo.get(shell, fake, func() loginPATHResult {
				probes++
				return again
			})
			if !reflect.DeepEqual(got, test.want) || probes != test.wantProbes {
				t.Fatalf("answer %s later = %+v after %d probes, want %+v after %d",
					test.after, got, probes, test.want, test.wantProbes)
			}
		})
	}
}

// A caller passing its runner neither reads nor writes the process's cache.
func TestLoginPATHWithARunnerBypassesTheCache(t *testing.T) {
	const shell = "/login/bin/zsh"
	done := make(chan struct{})
	close(done)
	planted := &loginPATHCall{done: done, result: loginPATHResult{entries: []string{"/planted"}}}
	loginPATHCache.mu.Lock()
	previous, had := loginPATHCache.byShell[shell]
	loginPATHCache.byShell[shell] = planted
	loginPATHCache.mu.Unlock()
	t.Cleanup(func() {
		loginPATHCache.mu.Lock()
		defer loginPATHCache.mu.Unlock()
		delete(loginPATHCache.byShell, shell)
		if had {
			loginPATHCache.byShell[shell] = previous
		}
	})
	runner := loginRunner(shell, "/opt/mise/shims")
	for launch := range 2 {
		got := loginPATH(context.Background(), clock.NewFake(time.Unix(0, 0)), shell, runner)
		if !slices.Equal(got.entries, []string{"/opt/mise/shims"}) || got.problem != "" {
			t.Fatalf("launch %d: loginPATH = %+v, want the runner's /opt/mise/shims, not the cached /planted",
				launch, got)
		}
	}
	runs := 0
	for _, call := range runner.Calls() {
		if call.Argv[0] == shell {
			runs++
		}
	}
	if runs != 2 {
		t.Fatalf("login shell runs over two launches = %d, want 2: a runner's answer is never cached", runs)
	}
	loginPATHCache.mu.Lock()
	kept := loginPATHCache.byShell[shell]
	loginPATHCache.mu.Unlock()
	if kept != planted || !slices.Equal(kept.result.entries, []string{"/planted"}) {
		t.Fatalf("cache entry for %s = %+v, want the planted /planted untouched", shell, kept)
	}
}
