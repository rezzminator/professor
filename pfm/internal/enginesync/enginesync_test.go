package enginesync

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// fakeBinary writes an engine-sync stand-in at path that records its argv,
// one argument per line, at path+".argv", prints stdout and stderr and exits
// with code.
func fakeBinary(t *testing.T, path, stdout, stderr string, code int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	quote := func(text string) string { return "'" + strings.ReplaceAll(text, "'", `'\''`) + "'" }
	script := "#!/bin/sh\n" +
		"for arg; do printf '%s\\n' \"$arg\"; done > " + quote(path+".argv") + "\n" +
		"printf '%s' " + quote(stdout) + "\n" +
		"printf '%s' " + quote(stderr) + " >&2\n" +
		"printf '%s\n' \"$ENGINE_SYNC_WORKBENCH_MARKER\" > " + quote(path+".marker") + "\n" +
		"exit " + strconv.Itoa(code) + "\n"
	if err := testjail.WriteExecutable(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func recordedArgv(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path + ".argv")
	if err != nil {
		t.Fatalf("the fake binary %s never ran: %v", path, err)
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
}

func TestResolveOrder(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	override := filepath.Join(root, "dev", "engine-sync")
	stableUnderHome := filepath.Join(home, ".local", "share", "engine-sync", "bin", "engine-sync")
	stableUnderXDG := filepath.Join(root, "xdg", "engine-sync", "bin", "engine-sync")
	stableUnderOwnHome := filepath.Join(root, "own", "bin", "engine-sync")
	fakeBinary(t, override, "", "", 0)
	fakeBinary(t, stableUnderHome, "", "", 0)
	fakeBinary(t, stableUnderXDG, "", "", 0)
	fakeBinary(t, stableUnderOwnHome, "", "", 0)
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{name: "an executable override wins", env: map[string]string{EnvBin: override}, want: override},
		{name: "no override takes the stable path under home", env: nil, want: stableUnderHome},
		{
			name: "XDG_DATA_HOME when absolute",
			env:  map[string]string{envXDGDataHome: filepath.Join(root, "xdg")},
			want: stableUnderXDG,
		},
		{
			name: "ENGINE_SYNC_HOME when absolute beats XDG_DATA_HOME",
			env:  map[string]string{EnvHome: filepath.Join(root, "own"), envXDGDataHome: filepath.Join(root, "xdg")},
			want: stableUnderOwnHome,
		},
		{
			name: "a relative ENGINE_SYNC_HOME is ignored",
			env:  map[string]string{EnvHome: "own"},
			want: stableUnderHome,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			runner := Runner{Env: &paths.MapEnv{Values: testCase.env}, Home: home}
			got, err := runner.Resolve()
			if err != nil || got != testCase.want {
				t.Fatalf("Resolve() = %q, %v; want %q", got, err, testCase.want)
			}
		})
	}
}

func TestResolveRefusesANonExecutableOverrideNamingIt(t *testing.T) {
	root := t.TempDir()
	notExecutable := filepath.Join(root, "dev-engine-sync")
	if err := os.WriteFile(notExecutable, []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stable := filepath.Join(root, ".local", "share", "engine-sync", "bin", "engine-sync")
	fakeBinary(t, stable, "", "", 0)
	runner := Runner{Env: &paths.MapEnv{Values: map[string]string{EnvBin: notExecutable}}, Home: root}
	got, err := runner.Resolve()
	var unavailable *UnavailableError
	if !errors.As(err, &unavailable) || !strings.Contains(err.Error(), notExecutable) {
		t.Fatalf("Resolve() = %q, %v; want an *UnavailableError naming %s", got, err, notExecutable)
	}
}

func TestResolveUnavailableNamesTheOverride(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "dev", "engine-sync")
	runner := Runner{Env: &paths.MapEnv{Values: map[string]string{EnvBin: missing}}, Home: root}
	_, err := runner.Resolve()
	var unavailable *UnavailableError
	if !errors.As(err, &unavailable) {
		t.Fatalf("Resolve() error = %v, want *UnavailableError", err)
	}
	if want := "engine-sync binary unavailable: " + missing + ": "; !strings.HasPrefix(err.Error(), want) {
		t.Fatalf("error = %q, want prefix %q", err, want)
	}
	if !strings.Contains(err.Error(), EnvBin+"="+missing) {
		t.Fatalf("error = %q does not name the override it passed over", err)
	}
}

func TestForwardPassesArgvOutputAndExitCodeThrough(t *testing.T) {
	cases := []struct {
		name string
		code int
	}{
		{name: "pass", code: 0},
		{name: "finding", code: 1},
		{name: "usage", code: 2},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "engine-sync")
			fakeBinary(t, binary, "CODEX CHECK PASS\n", "engine-sync codex: STALE x\n", testCase.code)
			runner := Runner{Env: &paths.MapEnv{Values: map[string]string{EnvBin: binary}}, Home: t.TempDir()}
			var stdout, stderr bytes.Buffer
			args := []string{"codex", "check", ".", "--model", "opus=x"}
			code := runner.Forward(context.Background(), "pfm codex", args, &stdout, &stderr)
			if code != testCase.code {
				t.Fatalf("Forward exit = %d, want %d", code, testCase.code)
			}
			if got := recordedArgv(t, binary); strings.Join(got, " ") != strings.Join(args, " ") {
				t.Fatalf("argv = %q, want %q", got, args)
			}
			if stdout.String() != "CODEX CHECK PASS\n" {
				t.Fatalf("stdout = %q", stdout.String())
			}
			want := "pfm codex: deprecated, runs engine-sync codex check . --model opus=x\nengine-sync codex: STALE x\n"
			if stderr.String() != want {
				t.Fatalf("stderr = %q, want %q", stderr.String(), want)
			}
		})
	}
}

func TestForwardWithoutABinaryIsExitOneNamingTheCause(t *testing.T) {
	home := t.TempDir()
	runner := Runner{Env: &paths.MapEnv{}, Home: home}
	var stdout, stderr bytes.Buffer
	code := runner.Forward(context.Background(), "pfm opencode", []string{"opencode", "build"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("Forward exit = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "pfm opencode: engine-sync binary unavailable: "+
		filepath.Join(home, ".local", "share", "engine-sync", "bin", "engine-sync")) {
		t.Fatalf("stderr = %q does not name the unavailable binary", stderr.String())
	}
}

func TestGlobalConfigPath(t *testing.T) {
	if got, want := GlobalConfigPath("/h"), "/h/.claude/engine-sync.json"; got != want {
		t.Fatalf("GlobalConfigPath = %q, want %q", got, want)
	}
}

func TestOldConfigDirs(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want []string
	}{
		{name: "under home", want: []string{"/h/.config/engine-sync"}},
		{
			name: "XDG_CONFIG_HOME when absolute",
			env:  map[string]string{envXDGConfigHome: "/x"},
			want: []string{"/h/.config/engine-sync", "/x/engine-sync"},
		},
		{
			name: "an XDG_CONFIG_HOME equal to the default is one directory",
			env:  map[string]string{envXDGConfigHome: "/h/.config"},
			want: []string{"/h/.config/engine-sync"},
		},
		{
			name: "a relative XDG_CONFIG_HOME is ignored",
			env:  map[string]string{envXDGConfigHome: "x"},
			want: []string{"/h/.config/engine-sync"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := OldConfigDirs(&paths.MapEnv{Values: testCase.env}, "/h")
			if strings.Join(got, "\n") != strings.Join(testCase.want, "\n") {
				t.Fatalf("OldConfigDirs = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestRunnerPreservesWorkbenchMarker(t *testing.T) {
	t.Setenv("ENGINE_SYNC_WORKBENCH_MARKER", "operator-marker")
	for _, childEnv := range [][]string{nil, {"ENGINE_SYNC_WORKBENCH_MARKER=operator-marker", "HOME=/fixture"}} {
		runner, binary := fakeRunner(t, "ok", "", 0)
		runner.ChildEnv = childEnv
		if _, err := runner.run(context.Background(), []string{"version"}); err != nil {
			t.Fatal(err)
		}
		marker, err := os.ReadFile(binary + ".marker")
		if err != nil || strings.TrimSpace(string(marker)) != "operator-marker" {
			t.Fatalf("workbench marker = %q (%v), want %q", marker, err, "operator-marker")
		}
	}
}
