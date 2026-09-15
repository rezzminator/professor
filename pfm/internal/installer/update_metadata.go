package installer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"hostops/pfm/internal/atomicfile"
	"hostops/pfm/internal/deps"
)

const (
	sourceRepoMarkerName = "source-repo"
	binaryOwnershipName  = "binary-ownership.json"
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
// still names a directory. A missing marker is a visible update/init error.
func ReadSourceRepoMarker(home string) (string, error) {
	raw, err := os.ReadFile(SourceRepoPath(home))
	if err != nil {
		return "", fmt.Errorf("read source repository marker: %w", err)
	}
	repo := strings.TrimSpace(string(raw))
	if repo == "" || strings.ContainsAny(repo, "\r\n") {
		return "", errors.New("source repository marker is not one path")
	}
	info, err := os.Stat(repo)
	if err != nil {
		return "", fmt.Errorf("inspect recorded source repository %s: %w", repo, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("recorded source repository %s is not a directory", repo)
	}
	return repo, nil
}

// reportSourceRepoMarker names, never silences, the state of an install run
// with no --repo: the existing marker (kept as is), its absence (a named
// skip — never rendered as if nothing were expected there), or any other
// read failure (returned with context, an error is never absence).
func (installer *engine) reportSourceRepoMarker() error {
	recorded, err := ReadSourceRepoMarker(installer.options.Home)
	switch {
	case err == nil:
		installer.ok(SourceRepoPath(installer.options.Home) + " (kept: " + recorded + ")")
		return nil
	case errors.Is(err, fs.ErrNotExist):
		installer.skip(
			"source repository not found — run pfm install from inside your Professor clone or set PFM_SOURCE_REPO; pfm init and pfm update read it",
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

	git := deps.Executable("git")
	toplevelBytes, err := exec.Command(git, "-C", repo, "rev-parse", "--show-toplevel").CombinedOutput()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			installer.skip("git unavailable — pre-push gate not armed in " + repo)
			return nil
		}
		message := strings.TrimSpace(string(toplevelBytes))
		if strings.Contains(strings.ToLower(message), "not a git repository") {
			installer.skip(repo + " is not a git repository — pre-push gate not armed")
			return nil
		}
		return fmt.Errorf("resolve %s as a git repository: %w: %s", repo, err, message)
	}

	actualBytes, configErr := exec.Command(git, "-C", repo, "config", "--get", "core.hooksPath").CombinedOutput()
	actual := strings.TrimSpace(string(actualBytes))
	if configErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(configErr, &exitErr) || exitErr.ExitCode() != 1 || actual != "" {
			return fmt.Errorf("read core.hooksPath in %s: %w: %s", repo, configErr, actual)
		}
		actual = ""
	}

	if PrePushGateArmed(repo, actual) {
		installer.ok("pre-push gate armed core.hooksPath=" + actual + " in " + repo)
		return nil
	}

	return installer.change("arm pre-push gate core.hooksPath=.githooks in "+repo, func() error {
		out, err := exec.Command(git, "-C", repo, "config", "core.hooksPath", ".githooks").CombinedOutput()
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
// above) and `pfm doctor`'s inspectPrePushGate compare through this one
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
