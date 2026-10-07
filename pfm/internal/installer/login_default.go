package installer

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
)

// The login default points a Claude started outside pfm (a login shell, a
// systemd user service, a desktop launcher) at the primary account instead of the store.
// It lives in a pfm-named environment.d file and in one fenced block in each
// shell startup file; every write keeps a CLAUDE_CONFIG_DIR already set.
const (
	loginDefaultFenceBegin              = "# BEGIN pfm claude-config-dir — installer-owned"
	loginDefaultFenceEnd                = "# END pfm claude-config-dir — installer-owned"
	loginDefaultEnvFile                 = "pfm-claude-config-dir.conf"
	loginDefaultExplicitConfigDirReason = "a --config-dir install leaves it as it is"
	// loginDefaultUnsafe is every byte the value may not carry: it is written
	// unescaped into environment.d and single-quoted into POSIX sh.
	loginDefaultUnsafe = " \t\r\n\"'\\$`"
)

func loginDefaultEnvironmentPath(home string) string {
	return filepath.Join(home, ".config", "environment.d", loginDefaultEnvFile)
}

// loginDefaultShellFiles: ~/.profile for login shells and display managers,
// ~/.zshenv for every zsh.
func loginDefaultShellFiles(home string) []string {
	return []string{filepath.Join(home, ".profile"), filepath.Join(home, ".zshenv")}
}

// loginDefaultShellBlock sets both variables only where CLAUDE_CONFIG_DIR is
// unset or empty: ~/.zshenv runs in every zsh, the shells of panes pfm
// launched on another account and every tool shell of a running chat included.
func loginDefaultShellBlock(dir string) string {
	sentinel := claudelaunch.ConfigDirDefaultEnv
	return loginDefaultFenceBegin + "\n" +
		`if [ -z "${` + claudeConfigDirEnv + `:-}" ]; then` + "\n" +
		"\t" + claudeConfigDirEnv + "='" + dir + "'\n" +
		"\t" + sentinel + "='" + dir + "'\n" +
		"\texport " + claudeConfigDirEnv + " " + sentinel + "\n" +
		"fi\n" +
		loginDefaultFenceEnd + "\n"
}

// loginDefaultEnvironment is the environment.d file. Its `${VAR:-default}`
// keeps a value an earlier file or the manager already set. environment.d has
// no conditional that sets one variable on another's absence, so the sentinel
// is set unconditionally; claudelaunch.InheritedConfigDir counts it only where
// CLAUDE_CONFIG_DIR equals it, so a preset dir never reads as the default.
func loginDefaultEnvironment(dir string) string {
	return "# pfm claude-config-dir — installer-owned: pfm install writes this file, uninstall removes it.\n" +
		claudeConfigDirEnv + "=${" + claudeConfigDirEnv + ":-" + dir + "}\n" +
		claudelaunch.ConfigDirDefaultEnv + "=" + dir + "\n"
}

// loginDefaultDir is the primary account's configured dir, or "" with the reason the
// login default is not written. A dir pfm would not launch is an error.
func (installer *engine) loginDefaultDir() (string, string, error) {
	if installer.options.ClaudeRosterHost && len(installer.options.ClaudeAccounts) == 0 {
		return "", loginDefaultExplicitConfigDirReason, nil
	}
	if len(installer.options.ClaudeAccounts) == 0 {
		return "", "no Claude account roster", nil
	}
	dir := installer.options.PrimaryConfigDir
	if dir == "" {
		return "", "no primary Claude account", nil
	}
	id := 0
	for _, account := range installer.options.ClaudeAccounts {
		if filepath.Clean(account.ConfigDir) == filepath.Clean(dir) {
			id = account.ID
			break
		}
	}
	if strings.ContainsAny(dir, loginDefaultUnsafe) {
		return "", "", fmt.Errorf(
			"login default: account %d dir %q carries a space, quote, backslash, $ or backtick", id, dir,
		)
	}
	if err := installer.checkLaunchConfigDir(id, dir); err != nil {
		return "", "", fmt.Errorf("login default: %w", err)
	}
	return dir, "", nil
}

// checkLaunchConfigDir applies the refusal every launch applies to dir. A
// preview, which creates nothing, passes a dir install would create first.
func (installer *engine) checkLaunchConfigDir(account int, dir string) error {
	if !installer.apply {
		if _, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
			return nil
		}
	}
	return claudelaunch.CheckConfigDir(account, dir)
}

// wireLoginDefault writes (or, on uninstall, removes) the login default. Each
// file is reported on its own; the failures join into one error.
func (installer *engine) wireLoginDefault(uninstall bool) error {
	dir := ""
	if !uninstall {
		var reason string
		var err error
		dir, reason, err = installer.loginDefaultDir()
		if err != nil {
			return installer.fail(err)
		}
		if reason != "" {
			installer.skip("login default: " + reason)
			if reason == loginDefaultExplicitConfigDirReason {
				return nil
			}
			uninstall = true
		}
	}
	block := ""
	if !uninstall {
		block = loginDefaultShellBlock(dir)
	}
	var failures []error
	for _, path := range loginDefaultShellFiles(installer.options.Home) {
		if err := installer.wireLoginDefaultBlock(path, block); err != nil {
			failures = append(failures, err)
		}
	}
	if err := installer.wireLoginDefaultEnvironment(dir); err != nil {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}

// wireLoginDefaultBlock puts block (empty: none) in path, every other line of
// the file kept; a symlinked startup file is left for its owner to edit.
func (installer *engine) wireLoginDefaultBlock(path, block string) error {
	if info, err := os.Lstat(path); err == nil && info.Mode()&fs.ModeSymlink != 0 {
		target, resolveErr := filepath.EvalSymlinks(path)
		if resolveErr != nil {
			target, resolveErr = os.Readlink(path)
			if resolveErr != nil {
				return installer.loginDefaultFailure(fmt.Errorf("read link %s: %w", path, resolveErr))
			}
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			if block == "" {
				// A link to nothing holds no block to take back.
				if errors.Is(readErr, fs.ErrNotExist) {
					return nil
				}
				return installer.loginDefaultFailure(fmt.Errorf("read %s: %w", path, readErr))
			}
		} else {
			updated, err := spliceLoginDefaultBlock(string(raw), block)
			if err != nil {
				return installer.loginDefaultFailure(fmt.Errorf("%s: %w", path, err))
			}
			if updated == string(raw) {
				if block != "" {
					installer.ok(path + " login default")
				}
				return nil
			}
		}
		message := "login default: " + path + " links to " + target + "; pfm does not write through a link — "
		if block == "" {
			installer.skip(message + "remove the block between \"" + loginDefaultFenceBegin + "\" and \"" +
				loginDefaultFenceEnd + "\" from " + target + " by hand")
		} else {
			installer.skip(message + "add this block to " + target + " by hand:\n" + strings.TrimSuffix(block, "\n"))
		}
		return nil
	}
	raw, err := os.ReadFile(path)
	existed := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return installer.loginDefaultFailure(fmt.Errorf("read %s: %w", path, err))
	}
	updated, err := spliceLoginDefaultBlock(string(raw), block)
	if err != nil {
		return installer.loginDefaultFailure(fmt.Errorf("%s: %w", path, err))
	}
	if updated == string(raw) {
		if block != "" {
			installer.ok(path + " login default")
		}
		return nil
	}
	mode := fs.FileMode(0o644)
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	}
	backup := ""
	if existed && len(raw) > 0 {
		backup = availableBackup(path, installer.stamp)
	}
	err = installer.change(changeDescription(path, existed), func() error {
		if backup != "" {
			if err := copyBackup(path, backup); err != nil {
				return fmt.Errorf("back up %s to %s: %w", path, backup, err)
			}
		}
		if block == "" && updated == "" {
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("remove emptied %s: %w", path, err)
			}
			return nil
		}
		if err := atomicfile.Write(path, []byte(updated), mode); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
		return nil
	})
	if err != nil {
		return installer.loginDefaultFailure(err)
	}
	return nil
}

// wireLoginDefaultEnvironment writes the environment.d file for dir, or
// removes it when dir is empty (uninstall). The file is pfm's whole.
func (installer *engine) wireLoginDefaultEnvironment(dir string) error {
	path := loginDefaultEnvironmentPath(installer.options.Home)
	raw, err := os.ReadFile(path)
	existed := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return installer.loginDefaultFailure(fmt.Errorf("read %s: %w", path, err))
	}
	if dir == "" {
		if !existed {
			return nil
		}
		err = installer.change("remove "+path, func() error {
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("remove %s: %w", path, err)
			}
			return nil
		})
		if err != nil {
			return installer.loginDefaultFailure(err)
		}
		return nil
	}
	wanted := loginDefaultEnvironment(dir)
	if existed && string(raw) == wanted {
		installer.ok(path)
		return nil
	}
	description := "create " + path
	if existed {
		description = "rewrite " + path
	}
	return installer.change(description, func() error {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
		}
		if err := atomicfile.Write(path, []byte(wanted), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
		return nil
	})
}

func (installer *engine) loginDefaultFailure(err error) error {
	return installer.fail(fmt.Errorf("login default: %w", err))
}

// spliceLoginDefaultBlock replaces the fenced block in content with block, in
// place, or appends it when there is none; an empty block removes it. A fence
// that is not exactly one begin line followed by one end line is refused.
func spliceLoginDefaultBlock(content, block string) (string, error) {
	lines := strings.SplitAfter(content, "\n")
	begin, end := -1, -1
	for index, line := range lines {
		switch strings.TrimRight(line, "\r\n") {
		case loginDefaultFenceBegin:
			if begin >= 0 {
				return "", errors.New("two pfm claude-config-dir begin markers; remove one by hand")
			}
			begin = index
		case loginDefaultFenceEnd:
			if begin < 0 {
				return "", errors.New("a pfm claude-config-dir end marker with no begin before it; remove it by hand")
			}
			if end >= 0 {
				return "", errors.New("two pfm claude-config-dir end markers; remove one by hand")
			}
			end = index
		}
	}
	if begin >= 0 && end < 0 {
		return "", errors.New("a pfm claude-config-dir begin marker with no end; remove it by hand")
	}
	if begin < 0 {
		if block == "" {
			return content, nil
		}
		if content != "" && !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		return content + block, nil
	}
	return strings.Join(lines[:begin], "") + block + strings.Join(lines[end+1:], ""), nil
}
