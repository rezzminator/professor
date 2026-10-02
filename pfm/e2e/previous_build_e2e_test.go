//go:build e2e

package e2e

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// previousBuild is the previous-release pfm a background goroutine clones,
// checks out and builds while the test goroutine runs the earlier phases.
type previousBuild struct {
	done chan struct{}
	path string
	err  error
}

// startPreviousBuild starts cloning the previous release and building its pfm
// in the background. The test goroutine makes the temporary directories and
// reads the tag first; the goroutine calls no t method, and a cleanup waits
// for it before those directories are removed.
func (h *e2eHarness) startPreviousBuild() {
	h.t.Helper()
	clone := filepath.Join(h.t.TempDir(), "previous")
	output := filepath.Join(h.t.TempDir(), "pfm-previous")
	tag := strings.TrimSpace(os.Getenv(e2ePreviousTag))
	build := &previousBuild{done: make(chan struct{})}
	h.previous = build
	h.t.Cleanup(func() { <-build.done })
	go func() {
		defer close(build.done)
		build.path, build.err = fetchPreviousBinary(h.repo, clone, output, tag)
	}()
}

// previousBinary waits for the background previous-release build and fails
// the calling test with its setup error.
func (h *e2eHarness) previousBinary() string {
	h.t.Helper()
	if h.previous == nil {
		h.t.Fatal("previous release setup failed; differing paths: background build; status: never started")
	}
	<-h.previous.done
	if h.previous.err != nil {
		h.t.Fatal(h.previous.err)
	}
	return h.previous.path
}

// fetchPreviousBinary clones repo, checks out tag (the newest semantic release
// tag when empty) and builds that pfm to output.
func fetchPreviousBinary(repo, clone, output, tag string) (string, error) {
	if result := runGit(repo, "clone", "--no-local", repo, clone); result.err != nil {
		return "", fmt.Errorf("previous release setup failed; differing paths: local clone; status: %w", result.err)
	}
	if tag == "" {
		result := runGit(clone, "tag", "--list", "v*", "--sort=-v:refname")
		if result.err != nil {
			return "", fmt.Errorf(
				"previous release setup failed; differing paths: release tags; status: %w",
				result.err,
			)
		}
		for _, candidate := range strings.Fields(result.stdout) {
			if isReleaseTag(candidate) {
				tag = candidate
				break
			}
		}
	}
	if tag == "" {
		return "", errors.New(
			"previous release setup failed; differing paths: semantic release tag; status: none found",
		)
	}
	if result := runGit(clone, "checkout", "--detach", "--quiet", tag); result.err != nil {
		return "", fmt.Errorf(
			"previous release setup failed; differing paths: checkout %s; status: %w",
			tag,
			result.err,
		)
	}
	return buildPFM(clone, output)
}

func TestPreviousReleaseSetupFailureKeepsItsMessage(t *testing.T) {
	t.Parallel()
	requireE2EFence(t)
	const prefix = "previous release setup failed; differing paths: "
	notRepo := t.TempDir()
	harness := &e2eHarness{t: t, repo: notRepo}
	harness.startPreviousBuild()
	<-harness.previous.done
	if err := harness.previous.err; err == nil || !strings.HasPrefix(err.Error(), prefix+"local clone; status: ") {
		t.Fatalf("background clone of a non-repository: path=%q err=%v, want the local clone failure",
			harness.previous.path, err)
	}

	repo := filepath.Join(t.TempDir(), "repo")
	runGitFixture(t, t.TempDir(), "init", "--quiet", repo)
	runGitFixture(t, repo, "-c", "user.name=e2e", "-c", "user.email=e2e@example.invalid",
		"commit", "--quiet", "--allow-empty", "-m", "fixture")
	fetch := func(tag string) error {
		root := t.TempDir()
		previous, output := filepath.Join(root, "previous"), filepath.Join(root, "pfm-previous")
		path, err := fetchPreviousBinary(repo, previous, output, tag)
		if err == nil {
			t.Fatalf("previous release from a repository without pfm: path=%q, want an error", path)
		}
		return err
	}
	if err := fetch(""); err.Error() != prefix+"semantic release tag; status: none found" {
		t.Fatalf("no release tag: %v", err)
	}
	if err := fetch("v9.9.9"); !strings.HasPrefix(err.Error(), prefix+"checkout v9.9.9; status: ") {
		t.Fatalf("missing tag: %v", err)
	}
	runGitFixture(t, repo, "tag", "v0.0.1")
	if err := fetch(""); !strings.HasPrefix(err.Error(), "build ") ||
		!strings.Contains(err.Error(), "pfm-previous: go build ./cmd/pfm: ") {
		t.Fatalf("previous build without a pfm module: %v", err)
	}
}
