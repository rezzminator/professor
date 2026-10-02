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
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
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

// processDir is the one directory <label>.<pid> under artifacts.
func processDir(t *testing.T, artifacts, label string) string {
	t.Helper()
	found, err := filepath.Glob(filepath.Join(artifacts, label+".*"))
	if err != nil || len(found) != 1 {
		entries, _ := os.ReadDir(artifacts)
		t.Fatalf("want one %s.<pid> under %s, found %v (%v); directory holds %v", label, artifacts, found, err, entries)
	}
	return found[0]
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

// entryNames lists dir's entries by name.
func entryNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("list %s: %v", dir, err)
	}
	names := []string{}
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func TestProfileGreenExitLeavesASummaryAndNoBundle(t *testing.T) {
	t.Parallel()
	artifacts := t.TempDir()
	res := (&child{mode: "pass", artifacts: artifacts}).run(t)
	if res.code != 0 {
		t.Fatalf("child exit %d: %s%s", res.code, res.stdout, res.stderr)
	}
	dir := processDir(t, artifacts, profileLabel)
	summary := readSummary(t, dir)
	wantSummary(
		t,
		summary,
		map[string]any{"event": "exit", "exit_code": float64(0), "bundles": []any{}, "package": profileLabel},
	)
	if got := entryNames(t, dir); !reflect.DeepEqual(got, []string{"summary.json"}) {
		t.Fatalf("process directory holds %v, want the summary alone", got)
	}
	if strings.Contains(res.stderr, "profile") {
		t.Fatalf("a green profiled run wrote a profiler line: %q", res.stderr)
	}
}

func TestProfileTimeoutBundleListsTheBlockedTestBeforeTestingPanics(t *testing.T) {
	t.Parallel()
	artifacts := t.TempDir()
	res := (&child{mode: "hang", artifacts: artifacts, args: []string{"-test.timeout=2s"}}).run(t)
	if res.code != 2 || !strings.Contains(res.stdout+res.stderr, "panic: test timed out after 2s") {
		t.Fatalf("child exit %d, want testing's timeout panic (exit 2): %s%s", res.code, res.stdout, res.stderr)
	}
	dir := processDir(t, artifacts, profileLabel)
	summary := readSummary(t, dir)
	wantSummary(
		t,
		summary,
		map[string]any{"event": "timeout-imminent", "exit_code": float64(-1), "bundles": []any{"timeout"}},
	)
	if wall, _ := summary["wall_s"].(float64); wall <= 0 || wall >= 2 {
		t.Errorf("wall_s = %v at the capture, want under the 2 s timeout", summary["wall_s"])
	}
	if at, _ := summary["watchdog_s"].(float64); at < 0.5 || at >= 2 {
		t.Errorf("watchdog_s = %v, want about 1 (2 s less the 1 s minimum margin)", summary["watchdog_s"])
	}
	diagnosis := readFile(t, filepath.Join(dir, "timeout", "DIAGNOSIS.txt"))
	if !strings.Contains(
		diagnosis,
		"testjail.TestProfileChild [select (no cases)] at internal/testjail/profile_test.go:",
	) {
		t.Fatalf("diagnosis does not list the blocked test:\n%s", diagnosis)
	}
	for _, artifact := range []string{"goroutines.txt", "heap.pprof", "trace.out"} {
		if info, err := os.Stat(filepath.Join(dir, "timeout", artifact)); err != nil || info.Size() == 0 {
			t.Errorf("timeout bundle artifact %s: %v", artifact, err)
		}
	}
}

func TestProfileStepDeadlineFiresBeforeTheTestTimeout(t *testing.T) {
	t.Parallel()
	artifacts := t.TempDir()
	deadline := time.Now().Add(3 * time.Second)
	cmd, stdout, stderr := (&child{
		mode: "hang", artifacts: artifacts, args: []string{"-test.timeout=10m"},
		env: []string{fmt.Sprintf("%s=%d.%06d", paths.EnvTestDeadlineEpoch, deadline.Unix(), deadline.Nanosecond()/1000)},
	}).command(t)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	waitFor(t, 30*time.Second, "the deadline watchdog's summary", func() bool {
		found, _ := filepath.Glob(filepath.Join(artifacts, profileLabel+".*", "summary.json"))
		if len(found) != 1 {
			return false
		}
		data, err := os.ReadFile(found[0])
		return err == nil && strings.Contains(string(data), `"event": "deadline-imminent"`)
	})
	dir := processDir(t, artifacts, profileLabel)
	summary := readSummary(t, dir)
	wantSummary(
		t,
		summary,
		map[string]any{"event": "deadline-imminent", "exit_code": float64(-1), "bundles": []any{"deadline"}},
	)
	if at, _ := summary["watchdog_s"].(float64); at <= 0 || at >= 3 {
		t.Errorf("watchdog_s = %v, want the seconds before the 3 s deadline", summary["watchdog_s"])
	}
	diagnosis := readFile(t, filepath.Join(dir, "deadline", "DIAGNOSIS.txt"))
	if !strings.Contains(diagnosis, "testjail.TestProfileChild [select (no cases)]") {
		t.Fatalf("diagnosis does not list the blocked test:\n%s%s%s", diagnosis, stdout, stderr)
	}
}

func TestProfileUnusableStepDeadlineArmsNothingAndNamesTheValue(t *testing.T) {
	t.Parallel()
	past := fmt.Sprintf("%d.5", time.Now().Add(-time.Minute).Unix())
	for name, value := range map[string]string{
		"not decimal seconds": "tomorrow",
		"a comma fraction":    "1759300000,5",
		"already past":        past,
		"nothing left":        strconv.FormatInt(time.Now().Unix(), 10),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			artifacts := t.TempDir()
			res := (&child{
				mode: "pass", artifacts: artifacts, args: []string{"-test.timeout=0"},
				env: []string{paths.EnvTestDeadlineEpoch + "=" + value},
			}).run(t)
			if res.code != 0 {
				t.Fatalf("child exit %d: %s%s", res.code, res.stdout, res.stderr)
			}
			lines := strings.Split(strings.TrimSpace(res.stderr), "\n")
			if len(lines) != 1 || !strings.HasPrefix(lines[0], "testjail: ") ||
				!strings.Contains(
					lines[0],
					strconv.Quote(value),
				) || !strings.Contains(lines[0], paths.EnvTestDeadlineEpoch) {
				t.Fatalf(
					"stderr %q, want one testjail: line naming %s=%q",
					res.stderr,
					paths.EnvTestDeadlineEpoch,
					value,
				)
			}
			wantSummary(
				t,
				readSummary(t, processDir(t, artifacts, profileLabel)),
				map[string]any{"watchdog_s": nil, "event": "exit"},
			)
		})
	}
}

func TestProfileUnusableStepDeadlineLeavesTheTestTimeoutWatchdog(t *testing.T) {
	t.Parallel()
	artifacts := t.TempDir()
	res := (&child{
		mode: "pass", artifacts: artifacts, args: []string{"-test.timeout=100s"},
		env: []string{paths.EnvTestDeadlineEpoch + "=tomorrow"},
	}).run(t)
	if res.code != 0 || !strings.Contains(res.stderr, `"tomorrow"`) {
		t.Fatalf("child exit %d, stderr %q", res.code, res.stderr)
	}
	summary := readSummary(t, processDir(t, artifacts, profileLabel))
	if at, _ := summary["watchdog_s"].(float64); at < 85 || at > 91 {
		t.Fatalf("watchdog_s = %v, want about 90 (100 s less a tenth)", summary["watchdog_s"])
	}
}

func TestProfileHelperRecordsASummaryAndNeverABundleOrWatchdog(t *testing.T) {
	t.Parallel()
	artifacts := t.TempDir()
	// A timeout and a step deadline a watchdog would otherwise be armed for.
	deadline := strconv.FormatInt(time.Now().Add(time.Minute).Unix(), 10)
	res := (&child{
		mode: "fail", artifacts: artifacts, args: []string{"-test.timeout=2m"},
		env: []string{paths.EnvTestProfileParent + "=1", paths.EnvTestDeadlineEpoch + "=" + deadline},
	}).run(t)
	if res.code != 1 {
		t.Fatalf("child exit %d, want its deliberate failure: %s%s", res.code, res.stdout, res.stderr)
	}
	dir := processDir(t, artifacts, profileLabel)
	wantSummary(t, readSummary(t, dir), map[string]any{
		"event": "helper-exit", "exit_code": float64(1), "helper_of": "1", "bundles": []any{}, "watchdog_s": nil,
	})
	if got := entryNames(t, dir); !reflect.DeepEqual(got, []string{"summary.json"}) {
		t.Fatalf("a helper's process directory holds %v, want the summary alone", got)
	}
	if strings.Contains(res.stderr, "profile") {
		t.Fatalf("a helper wrote a profiler line: %q", res.stderr)
	}
}

func TestProfileKilledChildLeavesTheStartSummary(t *testing.T) {
	t.Parallel()
	artifacts := t.TempDir()
	c := &child{mode: "hang", artifacts: artifacts}
	cmd, _, _ := c.command(t)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	waitFor(t, 30*time.Second, "the child's test to be running", func() bool { return fileExists(c.homeFile) })
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill child: %v", err)
	}
	_ = cmd.Wait()
	wantSummary(t, readSummary(t, processDir(t, artifacts, profileLabel)), map[string]any{
		"event": "started-no-exit-recorded", "exit_code": float64(-1), "bundles": []any{},
	})
}

func TestProfileOffGreenSaysNothing(t *testing.T) {
	t.Parallel()
	res := (&child{mode: "pass"}).run(t)
	if res.code != 0 || strings.Contains(res.stderr, "testjail:") {
		t.Fatalf("child exit %d, stderr %q, want a green run with no profiler line", res.code, res.stderr)
	}
}

func TestProfileOffRedSaysSoOnce(t *testing.T) {
	t.Parallel()
	res := (&child{mode: "fail"}).run(t)
	if res.code != 1 {
		t.Fatalf("child exit %d, want its deliberate failure: %s%s", res.code, res.stdout, res.stderr)
	}
	if got := strings.TrimSpace(res.stderr); got != offLine {
		t.Fatalf("stderr %q, want exactly %q", got, offLine)
	}
}

func TestProfileSwitchedOffIsSilent(t *testing.T) {
	t.Parallel()
	for name, artifacts := range map[string]string{"with a directory": t.TempDir(), "without one": ""} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			res := (&child{mode: "fail", artifacts: artifacts, env: []string{paths.EnvTestProfile + "=0"}}).run(t)
			if res.code != 1 || strings.Contains(res.stderr, "testjail:") {
				t.Fatalf("child exit %d, stderr %q, want a red run with no profiler line", res.code, res.stderr)
			}
			if artifacts != "" {
				if got := entryNames(t, artifacts); len(got) != 0 {
					t.Fatalf("switched-off profiler left %v under %s", got, artifacts)
				}
			}
		})
	}
}

func TestProfileCPUIsOptIn(t *testing.T) {
	t.Parallel()
	artifacts := t.TempDir()
	res := (&child{mode: "cpu", artifacts: artifacts, env: []string{paths.EnvTestProfile + "=cpu"}}).run(t)
	if res.code != 0 {
		t.Fatalf("child exit %d: %s%s", res.code, res.stdout, res.stderr)
	}
	dir := processDir(t, artifacts, profileLabel)
	if info, err := os.Stat(filepath.Join(dir, "cpu.pprof")); err != nil || info.Size() == 0 {
		t.Fatalf("cpu.pprof under PFM_TEST_PROFILE=cpu: %v", err)
	}
	if _, ok := readSummary(t, dir)["cpu_profile_error"]; ok {
		t.Fatalf("a started CPU profile recorded an error: %v", readSummary(t, dir)["cpu_profile_error"])
	}

	plain := t.TempDir()
	if res := (&child{mode: "cpu", artifacts: plain}).run(t); res.code != 0 {
		t.Fatalf("child exit %d: %s%s", res.code, res.stdout, res.stderr)
	}
	if got := entryNames(t, processDir(t, plain, profileLabel)); !reflect.DeepEqual(got, []string{"summary.json"}) {
		t.Fatalf("a run without PFM_TEST_PROFILE=cpu holds %v, want the summary alone", got)
	}
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

func TestProfileTestListingWritesNothing(t *testing.T) {
	t.Parallel()
	artifacts := t.TempDir()
	res := (&child{mode: "pass", artifacts: artifacts, args: []string{"-test.list=.*"}}).run(t)
	if res.code != 0 || !strings.Contains(res.stdout, "TestProfileChild") || strings.Contains(res.stderr, "testjail:") {
		t.Fatalf("child exit %d, stdout %q, stderr %q, want a plain listing", res.code, res.stdout, res.stderr)
	}
	if got := entryNames(t, artifacts); len(got) != 0 {
		t.Fatalf("listing tests left %v under %s", got, artifacts)
	}
}

func TestProfileLabelHoldsWhereverTheBinaryRuns(t *testing.T) {
	t.Parallel()
	moduleDir, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"in its package directory":    "",
		"at the module root":          moduleDir,
		"outside the module entirely": t.TempDir(),
	}
	for name, dir := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			artifacts := t.TempDir()
			if res := (&child{mode: "pass", artifacts: artifacts, dir: dir}).run(t); res.code != 0 {
				t.Fatalf("child exit %d: %s%s", res.code, res.stdout, res.stderr)
			}
			if got := readSummary(t, processDir(t, artifacts, profileLabel))["package"]; got != profileLabel {
				t.Fatalf("package label %v, want %s", got, profileLabel)
			}
		})
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

// TestProfileFixture runs the deliberately broken fixture package as the
// profiler's own end-to-end proof: a red exit, and the slow mode's duration knob.
func TestProfileFixture(t *testing.T) {
	moduleDir, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	fixtureDir := filepath.Join(moduleDir, "internal", "testjail", "testdata", "profilefixture")
	goBinary, err := deps.Resolve("go")
	if err != nil {
		t.Fatalf("resolve go: %v", err)
	}
	bin := filepath.Join(t.TempDir(), "fixture.test")
	built, err := (deps.RealRunner{}).Run(context.Background(),
		[]string{goBinary, "test", "-c", "-o", bin, "./internal/testjail/testdata/profilefixture"},
		deps.RunOptions{Dir: moduleDir, Env: append(os.Environ(), "GOTOOLCHAIN=local", "GOTELEMETRY=off")})
	if err != nil || built.ExitCode != 0 {
		t.Fatalf("go test -c the fixture: %v, exit %d: %s%s", err, built.ExitCode, built.Stderr, built.Stdout)
	}
	fixture := func(mode string, extra ...string) *child {
		return &child{
			exe: bin, dir: fixtureDir, args: []string{"-test.run=^TestFixture$", "-test.v"},
			env: append([]string{"PFM_PROFILE_FIXTURE=" + mode}, extra...),
		}
	}
	passed := regexp.MustCompile(`--- PASS: TestFixture \((\d+\.\d+)s\)`)
	slept := func(t *testing.T, res childResult) float64 {
		t.Helper()
		found := passed.FindStringSubmatch(res.stdout)
		if res.code != 0 || found == nil {
			t.Fatalf("slow fixture exit %d, want a pass with its duration: %s%s", res.code, res.stdout, res.stderr)
		}
		seconds, err := strconv.ParseFloat(found[1], 64)
		if err != nil {
			t.Fatal(err)
		}
		return seconds
	}

	t.Run("a red exit names the failing test, its state and file:line", func(t *testing.T) {
		t.Parallel()
		c := fixture("fail")
		c.artifacts = t.TempDir()
		res := c.run(t)
		if res.code != 1 {
			t.Fatalf("fixture exit %d, want its deliberate failure: %s%s", res.code, res.stdout, res.stderr)
		}
		dir := processDir(t, c.artifacts, "internal_testjail_testdata_profilefixture")
		wantSummary(
			t,
			readSummary(t, dir),
			map[string]any{"event": "exit", "exit_code": float64(1), "bundles": []any{"exit"}},
		)
		diagnosis := readFile(t, filepath.Join(dir, "exit", "DIAGNOSIS.txt"))
		where := regexp.MustCompile(
			`\[sync\.Mutex\.Lock\] at internal/testjail/testdata/profilefixture/fixture_test\.go:\d+ ` +
				`\(profilefixture\.blocked\), created by profilefixture\.TestFixture in goroutine \d+ ` +
				`at internal/testjail/testdata/profilefixture/fixture_test\.go:\d+\n`,
		)
		if !where.MatchString(diagnosis) {
			t.Fatalf(
				"diagnosis does not name the failing test with its state and module-relative file:line:\n%s",
				diagnosis,
			)
		}
		if strings.Contains(diagnosis, moduleDir) {
			t.Fatalf("diagnosis carries the absolute module path %s:\n%s", moduleDir, diagnosis)
		}
		if !strings.Contains(
			res.stderr,
			"testjail: profile bundle (exit) → "+filepath.Join(dir, "exit", "DIAGNOSIS.txt"),
		) {
			t.Fatalf("stderr does not point at the bundle: %q", res.stderr)
		}
	})
	t.Run("slow sleeps PFM_PROFILE_FIXTURE_SLOW_S=1 whole seconds, not the three-second default", func(t *testing.T) {
		t.Parallel()
		if got := slept(t, fixture("slow", "PFM_PROFILE_FIXTURE_SLOW_S=1").run(t)); got < 1 || got >= 3 {
			t.Fatalf("slow with PFM_PROFILE_FIXTURE_SLOW_S=1 slept %.2fs, want at least 1 and below 3", got)
		}
	})
	t.Run("slow sleeps three seconds by default", func(t *testing.T) {
		t.Parallel()
		if got := slept(t, fixture("slow").run(t)); got < 3 || got >= 5 {
			t.Fatalf("slow by default slept %.2fs, want 3 up to 5", got)
		}
	})
	t.Run("slow refuses a duration that is not whole seconds", func(t *testing.T) {
		t.Parallel()
		res := fixture("slow", "PFM_PROFILE_FIXTURE_SLOW_S=soon").run(t)
		if res.code != 1 || !strings.Contains(res.stdout, `PFM_PROFILE_FIXTURE_SLOW_S="soon"`) {
			t.Fatalf("fixture exit %d, want a failure naming the value: %s%s", res.code, res.stdout, res.stderr)
		}
	})
}
