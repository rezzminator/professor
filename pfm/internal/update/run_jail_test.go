//go:build jail

package update

// Real git and a real go build are the subject here: cmd/go's VCS stamping
// beneath a git worktree, which no fake reproduces.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// TestBuildUpdateCandidateSurvivesAStrayGitDirectoryAboveTheWorktree pins
// -buildvcs=false. pfm update stages its source as a git worktree, whose .git
// is a FILE cmd/go does not accept as a VCS root, so VCS stamping walks up and
// dies on any .git DIRECTORY above it — an empty $HOME/.git did it live:
// "error obtaining VCS status: exit status 128".
func TestBuildUpdateCandidateSurvivesAStrayGitDirectoryAboveTheWorktree(t *testing.T) {
	t.Cleanup(StubRunnerForTest(obs.Runner(deps.RealRunner{})))
	for _, tool := range []string{"git", "go"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " is not installed")
		}
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(root, "source")
	for name, content := range map[string]string{
		"pfm/go.mod":          "module probe\n\ngo 1.24\n",
		"pfm/cmd/pfm/main.go": "package main\n\nvar version = \"dev\"\n\nfunc main() { println(version) }\n",
	} {
		path := filepath.Join(repo, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git := func(args ...string) {
		t.Helper()
		command := exec.Command(
			"git",
			append(
				[]string{
					"-C",
					repo,
					"-c",
					"user.name=probe",
					"-c",
					"user.email=probe@example.invalid",
					"-c",
					"commit.gpgsign=false",
				},
				args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	git("init", "-q")
	git("add", ".")
	git("commit", "-q", "-m", "probe")
	stage := filepath.Join(root, "stage")
	git("worktree", "add", "--detach", "-q", stage, "HEAD")

	candidate := filepath.Join(root, "pfm-candidate")
	if err := buildUpdateCandidate(context.Background(), stage, "v9.9.9", candidate); err != nil {
		t.Fatalf("candidate build beneath a stray .git directory: %v", err)
	}
	printed, err := exec.Command(candidate).CombinedOutput()
	if err != nil || strings.TrimSpace(string(printed)) != "v9.9.9" {
		t.Fatalf("candidate printed %q, %v; want the stamped v9.9.9", printed, err)
	}
}
