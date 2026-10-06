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

func (installer *engine) retireBBInstall() error {
	installer.say("retired /bb surfaces")
	sourceRepos, err := installer.recordedProfessorSourceRepos()
	if err != nil {
		installer.skip("retired /bb surfaces skipped: " + err.Error() +
			" — rerun pfm install --yes from inside your Professor clone")
		return nil
	}
	for _, link := range []struct {
		target         string
		managedSource  string
		legacyRelative string
	}{
		// frozen historical paths — pre-rename installs' /bb symlinks point here; never rename with the repo
		{
			target:         filepath.Join(installer.options.ConfigDir, "commands", "bb.md"),
			managedSource:  filepath.Join(installer.managedRoot, "bb.command.md"),
			legacyRelative: filepath.Join("blueprint", "templates", "host-swap", "bb.command.md"),
		},
		{
			target:         filepath.Join(installer.options.Home, ".agents", "skills", "bb"),
			managedSource:  filepath.Join(installer.managedRoot, "codex-skills", "bb"),
			legacyRelative: filepath.Join("blueprint", "templates", "host-swap", "codex-skills", "bb"),
		},
	} {
		current, linked := resolvedLink(link.target)
		owned := linked && current == filepath.Clean(link.managedSource)
		for _, repo := range sourceRepos {
			if linked && current == filepath.Join(repo, link.legacyRelative) {
				owned = true
				break
			}
		}
		if !owned {
			installer.skip(link.target + " is not an installed /bb link")
			continue
		}
		if err := installer.retire(link.target, "retired /bb surface"); err != nil {
			return err
		}
	}
	for _, relative := range []string{
		"bb.command.md",
		"codex-skills/bb/SKILL.md",
		"codex-skills/bb/agents/openai.yaml",
	} {
		if err := installer.retire(
			filepath.Join(installer.managedRoot, filepath.FromSlash(relative)),
			"retired /bb surface",
		); err != nil {
			return err
		}
	}
	for _, relative := range []string{"codex-skills/bb/agents", "codex-skills/bb", "codex-skills"} {
		if err := installer.retireEmptyDir(
			filepath.Join(installer.managedRoot, filepath.FromSlash(relative)),
		); err != nil {
			return err
		}
	}
	installer.say("")
	return nil
}

// retireChatCommands retires the 19 /chat:* Claude slash commands and the
// chat.sh/history.sh compatibility scripts they shared a directory with —
// every one of them superseded by the chat MCP tools, with history.sh's
// behavior ported natively into `pfm chat history`. Same discipline as
// retireBBInstall: only a link this installer's own managed root actually
// owns gets unlinked, never a path the operator happens to have there.
func (installer *engine) retireChatCommands() error {
	installer.say("retired /chat: slash commands")
	commands := filepath.Join(installer.options.ConfigDir, "commands")
	for _, link := range []struct {
		target        string
		managedSource string
	}{
		{filepath.Join(commands, "chat", "branch.md"), filepath.Join(installer.managedRoot, "chat", "branch.command.md")},
		{filepath.Join(commands, "chat", "capture.md"), filepath.Join(installer.managedRoot, "chat", "capture.command.md")},
		{filepath.Join(commands, "chat", "find.md"), filepath.Join(installer.managedRoot, "chat", "find.command.md")},
		{filepath.Join(commands, "chat", "goal.md"), filepath.Join(installer.managedRoot, "chat", "goal.command.md")},
		{filepath.Join(commands, "chat", "inject.md"), filepath.Join(installer.managedRoot, "chat", "inject.command.md")},
		{filepath.Join(commands, "chat", "interrogate.md"), filepath.Join(installer.managedRoot, "chat", "interrogate.command.md")},
		{filepath.Join(commands, "chat", "load.md"), filepath.Join(installer.managedRoot, "chat", "load.command.md")},
		{filepath.Join(commands, "chat", "ls.md"), filepath.Join(installer.managedRoot, "chat", "ls.command.md")},
		{filepath.Join(commands, "chat", "new.md"), filepath.Join(installer.managedRoot, "chat", "new.command.md")},
		{filepath.Join(commands, "chat", "read.md"), filepath.Join(installer.managedRoot, "chat", "read.command.md")},
		{filepath.Join(commands, "chat", "save.md"), filepath.Join(installer.managedRoot, "chat", "save.command.md")},
		{filepath.Join(commands, "chat", "whoami.md"), filepath.Join(installer.managedRoot, "chat", "whoami.command.md")},
		{filepath.Join(commands, "chat", "group", "create.md"), filepath.Join(installer.managedRoot, "chat", "group", "create.command.md")},
		{filepath.Join(commands, "chat", "group", "invite.md"), filepath.Join(installer.managedRoot, "chat", "group", "invite.command.md")},
		{filepath.Join(commands, "chat", "group", "ls.md"), filepath.Join(installer.managedRoot, "chat", "group", "ls.command.md")},
		{filepath.Join(commands, "chat", "group", "read.md"), filepath.Join(installer.managedRoot, "chat", "group", "read.command.md")},
		{filepath.Join(commands, "chat", "group", "send.md"), filepath.Join(installer.managedRoot, "chat", "group", "send.command.md")},
		{filepath.Join(commands, "chat", "group", "subscribe.md"), filepath.Join(installer.managedRoot, "chat", "group", "subscribe.command.md")},
		{filepath.Join(commands, "chat", "self", "compact.md"), filepath.Join(installer.managedRoot, "chat", "self", "compact.command.md")},
		{filepath.Join(commands, "chat", "chat.sh"), filepath.Join(installer.managedRoot, "chat", "chat.sh")},
		{filepath.Join(commands, "chat", "history.sh"), filepath.Join(installer.managedRoot, "chat", "history.sh")},
	} {
		current, linked := resolvedLink(link.target)
		if !linked || current != filepath.Clean(link.managedSource) {
			installer.skip(link.target + " is not an installed /chat: link")
			continue
		}
		if err := installer.unlinkOne(link.target); err != nil {
			return err
		}
	}
	for _, relative := range []string{
		"chat/branch.command.md", "chat/capture.command.md", "chat/find.command.md",
		"chat/goal.command.md", "chat/inject.command.md", "chat/interrogate.command.md",
		"chat/load.command.md", "chat/ls.command.md", "chat/new.command.md",
		"chat/read.command.md", "chat/save.command.md", "chat/whoami.command.md",
		"chat/group/create.command.md", "chat/group/invite.command.md", "chat/group/ls.command.md",
		"chat/group/read.command.md", "chat/group/send.command.md", "chat/group/subscribe.command.md",
		"chat/self/compact.command.md", "chat/chat.sh", "chat/history.sh",
	} {
		if err := installer.retire(
			filepath.Join(installer.managedRoot, filepath.FromSlash(relative)),
			"retired /chat: slash command — superseded by the chat MCP tools",
		); err != nil {
			return err
		}
	}
	for _, relative := range []string{"chat/group", "chat/self", chatName} {
		if err := installer.retireEmptyDir(
			filepath.Join(installer.managedRoot, filepath.FromSlash(relative)),
		); err != nil {
			return err
		}
	}
	// The host side is never unconditionally empty the way managedRoot is: an
	// operator's own file, or one of the links above skipped as unowned,
	// legitimately survives here. retireEmptyDir's hard failure on a
	// non-empty directory is right for managedRoot's fully pfm-owned tree; it
	// would wrongly abort every future install for an operator with one
	// leftover file, so the host cleanup is best-effort instead.
	for _, relative := range []string{"chat/group", "chat/self", chatName} {
		if err := installer.retireEmptyDirTolerant(
			filepath.Join(commands, filepath.FromSlash(relative)),
		); err != nil {
			return err
		}
	}
	installer.say("")
	return nil
}

func (installer *engine) retireStagedManagedSurfaces(uninstall bool) error {
	shimDir := filepath.Join(installer.managedRoot, "shim")
	if err := installer.retire(filepath.Join(shimDir, "pfm.zsh"), "retired staged shim"); err != nil {
		return err
	}
	if err := installer.retireEmptyDirTolerant(shimDir); err != nil {
		return err
	}
	return installer.retireStagedHarnessPrompts(uninstall)
}

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
