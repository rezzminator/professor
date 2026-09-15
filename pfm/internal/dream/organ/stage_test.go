package organ

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hostops/pfm/internal/dream/artifact"
)

func TestNewStageBuildsPrivateArtifactLayout(t *testing.T) {
	context := validContext(t)
	when := time.Date(2026, time.August, 13, 4, 5, 6, 0, time.UTC)
	layout, err := NewStage(context, "qa-cortex", when)
	if err != nil {
		t.Fatalf("NewStage: %v", err)
	}
	stagingRoot := filepath.Join(context.Organ, "tmp", "staging")
	if !strictDescendant(stagingRoot, layout.Root) {
		t.Fatalf("stage %q is not a strict descendant of %q", layout.Root, stagingRoot)
	}
	if !strings.HasPrefix(filepath.Base(layout.Root), "qa-cortex-20260813T040506.") {
		t.Fatalf("stage name = %q, want lane and UTC stamp", filepath.Base(layout.Root))
	}
	for _, path := range []string{layout.Root, layout.Maps, layout.Meta} {
		assertMode(t, path, 0o700)
	}
	want := map[string]string{
		"Maps":               filepath.Join(layout.Root, "maps"),
		"Meta":               filepath.Join(layout.Root, "meta"),
		"Paths":              filepath.Join(layout.Root, "paths.txt"),
		"Pin":                filepath.Join(layout.Root, "paths.sha256"),
		"Coverage":           filepath.Join(layout.Root, "coverage.md"),
		"Verdicts":           filepath.Join(layout.Root, "verdicts.md"),
		"NormalizedVerdicts": filepath.Join(layout.Root, "verdicts-normalized.tsv"),
		"HumanLog":           filepath.Join(context.Organ, "tmp", "logs", filepath.Base(layout.Root)+".log"),
		"StructuredLog":      filepath.Join(context.Organ, "tmp", "logs", filepath.Base(layout.Root)+".jsonl"),
	}
	actual := map[string]string{
		"Maps": layout.Maps, "Meta": layout.Meta, "Paths": layout.Paths, "Pin": layout.Pin,
		"Coverage": layout.Coverage, "Verdicts": layout.Verdicts,
		"NormalizedVerdicts": layout.NormalizedVerdicts, "HumanLog": layout.HumanLog,
		"StructuredLog": layout.StructuredLog,
	}
	for field, expected := range want {
		if actual[field] != expected {
			t.Errorf("%s = %q, want %q", field, actual[field], expected)
		}
	}
	for _, path := range []string{layout.HumanLog, layout.StructuredLog} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("log exists before corpus is proven non-empty: %s: %v", path, err)
		}
	}

	validated, err := ValidateStage(context, layout.Root)
	if err != nil {
		t.Fatalf("ValidateStage: %v", err)
	}
	if validated != layout {
		t.Fatalf("ValidateStage layout differs:\n got %#v\nwant %#v", validated, layout)
	}
}

func TestCreateLogsUsesExclusivePrivateFiles(t *testing.T) {
	context := validContext(t)
	layout, err := NewStage(context, "explorer", time.Date(2026, 8, 13, 4, 30, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if err := CreateLogs(context, layout.Root); err != nil {
		t.Fatalf("CreateLogs: %v", err)
	}
	for _, path := range []string{layout.HumanLog, layout.StructuredLog} {
		assertMode(t, path, 0o600)
	}
	if err := CreateLogs(context, layout.Root); err == nil || !strings.Contains(err.Error(), "exist") {
		t.Fatalf("CreateLogs(collision) error = %v, want exclusive-create failure", err)
	}
	for _, path := range []string{layout.HumanLog, layout.StructuredLog} {
		assertMode(t, path, 0o600)
	}
}

func TestCreateLogsCleansFirstFileWhenSecondCollides(t *testing.T) {
	context := validContext(t)
	layout, err := NewStage(context, "explorer", time.Date(2026, 8, 13, 4, 45, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	mustMkdir(t, filepath.Dir(layout.StructuredLog), 0o700)
	writeFile(t, layout.StructuredLog, "pre-existing\n", 0o600)
	if err := CreateLogs(context, layout.Root); err == nil || !strings.Contains(err.Error(), "exist") {
		t.Fatalf("CreateLogs(second collision) error = %v, want exclusive-create failure", err)
	}
	if _, err := os.Lstat(layout.HumanLog); !os.IsNotExist(err) {
		t.Fatalf("partial human log survives cleanup: %v", err)
	}
	raw, err := os.ReadFile(layout.StructuredLog)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "pre-existing\n" {
		t.Fatalf("pre-existing structured log changed: %q", raw)
	}
}

func TestValidateStageRejectsOutsideSymlinkWrongModeAndBadLeaves(t *testing.T) {
	context := validContext(t)
	stagingRoot := filepath.Join(context.Organ, "tmp", "staging")
	mustMkdir(t, stagingRoot, 0o700)

	outside := filepath.Join(t.TempDir(), "outside")
	mustMkdir(t, filepath.Join(outside, "maps"), 0o700)
	mustMkdir(t, filepath.Join(outside, "meta"), 0o700)
	if _, err := ValidateStage(context, outside); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("ValidateStage(outside) error = %v", err)
	}

	realStage := filepath.Join(stagingRoot, "real")
	mustMkdir(t, filepath.Join(realStage, "maps"), 0o700)
	mustMkdir(t, filepath.Join(realStage, "meta"), 0o700)
	link := filepath.Join(stagingRoot, "linked")
	if err := os.Symlink(realStage, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateStage(context, link); err == nil || !strings.Contains(err.Error(), "canonical") {
		t.Fatalf("ValidateStage(symlink) error = %v", err)
	}

	if err := os.Chmod(realStage, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateStage(context, realStage); err == nil || !strings.Contains(err.Error(), "0700") {
		t.Fatalf("ValidateStage(mode) error = %v", err)
	}
	if err := os.Chmod(realStage, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(realStage, "maps"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateStage(context, realStage); err == nil || !strings.Contains(err.Error(), "maps") {
		t.Fatalf("ValidateStage(maps mode) error = %v", err)
	}
}

func TestPrivateDirectoryOwnerCheckFailsClosed(t *testing.T) {
	directory := t.TempDir()
	if err := validatePrivateDirectory(
		directory,
		os.Getuid()+1,
	); err == nil ||
		!strings.Contains(err.Error(), "owner") {
		t.Fatalf("validatePrivateDirectory(wrong uid) error = %v", err)
	}
}

func TestRemoveEmptyStageValidatesBeforeRecursiveRemoval(t *testing.T) {
	context := validContext(t)
	layout, err := NewStage(context, "explorer", time.Date(2026, 8, 13, 5, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(layout.Meta, "window.tsv"), "empty\n", 0o600)
	if err := RemoveEmptyStage(context, layout.Root); err != nil {
		t.Fatalf("RemoveEmptyStage: %v", err)
	}
	if _, err := os.Lstat(layout.Root); !os.IsNotExist(err) {
		t.Fatalf("stage survives removal: %v", err)
	}
	for _, path := range []string{layout.HumanLog, layout.StructuredLog} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("empty-window removal left a log artifact %s: %v", path, err)
		}
	}

	outside := filepath.Join(t.TempDir(), "outside")
	mustMkdir(t, filepath.Join(outside, "maps"), 0o700)
	mustMkdir(t, filepath.Join(outside, "meta"), 0o700)
	if err := RemoveEmptyStage(context, outside); err == nil {
		t.Fatal("RemoveEmptyStage accepted an outside directory")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside directory was touched: %v", err)
	}
}

func validContext(t *testing.T) artifact.RepoContext {
	t.Helper()
	repo := newRepository(t)
	registryBase := t.TempDir()
	context, err := Resolve(repo, registryBase)
	if err != nil {
		t.Fatal(err)
	}
	makeSkeleton(t, context)
	mustMkdir(t, context.Registry, 0o700)
	return context
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode(%s) = %04o, want %04o", path, got, want)
	}
}
