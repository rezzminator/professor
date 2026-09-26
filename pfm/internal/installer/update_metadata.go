package installer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

const (
	sourceRepoMarkerName = "source-repo"
	binaryOwnershipName  = "binary-ownership.json"
)

// The two answered outcomes of reading the source-repo marker. They are
// distinct because the remedies are opposite: ErrNoSourceRepoMarker means no
// install ever recorded a clone (record one), ErrSourceRepoUnusable means one
// was recorded and no longer names a usable directory (the clone moved, was
// deleted, or the marker was hand-edited). A third outcome — the marker could
// not be read at all — carries NEITHER sentinel: that is "we failed to look",
// and a caller that folds it into either answer is reporting a guess.
var (
	ErrNoSourceRepoMarker = errors.New("no source repository recorded")
	ErrSourceRepoUnusable = errors.New("recorded source repository is unusable")
)

// BinaryOwnership is the small ledger used by pfm update. Paths are absolute
// because update must never infer ownership from PATH order.
type BinaryOwnership struct {
	Paths []string `json:"paths"`
}

func managedRootForHome(home string) string {
	return filepath.Join(home, ".local", "share", "pfm", "install")
}

// SourceRepoPath returns the install-owned clone marker location.
func SourceRepoPath(home string) string {
	return filepath.Join(managedRootForHome(home), sourceRepoMarkerName)
}

// WriteSourceRepoMarker records exactly one normalized clone path.
func WriteSourceRepoMarker(home, repo string) error {
	content, err := sourceRepoMarkerContent(repo)
	if err != nil {
		return err
	}
	return atomicfile.Write(SourceRepoPath(home), content, 0o600)
}

func sourceRepoMarkerContent(repo string) ([]byte, error) {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		return nil, errors.New("source repository path is empty")
	}
	abs, err := filepath.Abs(repo)
	if err != nil {
		return nil, fmt.Errorf("resolve source repository %q: %w", repo, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("inspect source repository %s: %w", abs, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("source repository %s is not a directory", abs)
	}
	return []byte(abs + "\n"), nil
}

// ReadSourceRepoMarker reads the one-line clone marker and verifies that it
// still names a directory. Its three outcomes are told apart by the caller
// with errors.Is: ErrNoSourceRepoMarker (nothing recorded),
// ErrSourceRepoUnusable (recorded, but the path is gone, not a directory, or
// not one path), and a plain wrapped error for a marker that could not be
// read at all. fs.ErrNotExist is kept in the chain of the first so callers
// written against the older single shape keep working.
func ReadSourceRepoMarker(home string) (string, error) {
	path := SourceRepoPath(home)
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("read source repository marker %s: %w: %w", path, ErrNoSourceRepoMarker, err)
	}
	if err != nil {
		return "", fmt.Errorf("read source repository marker %s: %w", path, err)
	}
	repo := strings.TrimSpace(string(raw))
	if repo == "" || strings.ContainsAny(repo, "\r\n") {
		return "", fmt.Errorf("source repository marker %s is not one path: %w", path, ErrSourceRepoUnusable)
	}
	info, err := os.Stat(repo)
	if err != nil {
		return "", fmt.Errorf("inspect recorded source repository %s: %w: %w", repo, ErrSourceRepoUnusable, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("recorded source repository %s is not a directory: %w", repo, ErrSourceRepoUnusable)
	}
	return repo, nil
}

// reportSourceRepoMarker names, never silences, the state of an install run
// with no --repo: the existing marker (kept as is), its absence (a named
// skip — never rendered as if nothing were expected there), a recorded clone
// that no longer resolves (its OWN skip line, naming the recorded path: an
// operator told to "run install from inside your clone" when they already
// did would never learn that the clone moved), or any other read failure
// (returned with context, an error is never absence).
func (installer *engine) reportSourceRepoMarker() error {
	recorded, err := ReadSourceRepoMarker(installer.options.Home)
	switch {
	case err == nil:
		installer.ok(SourceRepoPath(installer.options.Home) + " (kept: " + recorded + ")")
		return nil
	case errors.Is(err, ErrNoSourceRepoMarker):
		installer.skip(
			"source repository not found — run pfm install from inside your Professor clone or set PFM_SOURCE_REPO; pfm init and pfm update read it",
		)
		return nil
	case errors.Is(err, ErrSourceRepoUnusable):
		installer.skip(
			"recorded source repository unusable (" + err.Error() +
				") — rerun pfm install from inside your Professor clone or set PFM_SOURCE_REPO; pfm init and pfm update read it",
		)
		return nil
	default:
		return fmt.Errorf("check source repository marker: %w", err)
	}
}

// armSourceRepoPrePushGate arms the leak gate (core.hooksPath=.githooks) in
// the source clone install just recorded or kept, so `pfm doctor` run from
// that clone — where the update prompt sends every adopter — reports
// pre-push gate=armed instead of exiting 1 with UNWIRED. The product demands
// core.hooksPath in a clone it owns; nothing else in pfm ever sets it, so
// install/update is the only place that can. Only the resolved source clone
// is ever touched, and .githooks/ contents are never modified — this writes
// repo-local git config, nothing else.
func (installer *engine) armSourceRepoPrePushGate(repo string) error {
	hook := filepath.Join(repo, ".githooks", "pre-push")
	hookInfo, err := os.Stat(hook)
	if errors.Is(err, fs.ErrNotExist) {
		installer.skip("pre-push gate not shipped in " + repo + " — nothing to arm")
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect pre-push hook %s: %w", hook, err)
	}
	if !hookInfo.Mode().IsRegular() || hookInfo.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("%s is not an executable regular file — refusing to arm a broken pre-push hook", hook)
	}

	runner := installer.options.Runner
	if runner == nil {
		runner = execCommandRunner{}
	}
	reader, ok := runner.(OutputRunner)
	if !ok {
		return fmt.Errorf("read git repository through installer runner: output seam unavailable")
	}
	// Inside the fence a linked worktree's .git file names a host path the
	// container never mounts, so git is reached through the mounted git
	// directory, exactly as the store reads it (paths.DevRepoGitDir).
	gitDir, fenced := paths.DevRepoGitDir(repo)
	gitArgs := func(args ...string) []string {
		base := []string{"-C", repo}
		if fenced {
			base = append(base, "--git-dir", gitDir, "--work-tree", repo)
		}
		return append(base, args...)
	}
	toplevelBytes, err := reader.Output(context.Background(), "git", gitArgs("rev-parse", "--show-toplevel")...)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			installer.skip("git unavailable — pre-push gate not armed in " + repo)
			return nil
		}
		message := strings.TrimSpace(string(toplevelBytes))
		// git names a non-repository on stderr, which the runner carries in err.
		// Only discovery's own refusal ("not a git repository (or any of the
		// parent directories)" / "(or any parent up to mount point …)") is an
		// absence; "not a git repository: <path>" is a repository git could
		// not reach (a pruned linked worktree, an unmounted fence git dir).
		if strings.Contains(strings.ToLower(message+" "+err.Error()), "not a git repository (or any ") {
			installer.skip(repo + " is not a git repository — pre-push gate not armed")
			return nil
		}
		return fmt.Errorf("resolve %s as a git repository: %w: %s", repo, err, message)
	}

	actualBytes, configErr := reader.Output(
		context.Background(),
		"git",
		gitArgs("config", "--get", "core.hooksPath")...)
	actual := strings.TrimSpace(string(actualBytes))
	if configErr != nil {
		var exitErr interface{ ExitCode() int }
		if !errors.As(configErr, &exitErr) || exitErr.ExitCode() != 1 || actual != "" {
			return fmt.Errorf("read core.hooksPath in %s: %w: %s", repo, configErr, actual)
		}
		actual = ""
	}

	if PrePushGateArmed(repo, actual) {
		installer.ok("pre-push gate armed core.hooksPath=" + actual + " in " + repo)
		return nil
	}

	if fenced {
		// The fence mounts the git directory read-only by law, so arming here
		// could only fail; the host install writes core.hooksPath.
		installer.skip(
			"pre-push gate not armed in " + repo + " — the fence mounts git read-only; the host install arms it",
		)
		return nil
	}
	return installer.change("arm pre-push gate core.hooksPath=.githooks in "+repo, func() error {
		out, err := reader.Output(context.Background(), "git", gitArgs("config", "core.hooksPath", ".githooks")...)
		if err != nil {
			return fmt.Errorf("set core.hooksPath in %s: %w: %s", repo, err, strings.TrimSpace(string(out)))
		}
		return nil
	})
}

// PrePushGateArmed reports whether a recorded core.hooksPath value resolves
// to repository's shipped .githooks directory — a relative spelling
// (git's own default reading) and its absolute equivalent are the same
// armed state. Both the install-time arm step (armSourceRepoPrePushGate,
// above) and `pfm doctor`'s inspectPrePushGateWithRunner compare through this one
// function, so a clone armed with either spelling is recognised the same
// way by both and install never rewrites an already-armed clone.
func PrePushGateArmed(repository, actual string) bool {
	if actual == "" {
		return false
	}
	configured := actual
	if !filepath.IsAbs(configured) {
		configured = filepath.Join(repository, configured)
	}
	return filepath.Clean(configured) == filepath.Join(repository, ".githooks")
}

func binaryOwnershipPath(home string) string {
	return filepath.Join(managedRootForHome(home), binaryOwnershipName)
}

// ReadBinaryOwnership returns the install ledger. A missing ledger means no
// binary is owned; update must not adopt an arbitrary PATH copy.
func ReadBinaryOwnership(home string) (BinaryOwnership, error) {
	raw, err := os.ReadFile(binaryOwnershipPath(home))
	if errors.Is(err, fs.ErrNotExist) {
		return BinaryOwnership{}, nil
	}
	if err != nil {
		return BinaryOwnership{}, fmt.Errorf("read binary ownership ledger: %w", err)
	}
	var ledger BinaryOwnership
	if err := json.Unmarshal(raw, &ledger); err != nil {
		return BinaryOwnership{}, fmt.Errorf("decode binary ownership ledger: %w", err)
	}
	return ledger, nil
}

// RecordCanonicalBinary records only the canonical ~/.local/bin/pfm path.
func RecordCanonicalBinary(home string) error {
	content, err := canonicalBinaryOwnershipContent(home)
	if err != nil {
		return err
	}
	if sameFile(binaryOwnershipPath(home), content, 0o600) {
		return nil
	}
	return atomicfile.Write(binaryOwnershipPath(home), content, 0o600)
}

func canonicalBinaryOwnershipContent(home string) ([]byte, error) {
	path := filepath.Join(home, ".local", "bin", "pfm")
	ledger, err := ReadBinaryOwnership(home)
	if err != nil {
		return nil, err
	}
	found := false
	for _, owned := range ledger.Paths {
		if filepath.Clean(owned) == filepath.Clean(path) {
			found = true
			break
		}
	}
	if !found {
		ledger.Paths = append(ledger.Paths, path)
	}
	encoded, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode binary ownership ledger: %w", err)
	}
	return append(encoded, '\n'), nil
}

func (installer *engine) removeUpdateMetadata() error {
	for _, path := range []string{SourceRepoPath(installer.options.Home), binaryOwnershipPath(installer.options.Home)} {
		if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		if err := installer.change("remove "+path, func() error { return os.Remove(path) }); err != nil {
			return err
		}
	}
	if err := os.Remove(installer.managedRoot); err != nil &&
		!errors.Is(err, fs.ErrNotExist) && !errors.Is(err, syscall.ENOTEMPTY) {
		return err
	}
	return nil
}

func (installer *engine) writeUpdateMetadata() error {
	if installer.options.SourceRepo != "" {
		content, err := sourceRepoMarkerContent(installer.options.SourceRepo)
		if err != nil {
			return err
		}
		path := SourceRepoPath(installer.options.Home)
		if !sameFile(path, content, 0o600) {
			if err := installer.change("write "+path, func() error {
				return WriteSourceRepoMarker(installer.options.Home, installer.options.SourceRepo)
			}); err != nil {
				return err
			}
		} else {
			installer.ok(path)
		}
		abs, err := filepath.Abs(installer.options.SourceRepo)
		if err != nil {
			return fmt.Errorf("resolve source repository %q: %w", installer.options.SourceRepo, err)
		}
		if err := installer.armSourceRepoPrePushGate(filepath.Clean(abs)); err != nil {
			return err
		}
	} else {
		if err := installer.reportSourceRepoMarker(); err != nil {
			return err
		}
		if recorded, err := ReadSourceRepoMarker(installer.options.Home); err == nil {
			if err := installer.armSourceRepoPrePushGate(recorded); err != nil {
				return err
			}
		}
	}
	content, err := canonicalBinaryOwnershipContent(installer.options.Home)
	if err != nil {
		return err
	}
	path := binaryOwnershipPath(installer.options.Home)
	if !sameFile(path, content, 0o600) {
		if err := installer.change("write "+path, func() error {
			return RecordCanonicalBinary(installer.options.Home)
		}); err != nil {
			return err
		}
	} else {
		installer.ok(path)
	}
	return nil
}
