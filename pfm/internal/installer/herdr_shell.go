package installer

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/themesync"
)

const (
	herdrShellOpen  = "# >>> pfm herdr shell >>>\n"
	herdrShellClose = "# <<< pfm herdr shell <<<\n"
	herdrShellAsset = "shim/pfm-herdr-shell.sh"
)

var herdrTerminalHeader = regexp.MustCompile(
	`(?m)^\s*\[\s*(?:terminal|"terminal"|'terminal')\s*\][ \t]*(?:#[^\r\n]*)?(?:\r?\n|$)`,
)

// HerdrShellStatus describes the on-disk key, never an installer's assertion.
type HerdrShellStatus struct {
	Path    string
	Value   string
	Present bool
	Owned   bool
	Wired   bool
}

// InspectHerdrShell reads the setting and proves both marker ownership and an
// executable wrapper. Parse/read failures remain errors, not missing wiring.
func InspectHerdrShell(home string, env paths.Env) (HerdrShellStatus, error) {
	path, err := paths.HerdrConfig(home, env)
	status := HerdrShellStatus{Path: path}
	if err != nil {
		return status, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return status, nil
	}
	if err != nil {
		return status, fmt.Errorf("read Herdr config %s: %w", path, err)
	}
	var config struct {
		Terminal struct {
			DefaultShell string `toml:"default_shell"`
		}
	}
	metadata, err := toml.Decode(string(raw), &config)
	if err != nil {
		return status, fmt.Errorf("parse Herdr config %s: %w", path, err)
	}
	status.Value = config.Terminal.DefaultShell
	status.Present = metadata.IsDefined("terminal", "default_shell")
	block := herdrShellOpen + fmt.Sprintf("default_shell = %q\n", paths.HerdrShellWrapper(home)) + herdrShellClose
	if status.Value != paths.HerdrShellWrapper(home) {
		return status, nil
	}
	if strings.Count(string(raw), block) == 1 {
		// A marker inside a multiline string does not own the actual setting.
		without := strings.Replace(string(raw), block, "", 1)
		remaining, decodeErr := toml.Decode(without, &map[string]any{})
		status.Owned = decodeErr == nil && !remaining.IsDefined("terminal", "default_shell")
	}
	info, err := os.Stat(paths.HerdrShellWrapper(home))
	if errors.Is(err, fs.ErrNotExist) {
		return status, nil
	}
	if err != nil {
		return status, fmt.Errorf("inspect Herdr shell wrapper: %w", err)
	}
	status.Wired = info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
	return status, nil
}

func (installer *engine) wireHerdrShell(uninstall bool) (result error) {
	if !uninstall {
		binary := installer.env().Get(herdr.EnvBin)
		if binary == "" {
			binary = "herdr"
		}
		if _, err := installer.processRunner().LookPath(binary); errors.Is(err, exec.ErrNotFound) && binary == "herdr" {
			installer.skip("herdr shell: herdr absent")
			return nil
		} else if err != nil {
			return fmt.Errorf("resolve Herdr shell wiring binary %s: %w", binary, err)
		}
	}
	home := installer.options.Home
	if installer.apply {
		release, err := themesync.LockState(home)
		if err != nil {
			return fmt.Errorf("lock Herdr shell config: %w", err)
		}
		defer func() { result = errors.Join(result, release()) }()
	}
	status, err := InspectHerdrShell(home, installer.env())
	if err != nil {
		return err
	}
	wrapper := paths.HerdrShellWrapper(home)
	if status.Present && !status.Owned {
		installer.skip(fmt.Sprintf("herdr shell: user-owned default_shell=%q in %s", status.Value, status.Path))
		if !uninstall || status.Value == wrapper {
			return nil
		}
	}
	if uninstall {
		return installer.unwireHerdrShell(status, wrapper)
	}
	content, err := readAsset(herdrShellAsset)
	if err != nil {
		return fmt.Errorf("read Herdr shell wrapper asset: %w", err)
	}
	realShell := installer.herdrRealShell(wrapper)
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'" }
	content = []byte(
		strings.NewReplacer("__PFM_REAL_SHELL__", quote(realShell), "__PFM_WRAPPER__", quote(wrapper)).
			Replace(string(content)),
	)
	if sameFile(wrapper, content, 0o755) {
		installer.ok(wrapper)
	} else if err := installer.change("write "+wrapper, func() error { return atomicfile.Write(wrapper, content, 0o755) }); err != nil {
		return fmt.Errorf("stage Herdr shell wrapper: %w", err)
	}
	if status.Owned {
		installer.ok("herdr shell: wired " + status.Path)
		return nil
	}
	raw, existed, err := readCodexConfig(status.Path)
	if err != nil {
		return fmt.Errorf("read Herdr shell config: %w", err)
	}
	block := herdrShellOpen + fmt.Sprintf("default_shell = %q\n", wrapper) + herdrShellClose
	updated, err := extendHerdrShellConfig(string(raw), block, wrapper)
	if err != nil {
		return fmt.Errorf("extend Herdr terminal table in %s: %w", status.Path, err)
	}
	return installer.change(
		"wire herdr shell in "+status.Path,
		func() error { return installer.writeHerdrShellConfig(status.Path, raw, []byte(updated), existed) },
	)
}

// herdrRealShell discovers the account's shell through the injected runner.
// A tool shell's SHELL is only a validated fallback, never the account truth.
func (installer *engine) herdrRealShell(wrapper string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	shell, err := lookupLoginShell(ctx, installer.processRunner(), os.Getuid())
	if err == nil {
		err = validateHerdrShell(shell, wrapper)
	}
	if err == nil {
		return shell
	}
	lookupErr := fmt.Errorf("herdr shell: login shell lookup: %w", err)
	fallback := installer.env().Get("SHELL")
	if fallbackErr := validateHerdrShell(fallback, wrapper); fallbackErr != nil {
		lookupErr = errors.Join(lookupErr, fmt.Errorf("SHELL fallback: %w", fallbackErr))
		fallback = "/bin/sh"
	}
	message := fmt.Sprintf("%v; using %s", lookupErr, fallback)
	installer.say("  warn    %s", message)
	installer.record("warn", message, lookupErr)
	return fallback
}

func validateHerdrShell(shell, wrapper string) error {
	if !filepath.IsAbs(shell) || strings.ContainsAny(shell, "\x00\r\n") {
		return fmt.Errorf("shell %q is not an absolute executable path", shell)
	}
	if shell == wrapper {
		return fmt.Errorf("shell %q is the Herdr wrapper", shell)
	}
	info, err := os.Stat(shell)
	if err != nil {
		return fmt.Errorf("inspect shell %q: %w", shell, err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("shell %q is not executable", shell)
	}
	if wrapperInfo, statErr := os.Stat(wrapper); statErr == nil {
		if os.SameFile(info, wrapperInfo) {
			return fmt.Errorf("shell %q resolves to the Herdr wrapper", shell)
		}
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return fmt.Errorf("inspect Herdr wrapper %q: %w", wrapper, statErr)
	}
	return nil
}

func loginShellOutput(ctx context.Context, runner deps.Runner, argv ...string) (string, error) {
	result, err := runner.Run(ctx, argv, deps.RunOptions{})
	if err != nil {
		return "", fmt.Errorf("lookup login shell via %q: %w; stderr: %s", argv, err, result.Stderr)
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("lookup login shell via %q: exit %d; stderr: %s", argv, result.ExitCode, result.Stderr)
	}
	return strings.TrimSpace(string(result.Stdout)), nil
}

// extendHerdrShellConfig proves the new key is in the terminal table, not
// merely text inside a multiline TOML string that looks like a table header.
func extendHerdrShellConfig(original, block, wrapper string) (string, error) {
	valid := func(candidate string) bool {
		var config map[string]any
		if _, err := toml.Decode(candidate, &config); err != nil {
			return false
		}
		terminal, ok := config["terminal"].(map[string]any)
		return ok && terminal["default_shell"] == wrapper
	}
	for _, location := range herdrTerminalHeader.FindAllStringIndex(original, -1) {
		prefix := original[:location[1]]
		if !strings.HasSuffix(prefix, "\n") {
			prefix += "\n"
		}
		candidate := prefix + block + original[location[1]:]
		if valid(candidate) {
			return candidate, nil
		}
	}
	updated := original
	if updated != "" && !strings.HasSuffix(updated, "\n") {
		updated += "\n"
	}
	updated += "\n[terminal]\n" + block
	if valid(updated) {
		return updated, nil
	}
	return "", fmt.Errorf("cannot add default_shell without rewriting the existing terminal table")
}

func (installer *engine) writeHerdrShellConfig(path string, original, updated []byte, existed bool) error {
	mode := fs.FileMode(0o600)
	if existed {
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("inspect Herdr config %s before write: %w", path, err)
		}
		mode = info.Mode().Perm()
	}
	if err := installer.writeRegistryFileIf(path, original, updated, existed, mode, nil); err != nil {
		return fmt.Errorf("save Herdr config %s: %w", path, err)
	}
	return nil
}

func (installer *engine) unwireHerdrShell(status HerdrShellStatus, wrapper string) error {
	if status.Owned {
		raw, err := os.ReadFile(status.Path)
		if err != nil {
			return fmt.Errorf("read owned Herdr shell config: %w", err)
		}
		block := herdrShellOpen + fmt.Sprintf("default_shell = %q\n", wrapper) + herdrShellClose
		updated := strings.Replace(string(raw), block, "", 1)
		if err := installer.change(
			"unwire herdr shell in "+status.Path,
			func() error { return installer.writeHerdrShellConfig(status.Path, raw, []byte(updated), true) },
		); err != nil {
			return err
		}
	}
	if _, err := os.Lstat(wrapper); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect Herdr shell wrapper for removal: %w", err)
	}
	return installer.change("remove "+wrapper, func() error { return os.Remove(wrapper) })
}
