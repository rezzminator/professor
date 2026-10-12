package update

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/pricing"
	"github.com/rezzminator/professor/pfm/internal/pricing/modelcost"
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
	if err := restoreRefreshedPrices(
		context.Background(),
		repo,
		t.TempDir(),
		&stdout,
	); err != nil ||
		stdout.Len() != 0 {
		t.Fatalf("clean clone: err %v, stdout %q; want nothing done", err, stdout.String())
	}
	if err := os.WriteFile(file, []byte("refreshed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := restoreRefreshedPrices(context.Background(), repo, t.TempDir(), &stdout); err != nil {
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
		t.TempDir(),
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

// An unstaged edit of the clone's prices.json cannot be told from a refresh,
// so it is saved before it is discarded, and the progress line names where.
func TestRestoreRefreshedPricesSavesWhatItDiscardsAndSaysWhere(t *testing.T) {
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
	if err := os.WriteFile(file, []byte("my hand edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	backupDir := filepath.Join(t.TempDir(), "update-backups")

	var stdout bytes.Buffer
	if err := restoreRefreshedPrices(context.Background(), repo, backupDir, &stdout); err != nil {
		t.Fatalf("restore: %v", err)
	}

	saved, err := filepath.Glob(filepath.Join(backupDir, "prices-*.json"))
	if err != nil || len(saved) != 1 {
		t.Fatalf("saved copies = %v, %v; want exactly one under %s", saved, err, backupDir)
	}
	if content, err := os.ReadFile(saved[0]); err != nil || string(content) != "my hand edit\n" {
		t.Fatalf("saved copy %s = %q, %v; want the hand edit", saved[0], content, err)
	}
	if !strings.Contains(stdout.String(), saved[0]) {
		t.Fatalf("progress line %q does not name the saved copy %s", stdout.String(), saved[0])
	}
	if content, err := os.ReadFile(file); err != nil || string(content) != "released\n" {
		t.Fatalf("prices after restore = %q, %v; want the released file", content, err)
	}
}

// The refresh guard (internal/pricing/modelcost) watches the lock pfm update
// holds; a drift of either path would turn the guard off silently.
func TestPriceRefreshGuardWatchesTheLockUpdateHolds(t *testing.T) {
	home := t.TempDir()
	want := filepath.Join(installer.ManagedRoot(home), "update.lock")
	if got := modelcost.UpdateLockPath(home); got != want {
		t.Fatalf("modelcost.UpdateLockPath = %q, want the host update lock %q", got, want)
	}
}

// The saved copies are bounded: after a save only the newest updateBackupsKept
// remain, and what else sits in the directory is left alone.
func TestRestoreRefreshedPricesKeepsOnlyTheNewestSavedCopies(t *testing.T) {
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
	backupDir := t.TempDir()
	var seeded []string
	for day := 1; day <= updateBackupsKept+2; day++ {
		name := filepath.Join(backupDir, fmt.Sprintf("prices-202001%02dT000000.000000000Z.json", day))
		if err := os.WriteFile(name, []byte("old\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		seeded = append(seeded, name)
	}
	unrelated := filepath.Join(backupDir, "notes.txt")
	if err := os.WriteFile(unrelated, []byte("mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Prefix matches are not necessarily backups owned by the updater.
	unowned := []string{"prices-0.json", "prices-19991340T000000.000000000Z.json"}
	for _, name := range unowned {
		if err := os.WriteFile(filepath.Join(backupDir, name), []byte("mine\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	unownedDir := filepath.Join(backupDir, "prices-19990101T000000.000000000Z.json")
	if err := os.Mkdir(unownedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	unownedLink := filepath.Join(backupDir, "prices-19980101T000000.000000000Z.json")
	if err := os.Symlink(unrelated, unownedLink); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("hand edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	if err := restoreRefreshedPrices(context.Background(), repo, backupDir, &stdout); err != nil {
		t.Fatalf("restore: %v", err)
	}

	kept, err := filepath.Glob(filepath.Join(backupDir, "prices-*.json"))
	for _, name := range unowned {
		if content, err := os.ReadFile(filepath.Join(backupDir, name)); err != nil || string(content) != "mine\n" {
			t.Errorf("unrelated prefix match %s = %q, %v; want preserved bytes", name, content, err)
		}
	}
	if info, err := os.Stat(unownedDir); err != nil || !info.IsDir() {
		t.Errorf("timestamp-looking directory was removed: %v", err)
	}
	if target, err := os.Readlink(unownedLink); err != nil || target != unrelated {
		t.Errorf("timestamp-looking symlink changed: target %q, err %v", target, err)
	}
	if err != nil || len(kept) != updateBackupsKept+4 {
		t.Fatalf(
			"matching paths = %d (%v), %v; want %d backups plus 4 unrelated paths",
			len(kept),
			kept,
			err,
			updateBackupsKept,
		)
	}
	if _, err := os.Stat(seeded[0]); err == nil {
		t.Errorf("oldest copy %s survived the bound", seeded[0])
	}
	if _, err := os.Stat(seeded[len(seeded)-1]); err != nil {
		t.Errorf("newest seeded copy was removed: %v", err)
	}
	if !strings.Contains(stdout.String(), kept[len(kept)-1]) {
		t.Errorf("progress line %q does not name the newest saved copy %s", stdout.String(), kept[len(kept)-1])
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Errorf("an unrelated file in the backup directory was removed: %v", err)
	}
}
