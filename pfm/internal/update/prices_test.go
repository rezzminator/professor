package update

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/pricing"
)

func TestRestoreRefreshedPricesPutsOnlyARefreshBackToHead(t *testing.T) {
	repo := newUpdateGitFixture(t)
	file := filepath.Join(repo, filepath.FromSlash(pricing.FileRel))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("released\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTemp(t, repo, "add", pricing.FileRel)
	gitTemp(t, repo, "commit", "-qm", "fixture prices")
	read := func() string {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		return string(content)
	}

	var stdout bytes.Buffer
	if err := restoreRefreshedPrices(context.Background(), repo, &stdout); err != nil || stdout.Len() != 0 {
		t.Fatalf("clean clone: err %v, stdout %q; want nothing done", err, stdout.String())
	}
	if err := os.WriteFile(file, []byte("refreshed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := restoreRefreshedPrices(context.Background(), repo, &stdout); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if read() != "released\n" || !strings.Contains(stdout.String(), "update: restored the refreshed "+pricing.FileRel) {
		t.Fatalf(
			"after restore: file %q, stdout %q; want the released file and one line naming it",
			read(),
			stdout.String(),
		)
	}
	if status, err := updateGitOutput(context.Background(), repo, "status", "--porcelain"); err != nil ||
		strings.TrimSpace(status) != "" {
		t.Fatalf("status after restore = %q, %v; want clean", status, err)
	}

	if err := os.WriteFile(file, []byte("staged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTemp(t, repo, "add", pricing.FileRel)
	stdout.Reset()
	if err := restoreRefreshedPrices(
		context.Background(),
		repo,
		&stdout,
	); err != nil || stdout.Len() != 0 ||
		read() != "staged\n" {
		t.Fatalf(
			"staged edit: err %v, stdout %q, file %q; a staged change is the user's, left for the dirty check",
			err,
			stdout.String(),
			read(),
		)
	}
}

// The baseline doctor runs between the dirty check and the fast-forward, and a
// doctor refreshes prices like any call: a table it rewrote goes back before
// the fast-forward, or a release that changes the table refuses to apply.
func TestUpdateRestoresWhatTheBaselineDoctorRefreshedBeforeTheFastForward(t *testing.T) {
	repo := newUpdateGitFixture(t)
	file := filepath.Join(repo, filepath.FromSlash(pricing.FileRel))
	gitTemp(t, repo, "checkout", "-q", "v0.10.0")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, release := range []string{"v0.11.0", "v0.12.0"} {
		if err := os.WriteFile(file, []byte("released "+release+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitTemp(t, repo, "add", pricing.FileRel)
		gitTemp(t, repo, "commit", "-qm", "fixture prices "+release)
		gitTemp(t, repo, "tag", release)
	}
	gitTemp(t, repo, "push", "-q", "origin", "HEAD:refs/heads/release", "--tags")
	gitTemp(t, repo, "checkout", "-qB", "installed", "v0.11.0")
	runtime := updateTestRuntime(t)
	stubUpdatePipeline(t, runtime)
	updateBaselineDoctor = func(context.Context, pfmconfig.Runtime, bool, io.Writer, io.Writer) (doctorOutcome, error) {
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			return doctorOutcome{}, err
		}
		return doctorOutcome{}, os.WriteFile(file, []byte("refreshed by the baseline doctor\n"), 0o644)
	}

	var stdout, stderr bytes.Buffer
	args := []string{"--skip-harvest", "--to", "v0.12.0", "--repo", repo}
	if code := Run(args, &stdout, &stderr, runtime); code != 0 {
		t.Fatalf("Run() code = %d, want success; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if content, err := os.ReadFile(file); err != nil || string(content) != "released v0.12.0\n" {
		t.Fatalf("prices after update = %q, %v; want the release's table", content, err)
	}
}
