package installer

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/codexgen"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
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
	// GlobalAgentsInstalled is the clean state of a Codex agents/ registry:
	// every role is a regular file whose bytes match what this binary
	// compiles. It is spelled apart from "linked" because the two registries
	// hold different shapes — Claude a symlink, Codex a real file — and one
	// word for both would hide exactly the drift this whole check exists for.
	GlobalAgentsInstalled GlobalAgentsState = "installed"
	// GlobalAgentsSymlink: a Codex role is a symlink. Codex opens a role with
	// O_NOFOLLOW and rejects one outright, reporting only "agent type is
	// currently not available", so every spawn of that role fails while the
	// registry looks populated.
	GlobalAgentsSymlink GlobalAgentsState = "SYMLINK"
	// GlobalAgentsMismatch: a Codex role file exists and is readable, but its
	// bytes are not the ones this binary compiles — a stale install, or an
	// edit to the registry copy instead of the source.
	GlobalAgentsMismatch GlobalAgentsState = "MISMATCH"
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
	// Conflicts carries the foreign-link bucket when it coexists with a
	// higher-priority state (DANGLING): Names already names that state's own
	// bucket, so a second bucket would otherwise be computed and discarded
	// rather than rendered.
	Conflicts []string
	Error     string
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
	if len(status.Conflicts) != 0 {
		line += " conflict=" + strings.Join(status.Conflicts, ",")
	}
	if status.Error != "" {
		line += " error=" + status.Error
	}
	switch status.State {
	case GlobalAgentsLinked, GlobalAgentsInstalled:
		return line
	case GlobalAgentsSymlink:
		return line + ` note="Codex refuses a symlinked role and reports only 'agent type is currently not available'"` +
			` hint="run pfm install --yes"`
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
func InspectGlobalAgents(
	home string,
	accounts []pfmconfig.Account,
	claudeAbsent bool,
	codexHomes ...string,
) []GlobalAgentsStatus {
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
	// A declared variant is an agent the install owes every account exactly
	// like an original; its link source is the rendered file in the pfm-owned
	// generated directory. A declaration that cannot be read or rendered is
	// UNREADABLE with its error — never a roster quietly short of variants.
	variants, err := codexgen.LoadGlobalAgentVariants(agentsDir, paths.GeneratedClaudeAgentsDir(home))
	if err != nil {
		return []GlobalAgentsStatus{{Dir: agentsDir, State: GlobalAgentsUnreadable, Error: err.Error()}}
	}
	for _, variant := range variants {
		sources = append(sources, variant.Path)
	}

	statuses := make([]GlobalAgentsStatus, 0, len(accounts)+len(codexHomes)+1)
	for _, account := range accounts {
		statuses = append(statuses, inspectAccountGlobalAgents(account, repo, sources))
	}
	// One row per Codex home, against the bytes this binary compiles. A
	// compile failure is UNREADABLE for the whole check rather than a per-home
	// verdict: with nothing to compare against, no registry can be judged at
	// all, and saying so is the only honest answer.
	roles, err := codexgen.CompileGlobalRoles(home, repo)
	if err != nil {
		return append(statuses, GlobalAgentsStatus{Dir: agentsDir, State: GlobalAgentsUnreadable, Error: err.Error()})
	}
	if len(codexHomes) == 0 {
		codexHomes = []string{filepath.Join(home, ".codex")}
	}
	for _, codexHome := range codexHomes {
		statuses = append(statuses, inspectCodexRoleRegistry(filepath.Join(codexHome, "agents"), roles))
	}
	return statuses
}

// inspectCodexRoleRegistry opens every pfm role in one Codex home's agents/
// registry THE WAY CODEX DOES — O_NOFOLLOW, regular files only — and compares
// what it read against the bytes this binary compiles. Nothing else can
// certify the registry: a doctor that merely stat'ed the path, or that read it
// with os.ReadFile, blesses exactly the symlinks Codex refuses to load. Account
// is 0 and Dir names the registry itself, the same host-wide shape
// GlobalAgentsStatus.Describe already renders for NO-CLONE/NO-SOURCES.
//
// The outcomes are deliberately distinct. A symlink is SYMLINK, never
// "missing": the file is there and Codex still will not spawn it. Content that
// differs from the compiler is MISMATCH, never "installed": the role that
// spawns is not the role this binary ships. A registry that cannot be read is
// UNREADABLE — we failed to look, which is not the same as nothing being there.
func inspectCodexRoleRegistry(registry string, roles []codexgen.GlobalRole) GlobalAgentsStatus {
	status := GlobalAgentsStatus{Dir: registry, State: GlobalAgentsInstalled}
	var symlinked, mismatched, missing, unreadable []string
	for _, role := range roles {
		target := filepath.Join(registry, role.Name+".toml")
		found, err := codexgen.ReadGlobalRoleFile(target)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			missing = append(missing, role.Name)
		case errors.Is(err, codexgen.ErrGlobalRoleNotRegular) && isSymlink(target):
			symlinked = append(symlinked, role.Name)
		case err != nil:
			unreadable = append(unreadable, role.Name)
			if status.Error == "" {
				status.Error = err.Error()
			}
		case !bytes.Equal(found, role.Content):
			mismatched = append(mismatched, role.Name)
		}
	}
	// One state per line, worst first, with every other bucket still named:
	// a registry that is half symlinked and half absent must report both, or
	// the operator fixes one and believes the job done.
	switch {
	case len(unreadable) != 0:
		status.State = GlobalAgentsUnreadable
		status.Names = unreadable
		status.Missing = missing
		status.Conflicts = append(append([]string(nil), symlinked...), mismatched...)
	case len(symlinked) != 0:
		status.State = GlobalAgentsSymlink
		status.Names = symlinked
		status.Missing = missing
		status.Conflicts = mismatched
	case len(mismatched) != 0:
		status.State = GlobalAgentsMismatch
		status.Names = mismatched
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
// agents for at all — neither is a defect. Missing, Unreadable, Symlink and
// Mismatch are a state `pfm install --yes` owns and did not produce, so they
// are FAILURES — Symlink most of all: it is the shape that makes every spawn
// of that role fail while the registry looks full. Conflict, NoSources and
// Unresolved are advisory and stay warnings. The classification and its
// wording live in InspectGlobalAgents / Describe, so the checker can never
// drift from the installer it checks.
func ReportGlobalAgents(
	w io.Writer,
	home string,
	accounts []pfmconfig.Account,
	claudeAbsent bool,
	codexHomes ...string,
) (warnings, failures int) {
	statuses := InspectGlobalAgents(home, accounts, claudeAbsent, codexHomes...)
	for index := range statuses {
		status := &statuses[index]
		fmt.Fprintf(w, "doctor: global-agents %s\n", status.Describe())
		switch status.State {
		case GlobalAgentsLinked, GlobalAgentsInstalled, GlobalAgentsNoClone, GlobalAgentsNoClaude:
		case GlobalAgentsMissing, GlobalAgentsUnreadable, GlobalAgentsSymlink, GlobalAgentsMismatch:
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

// unwireGeneratedCodexAgents removes every pfm-owned Codex global role from
// each configured Codex home's agents/ registry, plus the retired generated
// directory earlier installs used as the role store
// (paths.LegacyGeneratedCodexAgentsDir).
//
// Two ownership proofs, because two layouts exist on real hosts. A REGULAR
// FILE is ours when it carries the generated marker as its first line — the
// proof pfm stamps on every role it writes, and the same one
// retireRenamedCodexAgents tests. A SYMLINK is ours when its TARGET resolves
// inside the retired generated directory or at the even older in-clone twin
// (<blueprint>/templates/global/agents/<name>.toml); this installer wrote both
// shapes, so both are retired here. Anything else — an operator's own role
// file, a link pointing somewhere pfm never wrote — is left untouched.
func (installer *engine) unwireGeneratedCodexAgents() error {
	if err := installer.unwireGeneratedClaudeAgents(); err != nil {
		return err
	}
	generated := paths.LegacyGeneratedCodexAgentsDir(installer.options.Home)
	repo, err := GlobalSourceRepo(installer.options.Home)
	if err != nil {
		return fmt.Errorf("resolve global source repository: %w", err)
	}
	legacyDir := filepath.Join(repo, "templates", "global", "agents")
	for _, codexHome := range installer.codexHomes() {
		registry := filepath.Join(codexHome, "agents")
		if err := installer.retireCodexRoles(registry, generated, legacyDir, nil); err != nil {
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

// retireOrphanCodexRoles removes the pfm-owned role files and pre-migration
// links left in each Codex registry by agents this clone no longer ships. The
// install that wrote them is the install that owes their removal: a role whose
// source is gone still occupies its agent_type, and a stale symlink among them
// is one Codex refuses to load at all.
func (installer *engine) retireOrphanCodexRoles(sourceRepo string, roles []codexgen.GlobalRoleInstalled) error {
	keep := make(map[string]bool, len(roles))
	for _, role := range roles {
		keep[strings.TrimSuffix(filepath.Base(role.Path), ".toml")] = true
	}
	generated := paths.LegacyGeneratedCodexAgentsDir(installer.options.Home)
	legacyDir := filepath.Join(sourceRepo, "templates", "global", "agents")
	for _, codexHome := range installer.codexHomes() {
		if err := installer.retireCodexRoles(
			filepath.Join(codexHome, "agents"), generated, legacyDir, keep,
		); err != nil {
			return err
		}
	}
	return nil
}

// retireCodexRoles removes the pfm-owned roles in one registry that `keep`
// does not name. Uninstall passes a nil roster and takes them all; install
// passes the roster it just compiled, so a role the clone stopped shipping
// (a rename, a retired agent) leaves the registry with it — its file removed
// exactly as its link used to be. A registry that cannot be read is an error,
// never an empty sweep: "we failed to look" must not leave an operator
// believing the retirement happened.
func (installer *engine) retireCodexRoles(registry, generated, legacyDir string, keep map[string]bool) error {
	entries, err := os.ReadDir(registry)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("inspect Codex global agents registry %s: %w", registry, err)
	}
	for _, entry := range entries {
		if keep[strings.TrimSuffix(entry.Name(), ".toml")] {
			continue
		}
		path := filepath.Join(registry, entry.Name())
		info, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect Codex global agent %s: %w", path, err)
		}
		owned := false
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return fmt.Errorf("read Codex global agent link %s: %w", path, err)
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(path), target)
			}
			target = filepath.Clean(target)
			owned = target == generated || strings.HasPrefix(target, generated+string(filepath.Separator))
			owned = owned || target == filepath.Clean(filepath.Join(legacyDir, entry.Name()))
		case info.Mode().IsRegular():
			owned, err = pfmGeneratedCodexRole(path)
			if err != nil {
				return err
			}
		}
		if !owned {
			continue
		}
		if err := installer.retire(path, "retired generated Codex agent"); err != nil {
			return err
		}
	}
	return nil
}

// isSymlink separates the two ways a role path can be unloadable. Codex
// refuses a symlink and a directory alike, but the operator's fix differs —
// a symlink is the migration `pfm install --yes` performs, a directory is
// something only they can explain — so the two never share one verdict line.
func isSymlink(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}

// pfmGeneratedCodexRole reports whether a role file carries pfm's generated
// marker — the one proof that a regular file at a pfm role's name is pfm's own
// and not an operator's. A file that cannot be read is an error, never a quiet
// "not ours": that answer would silently skip a role uninstall owes removal.
func pfmGeneratedCodexRole(path string) (bool, error) {
	content, err := codexgen.ReadGlobalRoleFile(path)
	if err != nil {
		return false, fmt.Errorf("inspect Codex global agent %s: %w", path, err)
	}
	return codexgen.GeneratedGlobalRole(content), nil
}

// unwireGeneratedClaudeAgents removes the pfm-owned directory the rendered
// Claude agent variants live in (paths.GeneratedClaudeAgentsDir). The links
// into it are unwireGlobalRegistry's to retire, which runs first and owns
// them by target; this removes what those links pointed at.
func (installer *engine) unwireGeneratedClaudeAgents() error {
	generated := paths.GeneratedClaudeAgentsDir(installer.options.Home)
	if _, err := os.Lstat(generated); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect generated Claude agents directory %s: %w", generated, err)
	}
	return installer.change("remove "+generated, func() error {
		if err := os.RemoveAll(generated); err != nil {
			return fmt.Errorf("remove generated Claude agents directory %s: %w", generated, err)
		}
		return nil
	})
}
