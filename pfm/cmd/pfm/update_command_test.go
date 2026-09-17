package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "hostops/pfm/internal/config"
	"hostops/pfm/internal/installer"
	"hostops/pfm/internal/update"
)

func TestUpdateBuildsSelectedTagIntoOwnedBinaryAndSkipsHarvestProvisioning(t *testing.T) {
	jailTest(t)
	repo := newTaggedBuildFixture(t)
	runtime, err := pfmconfig.LoadRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	canonical := filepath.Join(runtime.Paths.Home, ".local", "bin", "pfm")
	if err := os.WriteFile(canonical, []byte("old\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := installer.RecordCanonicalBinary(runtime.Paths.Home); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "candidate-argv.log")
	t.Setenv("PFM_UPDATE_CANDIDATE_MARKER", marker)
	t.Cleanup(update.StubBaselineDoctorForTest())

	var stdout, stderr bytes.Buffer
	if code := update.Run(
		[]string{"--skip-harvest", "--to", "v0.10.0", "--repo", repo},
		&stdout,
		&stderr,
		runtime,
	); code != 0 {
		t.Fatalf("update.Run() code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "updated v0.10.0") {
		t.Fatalf("update stdout=%q, want selected tag", stdout.String())
	}
	raw, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read candidate action marker: %v", err)
	}
	if !strings.Contains(string(raw), "install") || !strings.Contains(string(raw), "--skip-harvest") {
		t.Fatalf("candidate argv=%q, want install with --skip-harvest", raw)
	}
	version := exec.Command(canonical, "version")
	output, err := version.CombinedOutput()
	if err != nil {
		t.Fatalf("updated binary version: %v output=%q", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "pfm v0.10.0" {
		t.Fatalf("updated binary version=%q, want selected tag v0.10.0", got)
	}
	if got := updateGitBranch(t, repo); got != "installed" {
		t.Fatalf("source branch after update = %q, want installed", got)
	}
	if got := updateGitRevision(t, repo, "HEAD"); got != updateGitRevision(t, repo, "v0.10.0") {
		t.Fatalf("source HEAD after update = %q, want v0.10.0", got)
	}
}

func TestUpdateRunsPostBuildActionsThroughTheSelectedCandidate(t *testing.T) {
	jailTest(t)
	repo := newTaggedBuildFixture(t)
	runtime, err := pfmconfig.LoadRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	canonical := filepath.Join(runtime.Paths.Home, ".local", "bin", "pfm")
	if err := os.WriteFile(canonical, []byte("old\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := installer.RecordCanonicalBinary(runtime.Paths.Home); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "candidate-argv.log")
	t.Setenv("PFM_UPDATE_CANDIDATE_MARKER", marker)
	t.Cleanup(update.StubBaselineDoctorForTest())

	var stdout, stderr bytes.Buffer
	if code := update.Run(
		[]string{"--skip-harvest", "--to", "v0.10.0", "--repo", repo},
		&stdout,
		&stderr,
		runtime,
	); code != 0 {
		t.Fatalf("update.Run() code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	raw, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read candidate action marker: %v", err)
	}
	if !strings.Contains(string(raw), "install") || !strings.Contains(string(raw), "doctor") {
		t.Fatalf("post-build actions ran outside selected candidate; marker=%q", raw)
	}
}

func newTaggedBuildFixture(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitTemp(t, repo, "init", "-q")
	gitTemp(t, repo, "config", "user.email", "fixture.invalid")
	gitTemp(t, repo, "config", "user.name", "fixture-identity")
	mainPath := filepath.Join(repo, "pfm", "cmd", "pfm", "main.go")
	if err := os.MkdirAll(filepath.Dir(mainPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "pfm", "go.mod"), []byte("module fixture.invalid/pfm\n\ngo 1.24\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mainPath, []byte(
		"package main\n\nimport (\n\t\"fmt\"\n\t\"os\"\n)\n\nvar version = \"dev\"\n\nfunc main() {\n\tif marker := os.Getenv(\"PFM_UPDATE_CANDIDATE_MARKER\"); marker != \"\" {\n\t\tfile, err := os.OpenFile(marker, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)\n\t\tif err != nil { panic(err) }\n\t\tfmt.Fprintln(file, os.Args[1:])\n\t\t_ = file.Close()\n\t}\n\tfmt.Println(\"pfm\", version)\n}\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	gitTemp(t, repo, "add", ".")
	gitTemp(t, repo, "commit", "-qm", "fixture previous release")
	gitTemp(t, repo, "tag", "v0.9.0")
	if err := os.WriteFile(filepath.Join(repo, ".e2e-current-source"), []byte("current\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitTemp(t, repo, "add", ".e2e-current-source")
	gitTemp(t, repo, "commit", "-qm", "fixture current release")
	gitTemp(t, repo, "tag", "v0.10.0")
	remote := filepath.Join(t.TempDir(), "remote.git")
	if err := os.MkdirAll(remote, 0o700); err != nil {
		t.Fatal(err)
	}
	gitTemp(t, remote, "init", "--bare", "-q")
	gitTemp(t, repo, "remote", "add", "origin", remote)
	gitTemp(t, repo, "push", "-q", "origin", "HEAD", "--tags")
	gitTemp(t, repo, "checkout", "-qb", "installed", "v0.9.0")
	return repo
}

func gitTemp(t *testing.T, repo string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = repo
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func updateGitBranch(t *testing.T, repo string) string {
	t.Helper()
	command := exec.Command("git", "symbolic-ref", "--quiet", "--short", "HEAD")
	command.Dir = repo
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("resolve fixture branch: %v\n%s", err, output)
	}
	return strings.TrimSpace(string(output))
}

func updateGitRevision(t *testing.T, repo, revision string) string {
	t.Helper()
	command := exec.Command("git", "rev-parse", "--verify", revision)
	command.Dir = repo
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("resolve fixture revision %s: %v\n%s", revision, err, output)
	}
	return strings.TrimSpace(string(output))
}
