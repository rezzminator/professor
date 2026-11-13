package costsink

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestArmRefusesWhatItCannotServe(t *testing.T) {
	home := t.TempDir()
	binary := "/x/pfm"
	for _, tc := range []struct {
		name string
		opts ArmOptions
		want string
	}{
		{"no pid", ArmOptions{Self: binary, Home: home}, "cost sink: arm needs a pid and the pfm binary, got pid 0 self \"/x/pfm\""},
		{"no binary", ArmOptions{PID: os.Getpid(), Home: home}, "cost sink: arm needs a pid and the pfm binary, got pid {pid} self \"\""},
		{"a pid that is not running", ArmOptions{Self: binary, PID: 1 << 30, Home: home}, "cost sink: read the start time of pid 1073741824: open /proc/1073741824/stat: no such file or directory"},
		{"a binary that cannot start", ArmOptions{Self: filepath.Join(home, "missing"), PID: os.Getpid(), Home: home}, "cost sink: start {home}/missing internal cost-sink: file does not exist"},
	} {
		// A binary that is not there is refused by the runner's Start; every other row is refused before it.
		runner := &deps.FakeRunner{}
		runner.ScriptStart([]string{filepath.Join(home, "missing")}, 0, nil, os.ErrNotExist)
		tc.opts.Runner = runner
		_, err := Arm(tc.opts)
		if got := strings.NewReplacer(home, "{home}", strconv.Itoa(os.Getpid()), "{pid}").
			Replace(fmt.Sprint(err)); got != tc.want {
			t.Errorf("%s: Arm error = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// Arm starts the sink through its runner: detached, the listening socket as
// its one extra file, its stderr on the sink log and the environment given.
func TestArmStartsTheSinkDetachedThroughTheRunner(t *testing.T) {
	home := t.TempDir()
	self := os.Getpid()
	stat, err := gather.NewProcFS("").Stat(self)
	if err != nil {
		t.Fatalf("read our own start time: %v", err)
	}
	runner := &deps.FakeRunner{}
	environ := []string{"PATH=/usr/bin:/bin", "FAKE_MARKER=kept"}
	if _, err := Arm(ArmOptions{Self: "/x/pfm", PID: self, Home: home, Environ: environ, Runner: runner}); err != nil {
		t.Fatalf("Arm: %v", err)
	}
	starts := runner.Starts()
	if len(starts) != 1 {
		t.Fatalf("starts = %+v, want exactly one", starts)
	}
	want := []string{
		"/x/pfm", "internal", "cost-sink",
		"--pid", strconv.Itoa(self), "--start", strconv.FormatUint(stat.StartTime, 10),
	}
	if !slices.Equal(starts[0].Argv, want) {
		t.Fatalf("sink argv = %q, want %q", starts[0].Argv, want)
	}
	opts := starts[0].Opts
	stderr, ok := opts.Stderr.(*os.File)
	sinkLog := paths.LogRoot(paths.OSEnv{}, home).CostSink()
	if !opts.Detach || !ok || stderr.Name() != sinkLog || len(opts.ExtraFiles) != 1 ||
		opts.Stdout != nil || !slices.Equal(opts.Env, environ) {
		t.Fatalf("start options = %+v, want Detach, stderr on %s, one extra file and env %q", opts, sinkLog, environ)
	}
	if got := runner.LifecycleCalls(); !slices.Equal(got, []deps.LifecycleCall{{Pid: 4242, Action: "release"}}) {
		t.Fatalf("lifecycle = %+v, want the sink released", got)
	}
}
