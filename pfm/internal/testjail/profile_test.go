package testjail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime/pprof"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// The profiler is tested from outside, the way a package meets it: a copy of
// this very test binary (or of the profile fixture) runs as a child with the
// profiler's environment set, and the test reads the process directory it left.

const (
	// profileChildEnv selects what TestProfileChild does in a child; unset it
	// returns at once, so an ordinary run of this package never acts on it.
	profileChildEnv = "TESTJAIL_PROFILE_CHILD"
	// profileChildHomeFile is where the child writes its jailed PFM_HOME as its
	// test starts, which marks the test running and lets the parent remove the
	// jail a killed or timed-out child never got to clean up.
	profileChildHomeFile = "TESTJAIL_PROFILE_CHILD_HOME_FILE"
	// profileLabel is this package's process-directory label.
	profileLabel = "internal_testjail"
	// offLine is what a red run prints when profiling is off for want of a
	// directory to write.
	offLine = "testjail: profile off — PFM_TEST_ARTIFACT_DIR unset; no diagnosis bundle for this red run"
)

// TestProfileChild is the body the profiler tests run in a re-exec'd copy of
// this binary.
func TestProfileChild(t *testing.T) {
	mode := os.Getenv(profileChildEnv)
	if mode == "" {
		return
	}
	if file := os.Getenv(profileChildHomeFile); file != "" {
		if err := os.WriteFile(file, []byte(os.Getenv(paths.EnvHome)), 0o600); err != nil {
			t.Fatalf("record the jailed home: %v", err)
		}
	}
	switch mode {
	case "pass":
	case "cpu":
		burn(50 * time.Millisecond)
	case "fail":
		t.Fatal("deliberate failure")
	case "hang":
		select {}
	case "pause", "pause-fail", "pause-refused", "pause-off", "pause-parallel-after", "pause-parallel-before":
		pauseChild(t, mode)
	default:
		t.Fatalf("unknown %s %q", profileChildEnv, mode)
	}
}

func burn(d time.Duration) {
	from := time.Now()
	for n := 0; time.Since(from) < d; n++ {
		_ = n
	}
}

// child is one run of a test binary with the profiler's environment.
type child struct {
	exe       string   // default: this test binary
	mode      string   // TestProfileChild's behaviour; "" for a binary that is not this one
	args      []string // after -test.run=^TestProfileChild$
	env       []string // KEY=VALUE, last wins
	artifacts string   // PFM_TEST_ARTIFACT_DIR; "" leaves it unset
	dir       string   // working directory; "" keeps this package's
	homeFile  string
}

// profilerEnvNames are the variables the profiler reads: a child gets none of
// the caller's (a gate run sets them for this very process) beyond its own.
var profilerEnvNames = []string{
	paths.EnvTestArtifactDir, paths.EnvTestProfile, paths.EnvTestProfileParent, paths.EnvTestDeadlineEpoch,
	profileChildEnv, profileChildHomeFile,
}

func (c *child) command(t *testing.T) (*exec.Cmd, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	exe := c.exe
	if exe == "" {
		var err error
		if exe, err = os.Executable(); err != nil {
			t.Fatalf("locate this test binary: %v", err)
		}
	}
	args := c.args
	if c.mode != "" {
		args = append([]string{"-test.run=^TestProfileChild$"}, args...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = c.dir
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if !slices.Contains(profilerEnvNames, name) {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	if c.mode != "" {
		c.homeFile = filepath.Join(t.TempDir(), "home")
		cmd.Env = append(cmd.Env, profileChildEnv+"="+c.mode, profileChildHomeFile+"="+c.homeFile)
		t.Cleanup(c.removeJail)
	}
	if c.artifacts != "" {
		cmd.Env = append(cmd.Env, paths.EnvTestArtifactDir+"="+c.artifacts)
	}
	cmd.Env = append(cmd.Env, c.env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	return cmd, &stdout, &stderr
}

// removeJail deletes the PFM_HOME the child made: a child killed, or ended by
// the testing package's timeout panic, never ran its own cleanup.
func (c *child) removeJail() {
	data, err := os.ReadFile(c.homeFile)
	if err != nil {
		return
	}
	if home := string(data); strings.HasPrefix(filepath.Base(home), "pfm-jail-home-") {
		_ = os.RemoveAll(home)
	}
}

type childResult struct {
	stdout, stderr string
	code           int
}

// run starts the child and waits for it to end.
func (c *child) run(t *testing.T) childResult {
	t.Helper()
	cmd, stdout, stderr := c.command(t)
	err := cmd.Run()
	res := childResult{stdout: stdout.String(), stderr: stderr.String()}
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		res.code = exit.ExitCode()
	default:
		t.Fatalf("run %s: %v", cmd.Path, err)
	}
	return res
}

// waitFor polls cond every 50 ms until it holds, or fails the test after
// limit, naming what it waited for.
func waitFor(t *testing.T, limit time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("waited %v for %s", limit, what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func readSummary(t *testing.T, dir string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "summary.json"))
	if err != nil {
		t.Fatalf("read summary: %v", err)
	}
	var summary map[string]any
	if err := json.Unmarshal(data, &summary); err != nil {
		t.Fatalf("parse summary %s: %v\n%s", dir, err, data)
	}
	return summary
}

// wantSummary fails unless the summary holds every key=value of want
// (numbers as float64, as encoding/json decodes them).
func wantSummary(t *testing.T, summary, want map[string]any) {
	t.Helper()
	for key, value := range want {
		if got, ok := summary[key]; !ok || !reflect.DeepEqual(got, value) {
			t.Errorf("summary %s = %#v (present %v), want %#v", key, got, ok, value)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func TestProfileCPUStartFailureIsInTheSummary(t *testing.T) {
	t.Parallel()
	p := &profiler{dir: filepath.Join(t.TempDir(), "gone"), pkg: "x", start: time.Now(), bundles: []string{}}
	p.startCPU()
	if p.cpuErr == nil || p.cpu != nil {
		t.Fatalf("startCPU into a missing directory: cpuErr %v, cpu %v", p.cpuErr, p.cpu)
	}
	p.mu.Lock()
	summary := p.summary(0, "exit")
	p.mu.Unlock()
	if got, _ := summary["cpu_profile_error"].(string); !strings.Contains(got, "gone") {
		t.Fatalf("cpu_profile_error = %q, want the failed create named", got)
	}
}

func TestPackageLabel(t *testing.T) {
	mod := modulePrefix()
	cases := []struct{ name, wd, root, importPath, arg0, want string }{
		{
			"a package directory under the module root",
			"/work/pfm/internal/pricing",
			"/work/pfm",
			"",
			"x.test",
			"internal_pricing",
		},
		{"a nested package directory", "/work/pfm/cmd/pfm", "/work/pfm", "", "x.test", "cmd_pfm"},
		{"the module root falls to the import path", "/work/pfm", "/work/pfm", mod + "e2e.test", "x.test", "e2e"},
		{
			"no module falls to the import path",
			"/tmp/x",
			"",
			mod + "internal/pricing.test",
			"x.test",
			"internal_pricing",
		},
		{
			"a foreign import path falls to the binary name",
			"/tmp/x",
			"",
			"example.com/other.test",
			"/tmp/go-build/pricing.test",
			"pricing",
		},
		{"no build info falls to the binary name", "/tmp/x", "", "", "/tmp/go-build/pricing.test", "pricing"},
		{
			"no working directory falls to the import path",
			"",
			"",
			mod + "internal/pfmdb.test",
			"x.test",
			"internal_pfmdb",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := packageLabel(tc.wd, tc.root, tc.importPath, tc.arg0); got != tc.want {
				t.Fatalf(
					"packageLabel(%q, %q, %q, %q) = %q, want %q",
					tc.wd,
					tc.root,
					tc.importPath,
					tc.arg0,
					got,
					tc.want,
				)
			}
		})
	}
}

func TestModuleRootOfFindsTheNearestGoMod(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "internal", "pricing")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := moduleRootOf(nested); got != "" {
		t.Fatalf("moduleRootOf before any go.mod exists = %q, want none", got)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/m\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{root, nested} {
		if got := moduleRootOf(dir); got != root {
			t.Fatalf("moduleRootOf(%s) = %q, want %q", dir, got, root)
		}
	}
}

func TestWatchdogMargin(t *testing.T) {
	cases := []struct{ remaining, want time.Duration }{
		{300 * time.Second, 15 * time.Second},
		{150 * time.Second, 15 * time.Second},
		{100 * time.Second, 10 * time.Second},
		{30 * time.Second, 3 * time.Second},
		{5 * time.Second, time.Second},
		{1500 * time.Millisecond, time.Second},
		{0, time.Second},
	}
	for _, tc := range cases {
		if got := watchdogMargin(tc.remaining); got != tc.want {
			t.Errorf("watchdogMargin(%v) = %v, want %v", tc.remaining, got, tc.want)
		}
	}
}

func TestParseEpoch(t *testing.T) {
	good := map[string]time.Time{
		"1759300000":            time.Unix(1759300000, 0),
		"1759300000.5":          time.Unix(1759300000, 500_000_000),
		"1759300000.123456":     time.Unix(1759300000, 123_456_000),
		"1759300000.1234567891": time.Unix(1759300000, 123_456_789),
	}
	for value, want := range good {
		if got, err := parseEpoch(value); err != nil || !got.Equal(want) {
			t.Errorf("parseEpoch(%q) = %v, %v, want %v", value, got, err, want)
		}
	}
	for _, value := range []string{"", ".5", "5.", "1e9", "-5", "0x10", "1,5", " 5", "1.2.3", "tomorrow", "99999999999999999999"} {
		if got, err := parseEpoch(value); err == nil {
			t.Errorf("parseEpoch(%q) = %v, want an error", value, got)
		}
	}
}

func TestPickDeadlineTakesTheEarlierAndNamesAnUnusableValue(t *testing.T) {
	now := time.Unix(1_759_300_000, 0)
	start := now.Add(-100 * time.Millisecond)
	epoch := func(d time.Duration) string {
		at := now.Add(d)
		return fmt.Sprintf("%d.%09d", at.Unix(), at.Nanosecond())
	}
	cases := []struct {
		name        string
		timeout     time.Duration
		epoch       string
		wantSource  string
		wantAt      time.Time
		wantWarning string // substring; "" for none
	}{
		{"only the test timeout", 10 * time.Minute, "", "timeout", start.Add(10 * time.Minute), ""},
		{"a nearer step deadline", 10 * time.Minute, epoch(4 * time.Second), "deadline", now.Add(4 * time.Second), ""},
		{
			"a step deadline past the test timeout",
			10 * time.Minute,
			epoch(20 * time.Minute),
			"timeout",
			start.Add(10 * time.Minute),
			"",
		},
		{"only a step deadline", 0, epoch(4 * time.Second), "deadline", now.Add(4 * time.Second), ""},
		{
			"just over a second left",
			0,
			epoch(1500 * time.Millisecond),
			"deadline",
			now.Add(1500 * time.Millisecond),
			"",
		},
		{
			"exactly a second left",
			10 * time.Minute,
			epoch(time.Second),
			"timeout",
			start.Add(10 * time.Minute),
			"leaves 1.0s",
		},
		{
			"already past",
			10 * time.Minute,
			epoch(-time.Hour),
			"timeout",
			start.Add(10 * time.Minute),
			"leaves -3600.0s",
		},
		{
			"not decimal seconds, a test timeout stands",
			10 * time.Minute,
			"soon",
			"timeout",
			start.Add(10 * time.Minute),
			`"soon"`,
		},
		{"not decimal seconds, nothing else to arm", 0, "soon", "", time.Time{}, `"soon"`},
		{"neither", 0, "", "", time.Time{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			at, source, warn := pickDeadline(now, start, tc.timeout, tc.epoch)
			if source != tc.wantSource || !at.Equal(tc.wantAt) {
				t.Errorf("pickDeadline = (%v, %q), want (%v, %q)", at, source, tc.wantAt, tc.wantSource)
			}
			if (warn == "") != (tc.wantWarning == "") || !strings.Contains(warn, tc.wantWarning) {
				t.Errorf("warning %q, want one containing %q", warn, tc.wantWarning)
			}
			if tc.wantWarning != "" && !strings.Contains(warn, paths.EnvTestDeadlineEpoch) {
				t.Errorf("warning %q does not name %s", warn, paths.EnvTestDeadlineEpoch)
			}
		})
	}
}

func TestProfileBundleListsAnArtifactItCouldNotWrite(t *testing.T) {
	dir := t.TempDir()
	heap := filepath.Join(dir, "exit", "heap.pprof")
	if err := os.MkdirAll(heap, 0o755); err != nil { // a directory where the heap profile goes
		t.Fatal(err)
	}
	p := &profiler{dir: dir, pkg: "x", start: time.Now(), bundles: []string{}, frErr: errors.New("recorder refused")}
	p.bundle("exit")
	errs := readFile(t, filepath.Join(dir, "exit", "errors.txt"))
	for _, want := range []string{"create " + heap, "trace window: flight recorder not running: recorder refused"} {
		if !strings.Contains(errs, want) {
			t.Errorf("errors.txt lacks %q:\n%s", want, errs)
		}
	}
	for _, name := range []string{"goroutines.txt", "DIAGNOSIS.txt"} {
		if !fileExists(filepath.Join(dir, "exit", name)) {
			t.Errorf("bundle lacks %s though only other artifacts failed", name)
		}
	}
	if !reflect.DeepEqual(p.bundles, []string{"exit"}) {
		t.Errorf("bundles = %v, want [exit]", p.bundles)
	}
}

// A package that ends while its watchdog is writing (the deadline came as the
// last test returned) keeps the watchdog's bundle, and its summary says how it
// exited: finish waits for the firing, so the imminent summary never lands last.
func TestProfileFinishAfterAFiredWatchdogLeavesTheExitSummaryLast(t *testing.T) {
	dir := t.TempDir()
	p := &profiler{dir: dir, pkg: "x", start: time.Now(), bundles: []string{}, frErr: errors.New("no recorder here")}
	// Held, p.mu stops the firing and finish both, so the two meet as they do
	// at a real deadline: the watchdog already running when finish starts.
	p.armWatchdog(time.Hour, "deadline")
	p.mu.Lock()
	p.watch.Reset(0)
	running := func(frame string) func() bool {
		return func() bool {
			var dump bytes.Buffer
			_ = pprof.Lookup("goroutine").WriteTo(&dump, 2)
			return strings.Contains(dump.String(), frame)
		}
	}
	waitFor(t, 10*time.Second, "the watchdog to fire", running("(*profiler).armWatchdog.func1"))
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		p.finish(0)
	}()
	waitFor(t, 10*time.Second, "finish to start", running("(*profiler).finish("))
	p.mu.Unlock()
	select {
	case <-finished:
	case <-time.After(30 * time.Second):
		t.Fatal("finish did not return within 30s of the watchdog's firing")
	}
	wantSummary(t, readSummary(t, dir), map[string]any{
		"event": "exit", "exit_code": float64(0), "bundles": []any{"deadline"},
	})
	if !fileExists(filepath.Join(dir, "deadline", "DIAGNOSIS.txt")) {
		t.Error("finish returned before the fired watchdog wrote deadline/DIAGNOSIS.txt")
	}
}

func TestProfileSummaryNeverRendersAnUnreadablePSIAsZero(t *testing.T) {
	p := &profiler{pkg: "x", start: time.Now(), bundles: []string{}, psiErr: errors.New("psi: no pressure files")}
	p.mu.Lock()
	data, err := json.Marshal(p.summary(0, "exit"))
	p.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	var summary map[string]any
	if err := json.Unmarshal(data, &summary); err != nil {
		t.Fatal(err)
	}
	if got, ok := summary["psi"]; !ok || got != nil {
		t.Fatalf("psi = %#v (present %v), want null", got, ok)
	}
	if got := summary["psi_error"]; got != "psi: no pressure files" {
		t.Fatalf("psi_error = %#v, want the reason", got)
	}
}
