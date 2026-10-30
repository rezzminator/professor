package installer

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func (installer *engine) retireStagedManagedSurfaces(uninstall bool) error {
	shimDir := filepath.Join(installer.managedRoot, "shim")
	if err := installer.retire(filepath.Join(shimDir, "pfm.zsh"), "retired staged shim"); err != nil {
		return err
	}
	if err := installer.retireEmptyDirTolerant(shimDir); err != nil {
		return err
	}
	for _, name := range retiredVSCodeExtensionFiles {
		target := filepath.Join(installer.managedRoot, filepath.FromSlash(vscodeExtensionSource), name)
		if err := installer.retire(target, "renamed Prompt Lint asset"); err != nil {
			return err
		}
	}
	return installer.retireStagedHarnessPrompts(uninstall)
}

// retiredVSCodeExtensionFiles are the staged extension files the Prompt Lint
// rename replaced (prompt-lint.js, prompt-lint.schema.json); frozen history.
var retiredVSCodeExtensionFiles = []string{"lint.js", "findings.schema.json"}

// stagedHarnessPromptFiles are frozen historical paths: every file a pfm that
// staged harness prompts ever wrote under the legacy directory — the composed
// prompt per engine and each embedded part, re-pinned baselines included.
// Staging has stopped, so the set is closed; never derive it from the embed,
// which moves on with every re-pin.
var stagedHarnessPromptFiles = []string{
	"claude.md", "codex.md", "opencode.md",
	"claude/professor.md", "codex/professor.md", "opencode/professor.md",
	"share/head.md", "share/tail.md",
	"claude/baselines/harness-opus-v2.1.278.md", "claude/baselines/harness-opus-v2.1.280.md",
	"claude/baselines/harness-opus.model", "claude/baselines/harness-opus.sha256",
	"claude/baselines/harness-original-v2.1.278.md", "claude/baselines/harness-original-v2.1.280.md",
	"claude/baselines/harness-original.model", "claude/baselines/harness-original.sha256",
}

// stagedHarnessPromptDirs are the legacy directory's subdirectories, deepest first.
func stagedHarnessPromptDirs() []string {
	claude := pfmengine.MustLookup(pfmengine.Claude).LongName
	return []string{
		claude + "/baselines", claude,
		pfmengine.MustLookup(pfmengine.Codex).LongName, pfmengine.MustLookup(pfmengine.OpenCode).LongName,
		"share",
	}
}

func (installer *engine) retireStagedHarnessPrompts(uninstall bool) error {
	prompts := paths.LegacyHarnessPromptsDir(installer.options.Home)
	if _, err := os.Lstat(prompts); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if !uninstall {
		directories := installer.options.RosterConfigDirs
		if len(directories) == 0 {
			directories = []string{installer.options.ConfigDir}
		}
		keep := false
		for _, directory := range directories {
			pids, err := liveChatPIDs(installer.options.ProcRoot, directory)
			if err != nil {
				installer.skip("keep " + prompts + ": " + err.Error())
				keep = true
				continue
			}
			if len(pids) != 0 {
				installer.skip("keep " + prompts + ": live Claude chats " + strings.Join(pids, ",") +
					" in " + directory + " may still read it — rerun pfm install --yes once they close")
				keep = true
			}
		}
		if keep {
			return nil
		}
	}
	for _, relative := range stagedHarnessPromptFiles {
		if err := installer.retire(
			filepath.Join(prompts, filepath.FromSlash(relative)),
			"retired staged harness prompts",
		); err != nil {
			return err
		}
	}
	directories := append(stagedHarnessPromptDirs(), ".")
	for _, relative := range directories {
		dir := filepath.Join(prompts, filepath.FromSlash(relative))
		if _, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		stray, err := installer.strayEntries(dir)
		if err != nil {
			return err
		}
		if len(stray) != 0 {
			if dir == prompts {
				installer.skip("keep " + prompts + ": holds files pfm did not write: " + strings.Join(stray, ", "))
			}
			continue
		}
		installer.markRemoved(dir)
		if err := installer.change("remove empty "+dir, func() error { return os.Remove(dir) }); err != nil {
			return err
		}
	}
	return nil
}
