package installer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
)

func TestInstallManagedCleanup(t *testing.T) {
	for _, scenario := range []string{"absent", "wrong", "correct", "preview", "off", "relative", "unreadable"} {
		t.Run(scenario, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "managed")
			path := filepath.Join(dir, "pfm.json")
			options := Options{ManagedSettingsDir: dir, CleanupPeriodDays: 36500, RequireManagedCleanup: true}
			apply := true
			switch scenario {
			case "wrong":
				writeFixture(t, path, `{"cleanupPeriodDays":30}`)
			case "correct":
				writeFixture(t, path, `{"cleanupPeriodDays":36500}`)
			case "preview":
				apply = false
			case "off":
				options.RequireManagedCleanup = false
			case "relative":
				options.ManagedSettingsDir = "relative-managed"
				path = filepath.Join(options.ManagedSettingsDir, "pfm.json")
			case "unreadable":
				writeFixture(t, path, "{")
			}
			var output bytes.Buffer
			options.Stdout = &output
			installer := &engine{options: options, apply: apply}
			err := installer.installManagedCleanup(context.Background())
			if scenario == "unreadable" {
				if err == nil || !strings.Contains(err.Error(), "managed-cleanup "+path+":") {
					t.Fatalf("err=%v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := "  change  write " + path + "\n"
			switch scenario {
			case "correct":
				want = "  ok      " + path + "\n"
			case "off", "relative":
				want = ""
			}
			if output.String() != want {
				t.Fatalf("output=%q want=%q", output.String(), want)
			}
			switch scenario {
			case "absent", "wrong", "correct":
				raw, err := os.ReadFile(path)
				content := "{\"cleanupPeriodDays\":36500}\n"
				if scenario == "correct" {
					content = strings.TrimSuffix(content, "\n")
				}
				if err != nil || string(raw) != content {
					t.Fatalf("content=%q err=%v", raw, err)
				}
				if scenario != "correct" {
					info, err := os.Stat(path)
					if err != nil || info.Mode().Perm() != 0o644 {
						t.Fatalf("info=%v err=%v", info, err)
					}
				}
			case "preview":
				if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("preview path err=%v", err)
				}
			}
		})
	}
}

// managedCleanupRunner plays cached sudo against a parent that starts unwritable.
type managedCleanupRunner struct {
	t      *testing.T
	source string
	calls  [][]string
}

func (runner *managedCleanupRunner) Run(_ context.Context, name string, args ...string) error {
	if name != "sudo" || len(args) < 2 || args[0] != "-n" {
		runner.t.Fatalf("command=%s %q", name, args)
	}
	runner.calls = append(runner.calls, append([]string(nil), args...))
	if args[1] != installProgram {
		return nil
	}
	runner.source = args[len(args)-2]
	info, err := os.Stat(runner.source)
	if err != nil || info.Mode().Perm() != 0o600 {
		runner.t.Fatalf("temp=%v err=%v", info, err)
	}
	raw, err := os.ReadFile(runner.source)
	if err != nil {
		runner.t.Fatal(err)
	}
	return atomicfile.Write(args[len(args)-1], raw, 0o644)
}

func TestInstallManagedCleanupCachedSudo(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Application Support", "ClaudeCode", "managed-settings.d")
	runner := &managedCleanupRunner{t: t}
	var output bytes.Buffer
	installer := &engine{
		apply: true,
		options: Options{
			ManagedSettingsDir:    dir,
			RequireManagedCleanup: true,
			CleanupPeriodDays:     36500,
			Runner:                runner,
			Stdout:                &output,
			writeManaged:          func(string, []byte) error { return os.ErrPermission },
		},
	}
	if err := installer.installManagedCleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) == 0 {
		t.Fatal("sudo fallback did not run")
	}
	want := [][]string{}
	for _, args := range managedInstallArgs(runner.source, filepath.Join(dir, "pfm.json")) {
		want = append(want, append([]string{"-n"}, args...))
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls=%q want=%q", runner.calls, want)
	}
	for _, args := range runner.calls {
		if !strings.Contains(output.String(), "sudo "+shellCommandLine(args...)+"\n") {
			t.Fatalf("echo=%q", output.String())
		}
	}
	if got := InspectManagedCleanup(dir, true, 36500); got.State != "ok" {
		t.Fatalf("status=%+v", got)
	}
	if _, err := os.Stat(runner.source); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temp cleanup err=%v", err)
	}
}

func TestInspectManagedCleanup(t *testing.T) {
	for _, tc := range []struct {
		raw   string
		state string
	}{{`{"cleanupPeriodDays":30}`, "wrong"}, {`{"cleanupPeriodDays":36500}`, "ok"}, {"{", "unreadable"}, {`{}`, "unreadable"}, {`{"cleanupPeriodDays":"30"}`, "unreadable"}} {
		t.Run(fmt.Sprintf("%s_%s", tc.state, tc.raw), func(t *testing.T) {
			dir := t.TempDir()
			writeFixture(t, filepath.Join(dir, "pfm.json"), tc.raw)
			got := InspectManagedCleanup(dir, true, 36500)
			if got.State != tc.state || (got.Err != nil) != (tc.state == "unreadable") {
				t.Fatalf("status=%+v", got)
			}
		})
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "pfm.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got := InspectManagedCleanup(dir, true, 36500); got.State != "unreadable" {
		t.Fatalf("directory=%+v", got)
	}
}

func TestInstallManagedCleanupDeclinedSudo(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Application Support", "managed-settings.d")
	path := filepath.Join(dir, "pfm.json")
	var output bytes.Buffer
	installer := &engine{
		apply: true,
		options: Options{
			ManagedSettingsDir:    dir,
			RequireManagedCleanup: true,
			CleanupPeriodDays:     36500,
			Runner:                stateRunner{err: os.ErrPermission},
			Stdout:                &output,
			writeManaged:          func(string, []byte) error { return os.ErrPermission },
		},
	}
	if err := installer.installManagedCleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n")
	want := "  warn    managed-cleanup " + path + " — sudo -n needs cached credentials; run: sudo mkdir -p " + shellCommandLine(
		dir,
	) + " && printf '%s\\n' '{\"cleanupPeriodDays\":36500}' | sudo tee " + shellCommandLine(
		path,
	) + " >/dev/null"
	if len(lines) != 3 || lines[0] != "  change  write "+path || !strings.HasPrefix(lines[1], "sudo -n ") ||
		lines[2] != want {
		t.Fatalf("output=%q want warning=%q", output.String(), want)
	}
}

func TestInstallManagedCleanupTemporaryDirectoryFailure(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "not-a-directory")
	writeFixture(t, bad, "blocked")
	t.Setenv("TMPDIR", bad)
	installer := &engine{
		apply: true,
		options: Options{
			ManagedSettingsDir:    filepath.Join(dir, "managed"),
			RequireManagedCleanup: true,
			CleanupPeriodDays:     36500,
			Stdout:                &bytes.Buffer{},
			writeManaged:          func(string, []byte) error { return os.ErrPermission },
		},
	}
	if err := installer.installManagedCleanup(
		context.Background(),
	); err == nil ||
		!strings.Contains(err.Error(), "create managed-cleanup temporary directory:") {
		t.Fatalf("err=%v", err)
	}
}

func TestInstallManagedCleanupInRun(t *testing.T) {
	for _, scenario := range []string{"apply", "preview", "declined sudo"} {
		t.Run(scenario, func(t *testing.T) {
			home := t.TempDir()
			dir := filepath.Join(home, "managed")
			path := filepath.Join(dir, "pfm.json")
			var output bytes.Buffer
			options := Options{
				Home:                  home,
				Mode:                  ModeApply,
				MCPConfigPath:         testConfigPath(t),
				ManagedSettingsDir:    dir,
				RequireManagedCleanup: true,
				CleanupPeriodDays:     36500,
				Runner:                &fakeRunner{manager: true, nameSyncIdle: true, failSudo: true},
				Stdout:                &output,
			}
			if scenario == "preview" {
				options.Mode = ModeDryRun
			}
			if scenario == "declined sudo" {
				options.writeManaged = func(string, []byte) error { return os.ErrPermission }
			}
			if _, err := Run(context.Background(), options); err != nil {
				t.Fatalf("install err=%v\n%s", err, output.String())
			}
			if !strings.Contains(output.String(), "  change  write "+path+"\n") {
				t.Fatalf("cleanup step omitted:\n%s", output.String())
			}
			switch scenario {
			case "apply":
				if status := InspectManagedCleanup(dir, true, 36500); status.State != "ok" {
					t.Fatalf("status=%+v", status)
				}
			case "declined sudo":
				want := "  warn    managed-cleanup " + path + " — sudo -n needs cached credentials; run: sudo mkdir -p " + shellCommandLine(
					dir,
				) + " && printf '%s\\n' '{\"cleanupPeriodDays\":36500}' | sudo tee " + shellCommandLine(
					path,
				) + " >/dev/null\n"
				if !strings.Contains(output.String(), want) {
					t.Fatalf("warning missing:\n%s", output.String())
				}
			}
		})
	}
}
