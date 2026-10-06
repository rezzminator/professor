package update

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestUpdateRollbackOwnedFiles(t *testing.T) {
	const (
		hooksBefore = "{\"hooks\":{}}\n"
		hooksAfter  = "{\"hooks\":{\"SessionStart\":[{\"hooks\":[{\"type\":\"command\",\"command\":\"pfm internal new-hook\"}]}]}}\n"
		candidate   = "written by the candidate install\n"
		operator    = "{\"operator\":\"saved while the update ran\"}\n"
	)
	for _, testcase := range []struct {
		name            string
		before          map[string]string
		after           map[string]string
		between         func(*testing.T, map[string]string)
		want            map[string]string
		removed         string
		installDir      string
		snapshotDir     string
		symlink         bool
		restored        string
		residue         string
		checkPriorState bool
	}{
		{
			name:     "hook added on a new event",
			before:   map[string]string{"hooks": hooksBefore},
			after:    map[string]string{"hooks": hooksAfter},
			restored: "hooks", checkPriorState: true,
		},
		{
			name:  "files absent before the install",
			after: map[string]string{"openCode": candidate, "mcpLedger": candidate, "hookLedger": candidate},
		},
		{
			name: "registrations and config restored",
			before: map[string]string{
				"claudeRegistry": "{\"mcpServers\":{}}\n", "codexConfig": "model = \"operator\"\n",
				"hookTrust": "{}\n", "config": "{\"version\":2}\n",
			},
			after: map[string]string{
				"claudeRegistry": candidate, "codexConfig": candidate, "hookTrust": candidate, "config": candidate,
			},
		},
		{
			name: "rewritten after the install",
			before: map[string]string{
				"claudeRegistry": "{\"mcpServers\":{}}\n", "codexConfig": "model = \"operator\"\n",
				"hookTrust": "{}\n", "config": "{\"version\":2}\n",
			},
			after: map[string]string{
				"claudeRegistry": candidate, "codexConfig": candidate, "hookTrust": candidate, "config": candidate,
			},
			between: func(t *testing.T, paths map[string]string) {
				path := paths["claudeRegistry"]
				writeProjectFixtureFile(t, filepath.Dir(path), filepath.Base(path), operator)
			},
			want: map[string]string{"claudeRegistry": operator}, residue: "claudeRegistry",
		},
		{
			name:   "removed after the install",
			before: map[string]string{"codexConfig": "model = \"operator\"\n"},
			after:  map[string]string{"codexConfig": candidate},
			between: func(t *testing.T, paths map[string]string) {
				if err := os.Remove(paths["codexConfig"]); err != nil {
					t.Fatal(err)
				}
			},
			removed: "codexConfig", residue: "codexConfig",
		},
		{
			name:       "after-state unreadable",
			before:     map[string]string{"codexConfig": "model = \"operator\"\n"},
			after:      map[string]string{"codexConfig": candidate},
			installDir: "codexConfig", residue: "codexConfig",
		},
		{
			name:    "symlinked file",
			before:  map[string]string{"hooks": hooksBefore},
			after:   map[string]string{"hooks": hooksAfter},
			symlink: true, restored: "hookTarget",
		},
		{
			name:    "dangling symlinked file",
			after:   map[string]string{"hooks": hooksAfter},
			symlink: true,
		},
		{
			name:        "a candidate path cannot be snapshotted",
			snapshotDir: "hooks",
		},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			t.Setenv("CLAUDE_CONFIG_DIR", "")
			runtime, repo := updateRollbackTestRuntime(t)
			home := runtime.Paths.Home
			codexHome := filepath.Join(home, ".codex-1")
			runtime.Config = pfmconfig.Config{
				Accounts:      []pfmconfig.Account{{ID: 1, ConfigDir: filepath.Join(home, ".cc", "1")}},
				CodexAccounts: []pfmconfig.CodexAccount{{ID: 1, Home: codexHome}},
			}
			paths := map[string]string{
				"hooks":          filepath.Join(codexHome, "hooks.json"),
				"hookTarget":     filepath.Join(home, "dotfiles", "hooks.json"),
				"codexConfig":    filepath.Join(codexHome, "config.toml"),
				"hookTrust":      filepath.Join(codexHome, ".professor-hook-trust.json"),
				"claudeRegistry": filepath.Join(home, ".cc", "1", ".claude.json"),
				"config":         filepath.Join(home, "cfg", "pfm.config.json"),
				"openCode":       installer.OpenCodeConfigPath(home),
				"mcpLedger":      filepath.Join(installer.ManagedRoot(home), "mcp-ownership.json"),
				"hookLedger":     filepath.Join(installer.ManagedRoot(home), "settings-hook-ownership.json"),
			}
			if _, ok := testcase.before["config"]; ok {
				runtime.Config.Path, runtime.Config.Exists = paths["config"], true
			}
			for key, content := range testcase.before {
				path := paths[key]
				if testcase.symlink && key == "hooks" {
					path = paths["hookTarget"]
				}
				writeProjectFixtureFile(t, filepath.Dir(path), filepath.Base(path), content)
			}
			if testcase.symlink {
				for _, dir := range []string{codexHome, filepath.Dir(paths["hookTarget"])} {
					if err := os.MkdirAll(dir, 0o700); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Symlink(paths["hookTarget"], paths["hooks"]); err != nil {
					t.Fatal(err)
				}
			}
			if testcase.snapshotDir != "" {
				if err := os.MkdirAll(paths[testcase.snapshotDir], 0o700); err != nil {
					t.Fatal(err)
				}
			}
			var between func()
			if testcase.between != nil {
				between = func() { testcase.between(t, paths) }
			}
			installCalled := false
			stderr := updateRollbackAfterInstall(t, runtime, repo, func() error {
				installCalled = true
				for key, content := range testcase.after {
					path := paths[key]
					writeProjectFixtureFile(t, filepath.Dir(path), filepath.Base(path), content)
				}
				if testcase.installDir != "" {
					path := paths[testcase.installDir]
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(path, 0o700); err != nil {
						t.Fatal(err)
					}
				}
				if between != nil {
					return nil
				}
				return errors.New("injected install failure")
			}, between, func() {
				if testcase.checkPriorState {
					if got, err := os.ReadFile(paths["hooks"]); err != nil || string(got) != hooksBefore {
						t.Errorf("previous install sees hooks = %q, %v; want %q", got, err, hooksBefore)
					}
				}
			})
			for key := range testcase.after {
				path := paths[key]
				if key == testcase.installDir {
					if info, err := os.Stat(path); err != nil || !info.IsDir() {
						t.Errorf("%s after rollback = %v, %v; want the directory left as is", path, info, err)
					}
					continue
				}
				want, existed := testcase.before[key]
				if override, ok := testcase.want[key]; ok {
					want, existed = override, true
				}
				if key == testcase.removed {
					existed = false
				}
				if !existed {
					if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
						t.Errorf("%s after rollback: stat error = %v; want absent", path, err)
					}
					continue
				}
				if got, err := os.ReadFile(path); err != nil || string(got) != want {
					t.Errorf("%s after rollback = %q, %v; want %q", path, got, err, want)
				}
			}
			if testcase.restored != "" {
				physical, err := filepath.EvalSymlinks(paths[testcase.restored])
				if err != nil {
					t.Fatal(err)
				}
				want := "pfm update: restored " + physical + " to its pre-update state"
				if !strings.Contains(stderr, want) {
					t.Errorf("stderr = %q; want %q", stderr, want)
				}
			}
			if testcase.residue != "" {
				physicalHome, err := filepath.EvalSymlinks(home)
				if err != nil {
					t.Fatal(err)
				}
				relative, err := filepath.Rel(home, paths[testcase.residue])
				if err != nil {
					t.Fatal(err)
				}
				physical := filepath.Join(physicalHome, relative)
				want := "MCP registration " + physical +
					" changed after the update's install wrote it; left as is — reconcile it by hand"
				if testcase.removed != "" {
					want = "MCP registration " + physical +
						" was removed after the update's install wrote it; reconcile it by hand"
				} else if testcase.installDir != "" {
					want = physical
				}
				if !strings.Contains(stderr, "rollback residue: ") || !strings.Contains(stderr, want) {
					t.Errorf("stderr = %q; want rollback residue naming %q", stderr, want)
				}
			}
			if testcase.symlink {
				if info, err := os.Lstat(paths["hooks"]); err != nil || info.Mode()&os.ModeSymlink == 0 {
					t.Errorf("hooks.json after rollback = %v, %v; want its symlink preserved", info, err)
				}
			}
			if testcase.snapshotDir != "" {
				if installCalled || !strings.Contains(stderr, "snapshot update-owned files before install: ") {
					t.Errorf(
						"install called = %v, stderr = %q; want snapshot failure before install",
						installCalled,
						stderr,
					)
				}
				canonical := filepath.Join(home, ".local", "bin", "pfm")
				if got, err := os.ReadFile(canonical); err != nil || string(got) != "old\n" {
					t.Errorf("owned binary after snapshot failure = %q, %v; want old", got, err)
				}
			}
		})
	}
}

func updateRollbackAfterInstall(
	t *testing.T,
	runtime pfmconfig.Runtime,
	repo string,
	install func() error,
	between, beforeRollbackInstall func(),
) string {
	t.Helper()
	oldBuild, oldInstall, oldDoctor := updateBuildCandidate, updateApplyInstall, updateRunDoctor
	oldRollbackInstall, oldRollbackDoctor := updateRollbackInstall, updateRollbackDoctor
	t.Cleanup(func() {
		updateBuildCandidate, updateApplyInstall, updateRunDoctor = oldBuild, oldInstall, oldDoctor
		updateRollbackInstall, updateRollbackDoctor = oldRollbackInstall, oldRollbackDoctor
	})
	updateBuildCandidate = func(_ context.Context, _, _, output string) error {
		return testjail.WriteExecutable(output, []byte("new\n"), 0o755)
	}
	updateApplyInstall = func(context.Context, string, string, string, pfmconfig.Runtime, bool, io.Writer, io.Writer) error {
		return install()
	}
	updateRunDoctor = func(context.Context, string, pfmconfig.Runtime, string, bool, io.Writer, io.Writer) (doctorOutcome, error) {
		if between != nil {
			between()
		}
		return doctorOutcome{Exit: 3, Failures: 1}, nil
	}
	stubUpdateBaselineDoctor(t, doctorOutcome{})
	updateRollbackInstall = func(context.Context, string, string, string, pfmconfig.Runtime, bool, io.Writer, io.Writer) error {
		beforeRollbackInstall()
		return nil
	}
	updateRollbackDoctor = func(context.Context, string, pfmconfig.Runtime, string, bool, io.Writer, io.Writer) (doctorOutcome, error) {
		return doctorOutcome{}, nil
	}
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--repo", repo}, &stdout, &stderr, runtime); code != 5 {
		t.Fatalf("Run() code = %d, stdout = %q, stderr = %q; want 5", code, stdout.String(), stderr.String())
	}
	return stderr.String()
}
