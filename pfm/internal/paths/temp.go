package paths

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const (
	// EnvTmpDir is the POSIX temp-directory variable TempRoot honours.
	EnvTmpDir = "TMPDIR"
	// EnvTmuxTmpDir is tmux's own socket-base variable; tmux ignores TMPDIR.
	EnvTmuxTmpDir = "TMUX_TMPDIR"

	// systemTempDir is pfm's one literal temp path: the host-wide temp
	// directory, TempRoot's default when TMPDIR is unset, and the base tmux
	// (its _PATH_TMP) and the SID rendezvous use whatever TMPDIR says. Every
	// other temp location derives from it or from TMPDIR, here.
	systemTempDir = "/tmp"
	// darwinPrivateRoot is where macOS keeps the real /tmp and /var: both are
	// symlinks into it, so a path recorded on a Mac may name either form.
	darwinPrivateRoot = "/private"
)

// SystemTempDir is the host-wide temp directory, which TMPDIR never moves:
// tmux's socket base and the SID rendezvous every process must agree on.
func SystemTempDir() string { return systemTempDir }

// TempRootFrom is the temp root over an injected Env: TMPDIR, cleaned, else
// the system temp directory — os.TempDir's rule and chat.sh's ${TMPDIR:-/tmp}.
func TempRootFrom(env Env) string {
	if value := env.Get(EnvTmpDir); value != "" {
		return filepath.Clean(value)
	}
	return systemTempDir
}

// TempRoot is TempRootFrom over the live process environment.
func TempRoot() string { return TempRootFrom(OSEnv{}) }

// TempBase is the temp root a run's caches live under: a PFM_HOME jail's own
// tmp, so a jailed run never touches the host's, else TempRoot.
func TempBase(jailHome string) string {
	if jailHome != "" {
		return filepath.Join(jailHome, "tmp")
	}
	return TempRoot()
}

// TempDir is the private (0700) directory dedicated to purpose under the temp
// root: {TempRoot}/pfm-{uid}/{purpose}, created on first use. A caller wanting
// a throwaway directory or file passes it to os.MkdirTemp or os.CreateTemp and
// removes its own entry. The pfm-{uid} level keeps the path short, so a socket
// placed under a purpose stays inside macOS's 104-byte sun_path when TMPDIR is
// a /var/folders directory, and keeps another user's /tmp entries apart.
func TempDir(purpose string) (string, error) {
	return tempDirUnder(TempRoot(), purpose)
}

// MkdirTemp is a fresh throwaway directory, named pattern plus a random
// suffix as os.MkdirTemp names it, inside TempDir(purpose); the caller removes
// it. Either step failing leaves nothing to work in, so both return the error.
func MkdirTemp(purpose, pattern string) (string, error) {
	return mkdirTempUnder(TempRoot(), purpose, pattern)
}

func mkdirTempUnder(root, purpose, pattern string) (string, error) {
	directory, err := tempDirUnder(root, purpose)
	if err != nil {
		return "", err
	}
	return os.MkdirTemp(directory, pattern)
}

func tempDirUnder(root, purpose string) (string, error) {
	if purpose == "" || purpose == "." || purpose == ".." || purpose != filepath.Base(purpose) {
		return "", fmt.Errorf("temp directory purpose %q is not a single path element", purpose)
	}
	owner := filepath.Join(root, "pfm-"+strconv.Itoa(os.Getuid()))
	if err := EnsurePrivateDir("temp directory", owner); err != nil {
		return "", err
	}
	directory := filepath.Join(owner, purpose)
	if err := EnsurePrivateDir("temp directory", directory); err != nil {
		return "", err
	}
	return directory, nil
}

// TempRoots is every spelling of a temp location on this host: the temp root
// and the system temp directory, each as written, physically resolved, and in
// macOS's /private form, deduplicated. A classifier asking "is this path
// scratch" matches against all of them.
func TempRoots() []string { return TempRootsFrom(OSEnv{}) }

// TempRootsFrom is TempRoots over an injected Env.
func TempRootsFrom(env Env) []string {
	var roots []string
	seen := map[string]bool{}
	add := func(path string) {
		if !seen[path] {
			seen[path] = true
			roots = append(roots, path)
		}
	}
	for _, root := range []string{TempRootFrom(env), systemTempDir} {
		if root == "/" {
			// TMPDIR=/ would make every path scratch.
			continue
		}
		for _, form := range []string{root, PhysicalPath(root)} {
			add(form)
			if public, ok := strings.CutPrefix(form, darwinPrivateRoot+"/"); ok {
				add("/" + public)
			} else {
				add(darwinPrivateRoot + form)
			}
		}
	}
	return roots
}

// ErrNotRealDirectory is EnsurePrivateDir's refusal of a path that is a
// symlink or not a directory, so a caller can attach its own remedy.
var ErrNotRealDirectory = errors.New("is not a real directory")

// EnsurePrivateDir is the one door for a private directory: it creates
// directory 0700 if missing and refuses one that is a symlink, not a
// directory, or owned by another user; an existing one of
// ours with looser permissions is tightened to 0700. label names the
// directory's role in every error; an empty label names the path alone.
func EnsurePrivateDir(label, directory string) error {
	if strings.TrimSpace(directory) == "" {
		if label == "" {
			return errors.New("directory path is empty")
		}
		return fmt.Errorf("%s is empty", label)
	}
	subject := directory
	if label != "" {
		subject = label + " " + directory
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", subject, err)
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", subject, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s %w", subject, ErrNotRealDirectory)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("%s has no owner to check", subject)
	}
	if int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("%s is owned by uid %d, not %d", subject, stat.Uid, os.Getuid())
	}
	if info.Mode().Perm() != 0o700 {
		if err := os.Chmod(directory, 0o700); err != nil {
			return fmt.Errorf("secure %s: %w", subject, err)
		}
	}
	return nil
}
