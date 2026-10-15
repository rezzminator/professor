package installer

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// goRunner is a go toolchain for the callmeter build: LookPath finds go
// unless missing, and a build writes output to its -o path, or exits 1 with
// failure. Every other command is the plugin runner's.
type goRunner struct {
	pluginRunner
	missing bool
	output  string
	failure string
	builds  []deps.RunOptions
	argvs   [][]string
	onBuild func()
}

func (runner *goRunner) LookPath(name string) (string, error) {
	if name != "go" || runner.missing {
		return "", errors.New("no " + name + " on PATH")
	}
	return "/toolchain/bin/go", nil
}

func (runner *goRunner) Run(ctx context.Context, argv []string, options deps.RunOptions) (deps.RunResult, error) {
	if argv[0] != "/toolchain/bin/go" {
		return runner.pluginRunner.Run(ctx, argv, options)
	}
	runner.builds = append(runner.builds, options)
	runner.argvs = append(runner.argvs, argv)
	if runner.onBuild != nil {
		runner.onBuild()
	}
	if runner.failure != "" {
		return deps.RunResult{ExitCode: 1, Stderr: []byte(runner.failure)}, nil
	}
	output := argv[slices.Index(argv, "-o")+1]
	return deps.RunResult{}, os.WriteFile(output, []byte(runner.output), 0o600)
}

// writeCallmeterCheckout lays out the callmeter repository: its module with
// the plugin under plugins/callmeter at version.
func writeCallmeterCheckout(t *testing.T, root, version string) (module, checkout string) {
	t.Helper()
	module = filepath.Join(root, "callmeter")
	writeFixture(t, filepath.Join(module, "go.mod"), "module example.com/callmeter\n")
	checkout = filepath.Join(module, "plugins", "callmeter")
	writeFixture(
		t,
		filepath.Join(checkout, ".claude-plugin", "plugin.json"),
		`{"name":"callmeter","version":"`+version+`"}`,
	)
	writeFixture(t, filepath.Join(checkout, "libexec", "callmeter"), "#!/bin/sh\n")
	return module, checkout
}

// TestEnsureCallmeterBinary: an -alpha install builds callmeter from its
// checkout, stamped with the checkout's version, into the path the wrapper
// runs; the binary there is kept as callmeter.prev, an equal build changes
// nothing, a failed build leaves the binary as it was, and a dry run builds
// nothing. The database beside it is never touched.
func TestEnsureCallmeterBinary(t *testing.T) {
	for _, test := range []struct {
		name       string
		existing   string
		runner     goRunner
		apply      bool
		wantBinary string
		wantPrev   string
		wantLine   string
		wantErr    string
	}{
		{"first build", "", goRunner{output: "new"}, true, "new", "", "  change  place callmeter 0.4.0 built from {module} at {binary}\n", ""},
		{
			"a new build keeps the old binary as callmeter.prev", "old",
			goRunner{output: "new"},
			true, "new", "old",
			"  change  place callmeter 0.4.0 built from {module} at {binary}, the binary there kept as {binary}.prev\n", "",
		},
		{
			"an equal build changes nothing", "new",
			goRunner{output: "new"},
			true, "new", "",
			"  ok      callmeter binary {binary} matches the build of {module}\n", "",
		},
		// The wrapper downloads over a binary it cannot run: equal bytes are not enough.
		{
			"an equal binary that is not executable is replaced", "new-",
			goRunner{output: "new"},
			true, "new", "new",
			"  change  place callmeter 0.4.0 built from {module} at {binary}, the binary there kept as {binary}.prev\n", "",
		},
		{
			"a failed build leaves the binary", "old",
			goRunner{failure: "cmd/callmeter: syntax error"},
			true, "old", "", "",
			"build callmeter 0.4.0 in {module}: ",
		},
		{"no go", "old", goRunner{missing: true}, true, "old", "", "", "go not found"},
		{
			"a dry run builds nothing", "old",
			goRunner{output: "new"},
			false, "old", "",
			"  change  build callmeter 0.4.0 from {module} and place it at {binary} when it differs\n", "",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("GOFLAGS", "-mod=vendor")
			t.Setenv("GOOS", "plan9")
			t.Setenv("GOWORK", "/elsewhere/go.work")
			home := t.TempDir()
			state := t.TempDir()
			module, checkout := writeCallmeterCheckout(t, t.TempDir(), "0.4.0")
			binary := filepath.Join(state, "callmeter", "bin", "0.4.0", "callmeter")
			database := filepath.Join(state, "callmeter", "callmeter.db")
			writeFixture(t, database, "rows")
			if test.existing != "" {
				// "new-" is the build's bytes in a file without its execute bits.
				mode := os.FileMode(0o755)
				if test.existing == "new-" {
					mode = 0o644
				}
				writeFixture(t, binary, strings.TrimSuffix(test.existing, "-"))
				if err := os.Chmod(binary, mode); err != nil {
					t.Fatal(err)
				}
			}
			var out bytes.Buffer
			runner := test.runner
			inst := &engine{options: Options{
				Home: home, ProcessRunner: &runner, Stdout: &out,
				Env: &paths.MapEnv{Values: map[string]string{"XDG_STATE_HOME": state}},
			}, apply: test.apply}
			err := inst.ensureCallmeterBinary(
				context.Background(),
				&ClaudePluginTarget{Name: "callmeter", Checkout: checkout},
			)
			replace := strings.NewReplacer("{module}", module, "{binary}", binary)
			if want := replace.Replace(
				test.wantErr,
			); want == "" && err != nil ||
				want != "" && (err == nil || !strings.Contains(err.Error(), want)) {
				t.Fatalf("err = %v, want %q\n%s", err, want, out.String())
			}
			if want := replace.Replace(test.wantLine); want != "" && !strings.Contains(out.String(), want) {
				t.Fatalf("missing %q:\n%s", want, out.String())
			}
			got, _ := os.ReadFile(binary)
			prev, _ := os.ReadFile(binary + ".prev")
			if string(got) != test.wantBinary || string(prev) != test.wantPrev {
				t.Fatalf("binary %q prev %q, want %q and %q", got, prev, test.wantBinary, test.wantPrev)
			}
			if test.wantBinary == "new" {
				if info, err := os.Stat(binary); err != nil || info.Mode().Perm() != 0o755 {
					t.Fatalf("binary mode %v %v, want 0755", info, err)
				}
			}
			entries, _ := os.ReadDir(filepath.Dir(binary))
			for _, entry := range entries {
				if entry.Name() != "callmeter" && entry.Name() != "callmeter.prev" {
					t.Fatalf("left %s beside the binary", entry.Name())
				}
			}
			if readFixture(t, database) != "rows" {
				t.Fatal("the callmeter database was touched")
			}
			if !test.apply || test.runner.missing {
				if len(runner.builds) != 0 {
					t.Fatalf("built %v, want no build", runner.argvs)
				}
				return
			}
			if len(runner.builds) != 1 || runner.builds[0].Dir != module {
				t.Fatalf("builds %+v, want one in %s", runner.builds, module)
			}
			argv := strings.Join(runner.argvs[0], " ")
			if !strings.Contains(
				argv,
				"build -trimpath -buildvcs=false -ldflags -s -w -buildid= -X main.version=0.4.0 -o ",
			) ||
				!strings.HasSuffix(argv, " ./cmd/callmeter") {
				t.Fatalf("argv %q", argv)
			}
			env := runner.builds[0].Env
			// GOFLAGS set in the environment also outranks one go env -w wrote.
			pinned := []string{"CGO_ENABLED=0", "GOWORK=off", "GOFLAGS=-mod=readonly"}
			missing := slices.ContainsFunc(pinned, func(entry string) bool { return !slices.Contains(env, entry) })
			kept := slices.ContainsFunc(env, func(entry string) bool {
				return entry == "GOFLAGS=-mod=vendor" || entry == "GOWORK=/elsewhere/go.work" ||
					strings.HasPrefix(entry, "GOOS=")
			})
			if missing || kept {
				t.Fatalf("build env keeps a variable that changes the binary, or lacks %v: %v", pinned, env)
			}
		})
	}
}

// TestCallmeterBinaryPath: the path follows the wrapper's own resolution.
func TestCallmeterBinaryPath(t *testing.T) {
	for _, test := range []struct {
		name   string
		values map[string]string
		want   string
	}{
		{"CALLMETER_HOME", map[string]string{"CALLMETER_HOME": "/cm", "XDG_STATE_HOME": "/state"}, "/cm/bin/1.0/callmeter"},
		{"absolute XDG_STATE_HOME", map[string]string{"XDG_STATE_HOME": "/state"}, "/state/callmeter/bin/1.0/callmeter"},
		{"relative XDG_STATE_HOME is ignored", map[string]string{"XDG_STATE_HOME": "state"}, "/u/.local/state/callmeter/bin/1.0/callmeter"},
		{"home", nil, "/u/.local/state/callmeter/bin/1.0/callmeter"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := callmeterBinaryPath(&paths.MapEnv{Values: test.values}, "/u", "1.0")
			if err != nil || got != test.want {
				t.Fatalf("got %q, %v want %q", got, err, test.want)
			}
		})
	}
}

// TestPlaceCallmeterBinaryTakesTheWrapperLock: placing holds the wrapper's
// bin/.lock-{version}, so a download in flight is never overwritten and never
// overwrites; a lock past the wrapper's stale bound is a killed run's, taken over.
func TestPlaceCallmeterBinaryTakesTheWrapperLock(t *testing.T) {
	for _, test := range []struct {
		name    string
		lockAge time.Duration // 0: no lock
		wantErr string
	}{
		{"no lock", 0, ""},
		{"a live download holds it", time.Second, "held by a callmeter download or another pfm install"},
		{"a stale lock is taken over", 2 * callmeterLockStale, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			bin := t.TempDir()
			binary := filepath.Join(bin, "0.4.0", "callmeter")
			writeFixture(t, binary, "old")
			staging := t.TempDir()
			built := filepath.Join(staging, "callmeter")
			writeFixture(t, built, "new")
			lock := filepath.Join(bin, ".lock-0.4.0")
			if test.lockAge != 0 {
				if err := os.Mkdir(lock, 0o700); err != nil {
					t.Fatal(err)
				}
				at := time.Now().Add(-test.lockAge)
				if err := os.Chtimes(lock, at, at); err != nil {
					t.Fatal(err)
				}
			}
			err := placeCallmeterBinary(built, binary, true)
			want := map[bool]string{true: "old", false: "new"}[test.wantErr != ""]
			if got, _ := os.ReadFile(binary); string(got) != want ||
				(test.wantErr == "") != (err == nil) || err != nil && !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("binary %q, err %v; want %q, %q", got, err, want, test.wantErr)
			}
			if _, statErr := os.Stat(lock); test.wantErr == "" && !errors.Is(statErr, fs.ErrNotExist) {
				t.Fatalf("lock %s left behind: %v", lock, statErr)
			}
		})
	}
}

// TestCallmeterCheckoutVersion: the checkout's plugin.json version is one path
// element under bin/, never one that leaves it.
func TestCallmeterCheckoutVersion(t *testing.T) {
	for _, test := range []struct {
		version string
		ok      bool
	}{
		{"0.4.0", true},
		{"0.4.0-rc.1+dev", true},
		{"", false},
		{".", false},
		{"..", false},
		{"../0.4.0", false},
	} {
		t.Run(test.version, func(t *testing.T) {
			_, checkout := writeCallmeterCheckout(t, t.TempDir(), test.version)
			got, err := callmeterCheckoutVersion(checkout)
			if test.ok && (err != nil || got != test.version) || !test.ok && err == nil {
				t.Fatalf("got %q, %v want ok=%v", got, err, test.ok)
			}
		})
	}
}

// TestEnsureClaudePluginsBuildsCallmeterBeforeItsCopy: the callmeter build
// runs before its copy is refreshed, and a failed build leaves the copy as it
// was — its hooks never run a version whose binary is not in place.
func TestEnsureClaudePluginsBuildsCallmeterBeforeItsCopy(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "built", true: "build failed"}[failed], func(t *testing.T) {
			home, binary, first, second := pluginFixture(t)
			t.Setenv("CALLMETER_HOME", "")
			t.Setenv("XDG_STATE_HOME", "")
			root := t.TempDir()
			writeCallmeterCheckout(t, root, "0.4.0")
			mirror := filepath.Join(home, ".local", "share", "callmeter-dev", "plugins", "callmeter")
			runner := &goRunner{output: "new"}
			if failed {
				runner.failure = "boom"
			}
			copied := false
			runner.onBuild = func() { _, err := os.Stat(mirror); copied = err == nil }
			var out bytes.Buffer
			inst := pluginEngine(home, binary, first, second, runner, &out, true)
			inst.options.Version = alphaVersion
			inst.options.ClaudePluginCheckoutRoot = root
			inst.options.ClaudePlugins = bareList("callmeter")
			simulateClaude(t, &runner.pluginRunner)
			err := inst.ensureClaudePlugins(context.Background())
			if len(runner.builds) != 1 || copied {
				t.Fatalf("builds=%d copied before the build=%v\n%s", len(runner.builds), copied, out.String())
			}
			_, statErr := os.Stat(mirror)
			if failed {
				if err == nil || !strings.Contains(err.Error(), "build callmeter 0.4.0") || statErr == nil {
					t.Fatalf("err=%v copy stat=%v, want the build failure and no copy\n%s", err, statErr, out.String())
				}
				return
			}
			if err != nil || statErr != nil {
				t.Fatalf("err=%v copy stat=%v\n%s", err, statErr, out.String())
			}
			if got := readFixture(
				t,
				filepath.Join(home, ".local", "state", "callmeter", "bin", "0.4.0", "callmeter"),
			); got != "new" {
				t.Fatalf("binary %q", got)
			}
		})
	}
}
