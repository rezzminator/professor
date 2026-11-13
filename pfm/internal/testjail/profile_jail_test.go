//go:build jail

package testjail

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

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
