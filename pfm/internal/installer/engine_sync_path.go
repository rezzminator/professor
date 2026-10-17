package installer

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/rezzminator/professor/pfm/internal/enginesync"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// engineSyncPathLink is ~/.local/bin/engine-sync, the PATH name of the
// engine-sync binary: a link to the plugin wrapper's stable link.
func engineSyncPathLink(home string) string {
	return filepath.Join(home, ".local", "bin", "engine-sync")
}

// engineSyncPathLinkIsPfms reports whether link is a symlink to the stable
// path, the only link pfm writes and unwires: the one this environment names,
// or the default one, so a shell whose data-home variables differ from the
// install's still knows pfm's default link.
func engineSyncPathLinkIsPfms(env paths.Env, home string) bool {
	target, linked := resolvedLink(engineSyncPathLink(home))
	return linked && (target == filepath.Clean(enginesync.StablePath(env, home)) ||
		target == filepath.Clean(enginesync.StablePath(&paths.MapEnv{}, home)))
}

// wireEngineSyncPathLink links engine-sync onto PATH when ~/.local/bin/engine-sync
// is absent or already pfm's link. Any other entry there stays as it is (doctor
// warns), and so does an absent stable binary: the plugin wrapper creates it at
// the next Claude session start, and the next install links it then.
func (installer *engine) wireEngineSyncPathLink() error {
	link := engineSyncPathLink(installer.options.Home)
	stable := enginesync.StablePath(installer.env(), installer.options.Home)
	installer.say("engine-sync PATH link -> %s", link)
	defer installer.say("")
	if engineSyncPathLinkIsPfms(installer.env(), installer.options.Home) {
		installer.ok(link)
		return nil
	}
	if _, err := os.Lstat(link); err == nil {
		installer.skip(link + " is not pfm's link — left unchanged")
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("inspect engine-sync PATH link %s: %w", link, err)
	}
	if _, err := os.Lstat(stable); errors.Is(err, fs.ErrNotExist) {
		installer.skip("engine-sync PATH link not written: " + stable + " is absent (install the engine-sync plugin, " +
			"start a Claude session, then rerun pfm install --yes)")
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect engine-sync stable path %s: %w", stable, err)
	}
	return installer.change("link "+link+" -> "+stable, func() error {
		if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
			return err
		}
		return os.Symlink(stable, link)
	})
}

// unwireEngineSyncPathLink removes ~/.local/bin/engine-sync when it is pfm's link.
func (installer *engine) unwireEngineSyncPathLink() error {
	link := engineSyncPathLink(installer.options.Home)
	installer.say("engine-sync PATH link -> %s", link)
	defer installer.say("")
	if !engineSyncPathLinkIsPfms(installer.env(), installer.options.Home) {
		installer.skip(link + " is not pfm's link")
		return nil
	}
	return installer.change("remove "+link, func() error { return os.Remove(link) })
}

// tilde renders a path under home as ~/... for a doctor line.
func tilde(home, path string) string {
	if rel, ok := relativeUnder(home, path); ok {
		return "~/" + rel
	}
	return path
}

// ReportEngineSyncHost prints the doctor lines of the two host files pfm
// writes for engine-sync: the global file (a warning when it is not pfm's)
// and the PATH link (a warning for a foreign entry, a failure for a link that
// resolves to nothing).
func ReportEngineSyncHost(w io.Writer, home string, env paths.Env) (warnings, failures int) {
	configPath := enginesync.GlobalConfigPath(home)
	state, err := engineSyncFileOwnedBy(configPath)
	switch {
	case errors.Is(err, syscall.ENOTDIR):
		// ~/.claude is not a directory: the registry check reports that once.
	case err != nil:
		fmt.Fprintf(w, "doctor: engine-sync global file check failed: %s\n", err)
		failures++
	case state == engineSyncFileUnowned:
		fmt.Fprintf(w, "doctor: %s is not managed by professor — pfm left it unchanged\n", tilde(home, configPath))
		warnings++
	}
	link := engineSyncPathLink(home)
	info, err := os.Lstat(link)
	if errors.Is(err, fs.ErrNotExist) {
		return warnings, failures
	}
	if err != nil {
		fmt.Fprintf(w, "doctor: engine-sync PATH link check failed: %s\n", err)
		return warnings, failures + 1
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, _ := resolvedLink(link)
		if _, err := os.Stat(link); errors.Is(err, fs.ErrNotExist) {
			// install leaves a dangling link as it is: pfm's needs the stable
			// binary back, a foreign one its owner.
			if engineSyncPathLinkIsPfms(env, home) {
				fmt.Fprintf(
					w,
					"doctor: engine-sync PATH link %s -> %s is dangling: the engine-sync binary is gone — %s\n",
					tilde(home, link),
					tilde(home, target),
					engineSyncMissingRepair,
				)
			} else {
				fmt.Fprintf(
					w,
					"doctor: engine-sync PATH link %s -> %s is dangling and not pfm's link — remove or repoint it\n",
					tilde(home, link),
					tilde(home, target),
				)
			}
			return warnings, failures + 1
		} else if err != nil {
			fmt.Fprintf(w, "doctor: engine-sync PATH link check failed: %s\n", err)
			return warnings, failures + 1
		}
	}
	if !engineSyncPathLinkIsPfms(env, home) {
		fmt.Fprintf(w, "doctor: %s is not pfm's link — left unchanged\n", tilde(home, link))
		warnings++
	}
	return warnings, failures
}
