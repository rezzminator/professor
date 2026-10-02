package installer

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestRemoveRetiredNudgeStateReportsOneChangeAndPreservesOtherSIDFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"nudge-ctx-session", "nudge-band-session", "statusline-effort-session", "cc-sess.%1"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "nudge-ctx-directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "cc-sess.%1"), filepath.Join(dir, "nudge-band-link")); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	installer := &engine{options: Options{
		Env: &paths.MapEnv{Values: map[string]string{paths.EnvSIDDir: dir}}, Stdout: &output,
	}, apply: true}
	if err := installer.removeRetiredNudgeState(); err != nil {
		t.Fatal(err)
	}
	if installer.report.Changed != 1 ||
		strings.Count(output.String(), "remove retired compact-nudge state from "+dir) != 1 {
		t.Fatalf("first sweep changed=%d output=%q", installer.report.Changed, output.String())
	}
	for _, name := range []string{"nudge-ctx-session", "nudge-band-session"} {
		if _, err := os.Lstat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("retired file %s remains: %v", name, err)
		}
	}
	for _, name := range []string{"statusline-effort-session", "cc-sess.%1", "nudge-ctx-directory", "nudge-band-link"} {
		if _, err := os.Lstat(filepath.Join(dir, name)); err != nil {
			t.Errorf("unrelated entry %s changed: %v", name, err)
		}
	}
	output.Reset()
	installer.report = Report{}
	if err := installer.removeRetiredNudgeState(); err != nil {
		t.Fatal(err)
	}
	if installer.report.Changed != 0 || output.Len() != 0 {
		t.Fatalf("second sweep changed=%d output=%q", installer.report.Changed, output.String())
	}
}

func TestRemoveRetiredNudgeStateMissingDirAndFailedRemoval(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	var output bytes.Buffer
	installer := &engine{options: Options{
		Env: &paths.MapEnv{Values: map[string]string{paths.EnvSIDDir: dir}}, Stdout: &output,
	}, apply: true}
	if err := installer.removeRetiredNudgeState(); err != nil || installer.report.Changed != 0 || output.Len() != 0 {
		t.Fatalf("missing dir: changed=%d err=%v output=%q", installer.report.Changed, err, output.String())
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "nudge-ctx-session")
	if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("permission denied")
	if err := installer.removeRetiredNudgeStateWith(os.ReadDir, func(string) error { return wantErr }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "  warn    "+path) || !strings.Contains(output.String(), wantErr.Error()) {
		t.Fatalf("failed removal warning=%q", output.String())
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("failed removal did not leave file for retry: %v", err)
	}
}

func TestInstallRetiresNudgeStateOnce(t *testing.T) {
	home := t.TempDir()
	sidDir := filepath.Join(home, "sid")
	if err := os.Mkdir(sidDir, 0o700); err != nil {
		t.Fatal(err)
	}
	retired := filepath.Join(sidDir, "nudge-ctx-session")
	effort := filepath.Join(sidDir, paths.SIDEffortPrefix+"session")
	for _, path := range []string{retired, effort} {
		if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sourceRepo := t.TempDir()
	recordFixtureSourceRepo(t, home, sourceRepo)
	configPath := filepath.Join(home, "pfm.config.json")
	writeFixture(t, configPath, `{"version":2}`)
	var output bytes.Buffer
	options := Options{
		Mode: ModeApply, Home: home, SourceRepo: sourceRepo, MCPConfigPath: configPath,
		CodexHomes: []string{}, Runner: &fakeRunner{nameSyncIdle: true},
		Env:    &paths.MapEnv{HomeDir: home, Values: map[string]string{paths.EnvSIDDir: sidDir}},
		Now:    func() time.Time { return time.Date(2031, 2, 3, 4, 5, 6, 0, time.UTC) },
		Stdout: &output,
	}
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatalf("first install: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "  change  remove retired compact-nudge state from "+sidDir) {
		t.Fatalf("install did not report retirement: %s", output.String())
	}
	if _, err := os.Stat(retired); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("retired state remains: %v", err)
	}
	if _, err := os.Stat(effort); err != nil {
		t.Fatalf("effort record lost: %v", err)
	}
	output.Reset()
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatalf("second install: %v\n%s", err, output.String())
	}
	if strings.Contains(output.String(), "remove retired compact-nudge state from ") {
		t.Fatalf("second install repeated retirement: %s", output.String())
	}
}

// `pfm install` without --yes previews: it names the retired state it would
// remove and removes nothing, so a preview never costs the operator a file.
func TestRemoveRetiredNudgeStatePreviewNamesTheChangeAndRemovesNothing(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"nudge-ctx-session", "nudge-band-session"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var output bytes.Buffer
	installer := &engine{options: Options{
		Env: &paths.MapEnv{Values: map[string]string{paths.EnvSIDDir: dir}}, Stdout: &output,
	}, apply: false}
	if err := installer.removeRetiredNudgeState(); err != nil {
		t.Fatal(err)
	}
	planned := "remove retired compact-nudge state from " + dir
	if installer.report.Changed != 1 || !strings.Contains(output.String(), planned) {
		t.Fatalf(
			"preview changed=%d output=%q, want the one planned removal named",
			installer.report.Changed,
			output.String(),
		)
	}
	for _, name := range []string{"nudge-ctx-session", "nudge-band-session"} {
		if _, err := os.Lstat(filepath.Join(dir, name)); err != nil {
			t.Errorf("preview removed %s: %v", name, err)
		}
	}
}
