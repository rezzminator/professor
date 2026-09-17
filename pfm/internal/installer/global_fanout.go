package installer

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"hostops/pfm/internal/codexgen"
	pfmconfig "hostops/pfm/internal/config"
	"hostops/pfm/internal/paths"
)

// This file holds the machine-global wiring that fans out across EVERY
// configured Claude account — skills here, plus the shared source-repo
// resolution and the registry inspection pfm doctor reports per account.
// The rule they all serve: a registry the installer would retire a stale
// link from is a registry it must install into, so a second account can
// never end up with none of the global agents, commands, or skills.

// claudeRegistries renders the <name> registry of every configured Claude
// account for a transcript header. A header naming one directory while the
// step below it writes N is a message that lies about its own work.
func (installer *engine) claudeRegistries(name string) string {
	dirs := installer.claudeConfigDirs()
	rendered := make([]string, 0, len(dirs))
	for _, config := range dirs {
		rendered = append(rendered, filepath.Join(config, name))
	}
	return strings.Join(rendered, ", ")
}

// GlobalSourceRepo resolves the clone every global agent/command/skill link
// is anchored on for a caller that has no installer options at all — pfm
// doctor, checking what a PRIOR install wired. It is the marker-and-default
// half of globalSourceRepoRoot, kept as one implementation so the checker
// can never anchor on a different clone than the installer it checks.
func GlobalSourceRepo(home string) (string, error) {
	marker := SourceRepoPath(home)
	if _, err := os.Lstat(marker); errors.Is(err, fs.ErrNotExist) {
		return filepath.Clean(filepath.Join(home, ".professor")), nil
	} else if err != nil {
		return "", fmt.Errorf("inspect source repository marker %s: %w", marker, err)
	}
	repo, err := ReadSourceRepoMarker(home)
	if err != nil {
		return "", err
	}
	return filepath.Clean(repo), nil
}

// GlobalAgentsState is what one account's agents/ registry holds, measured
// against the agent sources the recorded clone ships. UNREADABLE is its own
// state on purpose: a registry that could not be read is "we failed to
// look", never the "nothing there" of MISSING.
type GlobalAgentsState string

const (
	GlobalAgentsLinked     GlobalAgentsState = "linked"
	GlobalAgentsMissing    GlobalAgentsState = "MISSING"
	GlobalAgentsConflict   GlobalAgentsState = "CONFLICT"
	GlobalAgentsUnreadable GlobalAgentsState = "UNREADABLE"
	GlobalAgentsNoSources  GlobalAgentsState = "NO-SOURCES"
	// GlobalAgentsUnresolved: the clone itself could not be resolved, so no
	// account could be checked against anything.
	GlobalAgentsUnresolved GlobalAgentsState = "UNRESOLVED"
	// GlobalAgentsNoClone: pfm installs without the blueprint clone, so a HOME
	// with no Professor clone recorded AND none at the default path has no
	// global agents to be missing — this is not a warning, but it is also not
	// a clean bill: nothing was checked, and Describe says why.
	GlobalAgentsNoClone GlobalAgentsState = "NO-CLONE"
	// GlobalAgentsNoClaude: the installer never wires an account's agents/
	// registry on a host with no Claude Code binary (ClaudeAbsent), so
	// finding it unlinked there is not a defect either — named, not warned.
	GlobalAgentsNoClaude GlobalAgentsState = "NO-CLAUDE"
	// GlobalAgentsDangling: a ~/.codex/agents/<name>.toml link resolves to
	// the pfm-owned generated directory by name, but nothing is there
	// anymore — codexgen.GlobalLinkDangling surfaced by name, never silently
	// folded into MISSING, because a rerun of `pfm codex agents`/`pfm
	// install` self-heals it by recompiling first.
	GlobalAgentsDangling GlobalAgentsState = "DANGLING"
)

// GlobalAgentsStatus is one reported line's worth of facts: either one
// account's registry (Account/Dir set) or the roster-wide failure that
// stopped every account from being checked at all (Account 0, Dir naming the
// agent source directory).
type GlobalAgentsStatus struct {
	Account int
	Dir     string
	State   GlobalAgentsState
	// Names are the agents in the reported state; Missing carries the
	// not-linked ones when conflicts and absences coexist, so a CONFLICT
	// line never hides the agents the account also lacks.
	Names   []string
	Missing []string
	Error   string
}

// Describe renders the one operator-facing line every caller shows for a
// status, so doctor and any future surface can never word the same drift
// two ways. Every non-linked state carries the remediation.
func (status GlobalAgentsStatus) Describe() string {
	scope := fmt.Sprintf("account=%d dir=%s", status.Account, status.Dir)
	if status.Account == 0 {
		scope = "source=" + status.Dir
	}
	line := fmt.Sprintf("%s state=%s", scope, status.State)
	if len(status.Names) != 0 {
		line += " names=" + strings.Join(status.Names, ",")
	}
	if len(status.Missing) != 0 {
		line += " missing=" + strings.Join(status.Missing, ",")
	}
	if status.Error != "" {
		line += " error=" + status.Error
	}
	switch status.State {
	case GlobalAgentsLinked:
		return line
	case GlobalAgentsNoClone:
		return line + ` note="no Professor clone recorded or at the default path — global agents install from a clone (INSTALL.md § Build from source)"`
	case GlobalAgentsNoClaude:
		return line + ` note="no Claude Code binary installed — the installer never wires this account's agents"`
	default:
		return line + ` hint="run pfm install"`
	}
}

// InspectGlobalAgents classifies, per configured Claude account, whether
// every machine-global agent the recorded clone ships is linked into that
// account's agents/ registry. wireCodexAgents fans those links across every
// account, so an inspection that read the primary alone would certify a host
// whose other accounts have no global agents at all.
//
// An empty source roster is NO-SOURCES, never a clean bill: with nothing to
// enumerate, every account would trivially "have them all". A HOME with no
// source-repo marker AND nothing at the default clone path is NO-CLONE,
// reported by name rather than folded into NO-SOURCES: pfm installs without
// the blueprint clone, so there is nothing here to have gone missing, and the
// status says why nothing was checked instead of quietly certifying a host
// that was never a Professor clone to begin with. Any other Lstat failure on
// either path is UNRESOLVED with its error — a failed look is never absence.
func InspectGlobalAgents(home string, accounts []pfmconfig.Account, claudeAbsent bool) []GlobalAgentsStatus {
	if claudeAbsent {
		statuses := make([]GlobalAgentsStatus, 0, len(accounts))
		for _, account := range accounts {
			statuses = append(
				statuses,
				GlobalAgentsStatus{Account: account.ID, Dir: account.ConfigDir, State: GlobalAgentsNoClaude},
			)
		}
		return statuses
	}
	repo, err := GlobalSourceRepo(home)
	if err != nil {
		return []GlobalAgentsStatus{{Dir: home, State: GlobalAgentsUnresolved, Error: err.Error()}}
	}
	if _, markerErr := os.Lstat(SourceRepoPath(home)); errors.Is(markerErr, fs.ErrNotExist) {
		if _, repoErr := os.Lstat(repo); errors.Is(repoErr, fs.ErrNotExist) {
			return []GlobalAgentsStatus{{Dir: repo, State: GlobalAgentsNoClone}}
		} else if repoErr != nil {
			return []GlobalAgentsStatus{{Dir: repo, State: GlobalAgentsUnresolved, Error: repoErr.Error()}}
		}
	} else if markerErr != nil {
		return []GlobalAgentsStatus{{Dir: repo, State: GlobalAgentsUnresolved, Error: markerErr.Error()}}
	}
	agentsDir := filepath.Join(repo, "templates", "global", "agents")
	// os.ReadDir, not filepath.Glob: Glob drops every I/O error and would
	// render an agents path that cannot be read (not a directory, no
	// permission) as NO-SOURCES — a failed look reported as absence.
	entries, err := os.ReadDir(agentsDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return []GlobalAgentsStatus{{Dir: agentsDir, State: GlobalAgentsUnreadable, Error: err.Error()}}
	}
	var sources []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".md") {
			sources = append(sources, filepath.Join(agentsDir, entry.Name()))
		}
	}
	if len(sources) == 0 {
		return []GlobalAgentsStatus{{Dir: agentsDir, State: GlobalAgentsNoSources}}
	}
	sort.Strings(sources)

	statuses := make([]GlobalAgentsStatus, 0, len(accounts)+1)
	for _, account := range accounts {
		statuses = append(statuses, inspectAccountGlobalAgents(account, repo, sources))
	}
	// The compiled Codex .toml twins are host-wide — one ~/.codex/agents
	// registry, never per-account — so this check runs once, not fanned
	// across accounts the way the .claude/agents/*.md check above is.
	statuses = append(statuses, inspectHostGlobalCodexAgents(home, repo, sources))
	return statuses
}

// inspectHostGlobalCodexAgents classifies every ~/.codex/agents/<name>.toml
// link against the pfm-owned generated directory (paths.GeneratedCodexAgentsDir)
// the compiler writes into. Account is 0 and Dir names the registry itself —
// the same host-wide shape GlobalAgentsStatus.Describe already renders for
// NO-CLONE/NO-SOURCES — because this registry is not scoped to one Claude
// account.
func inspectHostGlobalCodexAgents(home, repo string, sources []string) GlobalAgentsStatus {
	registry := filepath.Join(home, ".codex", "agents")
	generated := paths.GeneratedCodexAgentsDir(home)
	status := GlobalAgentsStatus{Dir: registry, State: GlobalAgentsLinked}
	var conflicting, dangling, missing []string
	for _, source := range sources {
		name := strings.TrimSuffix(filepath.Base(source), ".md")
		target := filepath.Join(registry, name+".toml")
		desired := filepath.Join(generated, name+".toml")
		state, _, err := codexgen.ClassifyGlobalLink(target, desired, repo, codexgen.GlobalLinkFile)
		if err != nil {
			status.State = GlobalAgentsUnreadable
			status.Error = err.Error()
			return status
		}
		switch state {
		case codexgen.GlobalLinkCorrect:
		case codexgen.GlobalLinkConflict:
			conflicting = append(conflicting, name)
		case codexgen.GlobalLinkDangling:
			// An error to look must never render as ABSENCE: the link
			// itself resolves to the right generated path by name, but
			// the compiled file behind it is gone — named DANGLING, never
			// folded into "missing" (a state that would read as never
			// having been installed at all).
			dangling = append(dangling, name)
		default:
			missing = append(missing, name)
		}
	}
	switch {
	case len(dangling) != 0:
		status.State = GlobalAgentsDangling
		status.Names = dangling
		status.Missing = missing
	case len(conflicting) != 0:
		status.State = GlobalAgentsConflict
		status.Names = conflicting
		status.Missing = missing
	case len(missing) != 0:
		status.State = GlobalAgentsMissing
		status.Names = missing
	}
	return status
}

// ReportGlobalAgents reports one line per configured Claude account naming
// whether the machine-global agents the recorded clone ships are linked into
// that account's agents/ registry — the check that would have caught `pfm
// install` wiring the primary account only. Linked, NoClone and NoClaude
// count neither: NoClone means pfm was never given a clone to check agents
// against, and NoClaude means the account has no Claude Code binary to wire
// agents for at all — neither is a defect. Missing and Unreadable are a
// state `pfm install --yes` owns and did not produce, so they are FAILURES;
// Conflict, NoSources and Unresolved are advisory and stay warnings. The
// classification and its wording live in InspectGlobalAgents / Describe, so
// the checker can never drift from the installer it checks.
func ReportGlobalAgents(
	w io.Writer,
	home string,
	accounts []pfmconfig.Account,
	claudeAbsent bool,
) (warnings, failures int) {
	for _, status := range InspectGlobalAgents(home, accounts, claudeAbsent) {
		fmt.Fprintf(w, "doctor: global-agents %s\n", status.Describe())
		switch status.State {
		case GlobalAgentsLinked, GlobalAgentsNoClone, GlobalAgentsNoClaude:
		case GlobalAgentsMissing, GlobalAgentsUnreadable, GlobalAgentsDangling:
			failures++
		default:
			warnings++
		}
	}
	return warnings, failures
}

func inspectAccountGlobalAgents(account pfmconfig.Account, repo string, sources []string) GlobalAgentsStatus {
	status := GlobalAgentsStatus{Account: account.ID, Dir: account.ConfigDir, State: GlobalAgentsLinked}
	registry := filepath.Join(account.ConfigDir, "agents")
	var conflicting, missing []string
	for _, source := range sources {
		name := strings.TrimSuffix(filepath.Base(source), ".md")
		state, _, err := codexgen.ClassifyGlobalLink(
			filepath.Join(registry, filepath.Base(source)), source, repo, codexgen.GlobalLinkFile,
		)
		if err != nil {
			status.State = GlobalAgentsUnreadable
			status.Error = err.Error()
			return status
		}
		switch state {
		case codexgen.GlobalLinkCorrect:
		case codexgen.GlobalLinkConflict:
			conflicting = append(conflicting, name)
		default:
			// Missing, a copy where a link belongs, and a stale in-repo link
			// are three ways of not having this agent — none is what the
			// account should be reading, and one rerun fixes all three.
			missing = append(missing, name)
		}
	}
	switch {
	case len(conflicting) != 0:
		status.State = GlobalAgentsConflict
		status.Names = conflicting
		status.Missing = missing
	case len(missing) != 0:
		status.State = GlobalAgentsMissing
		status.Names = missing
	}
	return status
}

// wireGlobalSkills links every machine-global skill into the skills/
// registry of every configured Claude account: the in-tree workflows/deep-rr
// directory, and each
// skill directory shipped under templates/global/skills/.
// ghostwriter/vision-factory clone management is explicitly out of scope
// here — a different owner entirely.
func (installer *engine) wireGlobalSkills() error {
	sourceRepo, err := installer.globalSourceRepoRoot()
	if err != nil {
		return fmt.Errorf("resolve global skills source repository: %w", err)
	}
	if err := installer.wireGlobalSkill(
		sourceRepo,
		filepath.Join(sourceRepo, "workflows", "deep-rr"),
		"deep-rr",
	); err != nil {
		return err
	}
	return installer.wireTemplateSkills(sourceRepo)
}

// wireTemplateSkills links every top-level DIRECTORY of
// <sourceRepo>/templates/global/skills/ into every configured account's
// skills/ registry: one
// whole-directory link per entry, the same idiom wireGlobalCommands uses for
// its directory entries. The registry file that sits beside them
// (sources.json, naming the skills fetched from their own public repos) is
// not itself a skill and is never linked. An absent or empty source
// directory is reported and never an error — the same carve-out the global
// commands source gets.
func (installer *engine) wireTemplateSkills(sourceRepo string) error {
	source := filepath.Join(sourceRepo, "templates", "global", "skills")
	entries, err := os.ReadDir(source)
	if errors.Is(err, fs.ErrNotExist) {
		installer.skip("global skills source absent at " + source + " (0 entries)")
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect global skills source %s: %w", source, err)
	}
	linked := 0
	for _, entry := range entries {
		path := filepath.Join(source, entry.Name())
		info, statErr := os.Stat(path)
		if statErr != nil {
			return fmt.Errorf("inspect global skill source %s: %w", path, statErr)
		}
		if !info.IsDir() {
			continue
		}
		linked++
		if err := installer.wireGlobalSkill(sourceRepo, path, entry.Name()); err != nil {
			return err
		}
	}
	if linked == 0 {
		installer.skip("global skills source empty at " + source + " (0 skill directories)")
	}
	return nil
}

// wireGlobalSkill links one skill source directory to
// {ConfigDir}/skills/{name} for every configured Claude account. A source
// without a SKILL.md is not a skill
// any engine can load: it is reported as SKILL-SOURCE-MISSING and no link is
// ever created for it.
func (installer *engine) wireGlobalSkill(sourceRepo, source, name string) error {
	if _, err := os.Stat(filepath.Join(source, "SKILL.md")); errors.Is(err, fs.ErrNotExist) {
		installer.skip("SKILL-SOURCE-MISSING " + name + " (" + filepath.Join(source, "SKILL.md") + " absent)")
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect %s skill source: %w", name, err)
	}
	for _, config := range installer.claudeConfigDirs() {
		if err := installer.wireGlobalLink(
			source,
			filepath.Join(config, "skills", name),
			sourceRepo,
			true,
		); err != nil {
			return err
		}
	}
	return nil
}

// unwireGeneratedCodexAgents removes the pfm-owned generated directory the
// compiled Codex global agent .toml twins live in (paths.GeneratedCodexAgentsDir)
// and every ~/.codex/agents/<name>.toml link this installer owns. Ownership
// is decided by the link's TARGET, the same rule retireOrphanGlobalCommands
// holds to: only a symlink resolving INSIDE the generated directory is ours
// to remove — an operator's own agent file, or a link pointing anywhere
// else, is left untouched.
func (installer *engine) unwireGeneratedCodexAgents() error {
	generated := paths.GeneratedCodexAgentsDir(installer.options.Home)
	registry := filepath.Join(installer.options.Home, ".codex", "agents")
	entries, err := os.ReadDir(registry)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("inspect Codex global agents registry %s: %w", registry, err)
	}
	for _, entry := range entries {
		path := filepath.Join(registry, entry.Name())
		info, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect Codex global agent link %s: %w", path, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		target, err := os.Readlink(path)
		if err != nil {
			return fmt.Errorf("read Codex global agent link %s: %w", path, err)
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		target = filepath.Clean(target)
		if target != generated && !strings.HasPrefix(target, generated+string(filepath.Separator)) {
			continue
		}
		if err := installer.retire(path, "retired generated Codex agent link"); err != nil {
			return err
		}
	}
	if _, err := os.Lstat(generated); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect generated Codex agents directory %s: %w", generated, err)
	}
	return installer.change("remove "+generated, func() error {
		if err := os.RemoveAll(generated); err != nil {
			return fmt.Errorf("remove generated Codex agents directory %s: %w", generated, err)
		}
		return nil
	})
}
