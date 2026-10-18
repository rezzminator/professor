package update

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/professor"
)

// retiredMatch is how far past a retired name a reference may run.
type retiredMatch int

const (
	// matchExact: the name alone — `/pfm:refresh`, never `/pfm:refreshed` or
	// `/pfm:refresh:x`.
	matchExact retiredMatch = iota
	// matchFamily: the name alone or as a namespace — `/wave` and
	// `/wave:refine`, never `/waveform`.
	matchFamily
	// matchPrefix: any text the name starts — `mcp__chat__chat_inject`.
	matchPrefix
)

// The kinds a retired name is reported as.
const (
	retiredCommand = "command"
	retiredAgent   = "agent"
	retiredSkill   = "skill"
	retiredPhase   = "phase"
	retiredTool    = "tool"
	retiredPath    = "path"
)

// retiredName is one name the framework retired that a project file may
// still reference: a command, agent, skill, gitter phase, MCP tool or
// project-tier path.
type retiredName struct {
	name      string
	kind      string
	successor string // "" when nothing replaced it
	match     retiredMatch
	// probe is the root-relative file whose existence means the project defines
	// the thing itself, for a name that is a context around the thing rather
	// than the thing (`subagent_type: "ultimate"`); "" derives it from name.
	probe string
}

// RetiredPfmCommand answers the dispatch of a retired `pfm {command}`: the
// retired name and the successor the retired list gives it, read from
// retiredNames (the one table). ok is false for a command the list does not
// retire as a command with a successor.
func RetiredPfmCommand(command string) (name, successor string, ok bool) {
	name = "pfm " + command
	for _, retired := range retiredNames {
		if retired.kind == retiredCommand && retired.name == name && retired.successor != "" {
			return retired.name, retired.successor, true
		}
	}
	return "", "", false
}

// chatlogSuccessor is what replaced the /tokens command and the transcript skill.
const chatlogSuccessor = "the chatlog skill"

// imagineSuccessor is what replaced /imagine:build, /imagine:build-fast,
// /imagine:callmeter, /ultimate-spec and the ultimate visualization skill.
const imagineSuccessor = "/imagine"

// retiredNames is the one registry of retired names `pfm doctor
// --project-updates` scans a project's files for. Every entry cites
// the release note or commit that retired it. A retired name that is also a
// live one, or an everyday word or route, is left out with its reason: a
// scan that false-hits trains the reader to skip it.
//
// Left out: bare `/pfm` (live again as the machine-global pfm CLI guide,
// releases/v0.78.0.md "Project: /pfm becomes /pcm" — the retired change
// manager is matched by its project file instead); `/pm` and `/git`
// (releases/v0.78.0.md "Project: retired project surface") and the
// `developer`, `qa`, `architect` and `scheduler` agents (releases/v0.78.0.md
// "Global: flights replaces wave", "Project: flights cast replaces
// per-project developer and qa"), all ordinary words, paths or routes.
var retiredNames = []retiredName{
	// chat-call: split family registrations and rename chat delivery.
	{
		name:      "mcp__professor__",
		kind:      retiredTool,
		successor: "mcp__chat-professor__* / mcp__harvester-professor__*",
		match:     matchPrefix,
	},
	{name: "pfm chat inject", kind: retiredCommand, successor: "pfm chat call", match: matchExact},
	{name: "chat_inject", kind: retiredTool, successor: "mcp__chat-professor__call", match: matchExact},
	// releases/v0.78.0.md "Global: flights replaces wave": every /wave:*
	// command and the wave-builder skill are removed; new work starts with
	// /flights:spec. The bare /wave pair retired earlier (releases/v0.43.0.md).
	{name: "/wave", kind: retiredCommand, successor: "/flights:spec", match: matchFamily},
	{name: "wave-builder", kind: retiredSkill, successor: "/flights:spec", match: matchExact},
	// releases/v0.77.0.md "B: templates — /jc and /qa:live are gone"; commit
	// e7c8b6a2 "retire /jc, /qa:live and /pfm:refresh". /jc:wave was its
	// namespace (releases/v0.32.0.md).
	{name: "/jc", kind: retiredCommand, match: matchFamily},
	{name: "/qa:live", kind: retiredCommand, match: matchExact},
	// Same note: "gitter renames JC-COMMIT to COMMIT"; "/documenter's
	// JC-UPDATE mode is renamed FIX-UPDATE", and /documenter itself retired
	// in releases/v0.78.0.md.
	{name: "JC-COMMIT", kind: retiredPhase, successor: "COMMIT", match: matchExact},
	{name: "JC-UPDATE", kind: retiredPhase, match: matchExact},
	// releases/v0.77.0.md "C: pfm — /pfm:refresh is gone as a command":
	// /pfm:release --from is its only caller (the live form per CLAUDE.md
	// § Vocabulary "refresh").
	{
		name:      "/pfm:refresh",
		kind:      retiredCommand,
		successor: "/pfm:release prepare --from {live-project}",
		match:     matchExact,
	},
	// releases/v0.77.0.md "/quality:md-forlint (renamed from /quality:forlint)".
	{name: "/quality:forlint", kind: retiredCommand, successor: "/quality:md-forlint", match: matchExact},
	// releases/v0.78.0.md "Project: /pfm becomes /pcm": commands/pfm.md is
	// renamed commands/pcm.md with its audit scopes inline.
	{name: ".claude/commands/pfm.md", kind: retiredPath, successor: ".claude/commands/pcm.md", match: matchExact},
	{
		name: ".claude/commands/pfm/references/audit-scopes.md", kind: retiredPath,
		successor: ".claude/commands/pcm.md", match: matchExact,
	},
	// releases/v0.78.0.md "→ For: every adopter · per project · MCP tool
	// names": mcp__chat__* and mcp__harvester__* become their
	// mcp__professor__ forms.
	{name: "mcp__chat__", kind: retiredTool, successor: "mcp__chat-professor__*", match: matchPrefix},
	{name: "mcp__harvester__", kind: retiredTool, successor: "mcp__harvester-professor__*", match: matchPrefix},
	// releases/v0.62.0.md "Removed: the host-level /chat:* Claude
	// slash-command family"; "drive chat through the MCP tools instead".
	{name: "/chat:", kind: retiredCommand, successor: "mcp__chat-professor__*", match: matchPrefix},
	// releases/v0.78.0.md "Project: retired project surface": /documenter and
	// /documenter:archive ("Doc consolidation is the main session's work
	// under /quality:doc"), /km, /audit:ai-output and six agents.
	{name: "/documenter", kind: retiredCommand, successor: "/quality:doc", match: matchFamily},
	{name: "/km", kind: retiredCommand, match: matchExact},
	{name: "/audit:ai-output", kind: retiredCommand, match: matchExact},
	// Commit "remove /rnd end to end": the project-scope research command and
	// its former /rnd:hammer and /rnd:referee sub-commands have no successor.
	{name: "/rnd", kind: retiredCommand, match: matchFamily},
	{name: "mono-architect", kind: retiredAgent, match: matchExact},
	{name: "mono-documenter", kind: retiredAgent, match: matchExact},
	{name: "mono-planner", kind: retiredAgent, match: matchExact},
	{name: "rndier", kind: retiredAgent, match: matchExact},
	{name: "role-wrapper", kind: retiredAgent, match: matchExact},
	{name: "qa-wrapper", kind: retiredAgent, match: matchExact},
	// releases/v0.78.0.md "Global: rr family": "rr-super is retired"; its
	// stronger variant is super-rr.
	{name: "rr-super", kind: retiredAgent, successor: "super-rr", match: matchExact},
	// Commit 4bb529c7 "one tracer — the tracer-pro and tracer-pro-max tiers
	// are gone". Two rows: a match followed by `-` is a longer name, so
	// tracer-pro never hits tracer-pro-max.
	{name: "tracer-pro", kind: retiredAgent, successor: "tracer", match: matchExact},
	{name: "tracer-pro-max", kind: retiredAgent, successor: "tracer", match: matchExact},
	// Commit f0c88ffb "tracer-rr is clone-distiller".
	{name: "tracer-rr", kind: retiredAgent, successor: "clone-distiller", match: matchExact},
	// Commit 3b401f24: the twin agents take their originals' full names; terra retires.
	{name: "gpt-foreman", kind: retiredAgent, successor: "gpt-flights-foreman", match: matchExact},
	{name: "gpt-lander", kind: retiredAgent, successor: "gpt-flights-lander", match: matchExact},
	{name: "gpt-executor", kind: retiredAgent, successor: "gpt-flights-mechanical-executor", match: matchExact},
	retiredAgentRow("terra", "", `subagent_type: "terra"`),
	retiredAgentRow("terra", "", `subagent_type:"terra"`),
	retiredAgentRow("terra", "", `subagent_type: 'terra'`),
	retiredAgentRow("terra", "", "subagent_type: terra"),
	retiredAgentRow("terra", "", `subagent_type="terra"`),
	retiredAgentRow("terra", "", `subagent_type": "terra"`),
	retiredAgentRow("terra", "", `subagent_type":"terra"`),
	retiredAgentRow("terra", "", "agents/terra.md"),
	retiredAgentRow("terra", "", "`terra` agent"),
	retiredAgentRow("terra", "", "`terra` sub-agent"),
	retiredAgentRow("terra", "", "`terra` subagent"),
	// Commit 73f2ae2b "/reminder becomes /pfm:schedule".
	{name: "pfm chat reminder", kind: retiredCommand, successor: "pfm chat schedule", match: matchExact},
	{name: "build-codex.mjs", kind: retiredPath, successor: "engine-sync codex build|check", match: matchExact},
	{name: "scripts/codex-sync.sh", kind: retiredPath, successor: "engine-sync codex", match: matchExact},
	{name: "/reminder", kind: retiredCommand, successor: "/pfm:schedule", match: matchFamily},
	// Commit b9f530c9 "/tokens and the transcript skill become one skill":
	// chatlog, over `pfm tokens` and `pfm transcript` (both live). The
	// transcript skill is named by its path or as "transcript skill" only —
	// the bare word is everyday prose.
	{name: "/tokens", kind: retiredCommand, successor: chatlogSuccessor, match: matchFamily},
	{name: "skills/transcript", kind: retiredSkill, successor: chatlogSuccessor, match: matchExact},
	{name: "transcript skill", kind: retiredSkill, successor: chatlogSuccessor, match: matchExact},
	// Commit 8bde61ab "/close-out is /done-herewith"; the skill by its path
	// too, the bare word "close-out" being prose.
	{name: "/close-out", kind: retiredCommand, successor: "/done-herewith", match: matchFamily},
	{name: "skills/close-out", kind: retiredSkill, successor: "/done-herewith", match: matchExact},
	// `pfm codex|opencode` retire for `engine-sync codex|opencode`, which
	// `pfm install` puts on PATH. The same rows answer the retired dispatch
	// itself (RetiredPfmCommand).
	{name: "pfm codex", kind: retiredCommand, successor: "engine-sync codex", match: matchFamily},
	{name: "pfm opencode", kind: retiredCommand, successor: "engine-sync opencode", match: matchFamily},
	// Commit abbcf405 "`pfm price` is gone": `pfm model-cost` is the one
	// price command (a following `s` or `-` is another name, never a hit).
	{name: "pfm price", kind: retiredCommand, successor: "pfm model-cost", match: matchExact},
	// Commit c867316c "retire docs/agents": the docs hub scaffolds to docs/.
	{
		name: "docs/agents", kind: retiredPath, successor: "docs/_index.md and docs/standards.md",
		match: matchExact,
	},
	// The PreToolUse guard hook is removed with its stamp script: framework
	// files are edited through /pcm, which dispatches the pcm agent, and no
	// hook gates the edit. A settings.json hook entry or a script still
	// naming either one is dead wiring.
	{name: "pfm-guard.sh", kind: retiredPath, match: matchExact},
	{name: "guard-stamp.sh", kind: retiredPath, match: matchExact},
	// Commit 09d16b64 "the ultimate family becomes /imagine:build, build-spec,
	// build-fast, evolve and callmeter" retired /ultimate-spec and the ultimate
	// agent, and callmeter commit 156f558 "remove ultimate-visualization; it
	// moved to professor as /imagine:callmeter" its skill. /ultimate itself is
	// live again as a standalone skill, so it is no row and no agent row below
	// matches a reference to that skill (`/ultimate`, skills/ultimate/).
	// Commit 471d4b63 "the imagine family collapses to /imagine, the imagination
	// agent and the ultimate skill" retired every /imagine: subcommand, the
	// imagine agent and the imagine-core skill (its law lives in the imagination
	// agent); /imagine itself is live, so it is no row. A spec without a build
	// starts with /flights:spec. Commit 196ec68b "retire /imagine:evolve with its
	// imagine-historian agent" left both with no successor.
	{name: "/ultimate-spec", kind: retiredCommand, successor: imagineSuccessor, match: matchExact},
	{
		name: "/callmeter:ultimate-visualization", kind: retiredSkill, successor: imagineSuccessor,
		match: matchExact,
	},
	{name: "/imagine:build", kind: retiredCommand, successor: imagineSuccessor, match: matchExact},
	{name: "/imagine:build-fast", kind: retiredCommand, successor: imagineSuccessor, match: matchExact},
	{name: "/imagine:build-spec", kind: retiredCommand, successor: "/flights:spec", match: matchExact},
	{name: "/imagine:callmeter", kind: retiredCommand, successor: imagineSuccessor, match: matchExact},
	{name: "imagine-core", kind: retiredSkill, successor: "the imagination agent", match: matchExact},
	{name: "/imagine:evolve", kind: retiredCommand, match: matchExact},
	{name: "imagine-historian", kind: retiredAgent, match: matchExact},
	// The agent is named by its context only — the bare word is everyday prose.
	// One row per spelling an agent reference takes: the Agent tool's
	// subagent_type in YAML, call, keyword and JSON form, the agent file's path,
	// and the backticked name beside the word agent or sub-agent.
	retiredAgentRow("ultimate", "imagination", `subagent_type: "ultimate"`),
	retiredAgentRow("ultimate", "imagination", `subagent_type:"ultimate"`),
	retiredAgentRow("ultimate", "imagination", `subagent_type: 'ultimate'`),
	retiredAgentRow("ultimate", "imagination", "subagent_type: ultimate"),
	retiredAgentRow("ultimate", "imagination", `subagent_type="ultimate"`),
	retiredAgentRow("ultimate", "imagination", `subagent_type": "ultimate"`),
	retiredAgentRow("ultimate", "imagination", `subagent_type":"ultimate"`),
	retiredAgentRow("ultimate", "imagination", "agents/ultimate.md"),
	retiredAgentRow("ultimate", "imagination", "`ultimate` agent"),
	retiredAgentRow("ultimate", "imagination", "`ultimate` sub-agent"),
	retiredAgentRow("ultimate", "imagination", "`ultimate` subagent"),
	// The imagine agent, named by its context for the same reason: imagination,
	// /imagine and imagine-core are other names and never reach these rows.
	retiredAgentRow("imagine", "imagination", `subagent_type: "imagine"`),
	retiredAgentRow("imagine", "imagination", `subagent_type:"imagine"`),
	retiredAgentRow("imagine", "imagination", `subagent_type: 'imagine'`),
	retiredAgentRow("imagine", "imagination", "subagent_type: imagine"),
	retiredAgentRow("imagine", "imagination", `subagent_type="imagine"`),
	retiredAgentRow("imagine", "imagination", `subagent_type": "imagine"`),
	retiredAgentRow("imagine", "imagination", `subagent_type":"imagine"`),
	retiredAgentRow("imagine", "imagination", "agents/imagine.md"),
	retiredAgentRow("imagine", "imagination", "`imagine` agent"),
	retiredAgentRow("imagine", "imagination", "`imagine` sub-agent"),
	retiredAgentRow("imagine", "imagination", "`imagine` subagent"),
}

// retiredAgentRow is one contextual spelling of a retired agent reference.
// A project keeping its own agent definition is not naming the retired one.
func retiredAgentRow(name, successor, spelling string) retiredName {
	return retiredName{
		name: spelling, kind: retiredAgent, successor: successor, match: matchExact,
		probe: ".claude/agents/" + name + ".md",
	}
}

// definitionProbes are the root-relative paths whose existence means the
// project itself still defines the retired name: its own command, agent or
// skill file. Retired paths, phases, MCP tools and commands spelled with a
// space are defined by no project file; a legacy path cannot exempt itself.
func (r retiredName) definitionProbes() []string {
	if r.probe != "" {
		return []string{r.probe}
	}
	switch r.kind {
	case retiredCommand:
		command := strings.ReplaceAll(strings.TrimSuffix(strings.TrimPrefix(r.name, "/"), ":"), ":", "/")
		if command == "" || strings.ContainsAny(command, " ") {
			return nil
		}
		return []string{".claude/commands/" + command + ".md", ".claude/commands/" + command}
	case retiredAgent:
		return []string{".claude/agents/" + r.name + ".md"}
	case retiredSkill:
		return []string{".claude/skills/" + strings.TrimSuffix(strings.TrimPrefix(r.name, "skills/"), " skill")}
	}
	return nil
}

// dropDefinedNames removes the hits on a retired name the project defines
// itself: a project that keeps its own /km command is not naming a retired
// one. A probe that cannot be statted for any reason but absence leaves the
// name flagged.
func dropDefinedNames(root string, hits []professor.RetiredNameHit) []professor.RetiredNameHit {
	defined := map[string]bool{}
	for _, retired := range retiredNames {
		for _, probe := range retired.definitionProbes() {
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(probe))); err == nil {
				defined[retired.name] = true
				break
			}
		}
	}
	if len(defined) == 0 {
		return hits
	}
	kept := make([]professor.RetiredNameHit, 0, len(hits))
	for _, hit := range hits {
		if !defined[hit.Name] {
			kept = append(kept, hit)
		}
	}
	return kept
}

// retiredSkippedDirs are directory names the scan never enters at any depth:
// git's store, the flights' nested worktrees (other checkouts of this same
// project), installed dependencies, and OpenCode's generated engine mirror.
var retiredSkippedDirs = map[string]bool{".git": true, ".worktrees": true, "node_modules": true, ".opencode": true}

// retiredWalkSkippedDirs are the further directory names a walk outside a git
// work tree skips: with no git to say what is the project's own text, the
// dependency, build and cache directories are named. Inside a work tree git's
// own file list decides, and these names are not special.
var retiredWalkSkippedDirs = map[string]bool{
	".venv": true, "venv": true, "target": true, "dist": true, "build": true, "vendor": true, "__pycache__": true,
}

// retiredBinaryProbe is how many leading bytes are searched for a NUL: one
// there marks the file binary and unscanned.
const retiredBinaryProbe = 8000

// retiredMaxRead bounds bytes scanned per file; one extra byte detects a gap.
const retiredMaxRead = 8 << 20

// retiredMaxLine is the longest line the scan holds in memory. A file with a
// longer line is scanned up to it and reported INCOMPLETE until accepted, so a
// newline-free dump costs at most this much whatever its size.
const retiredMaxLine = 4 << 20

// ScanRetiredNames is the retired-name pass of `pfm doctor --project-updates`:
// the project's own text files except the ones the caller skips (pinned files
// whose report row covers them; slash-separated, root-relative), pfm's own
// baseline and the generated engine mirrors, each line checked against
// retiredNames. In a git work tree the project's own
// text is git's tracked and unignored files; elsewhere it is a walk of the
// root minus the dependency, build and cache directories. A file or
// directory it cannot read is a Failure, never a clean result.
func ScanRetiredNames(root string, pinned map[string]bool) professor.RetiredNameScan {
	return ScanRetiredNamesWithContext(context.Background(), root, pinned)
}

// ScanRetiredNamesWithContext shares the caller's deadline with git enumeration
// and stops between regular-file reads when the context is cancelled.
func ScanRetiredNamesWithContext(ctx context.Context, root string, pinned map[string]bool) professor.RetiredNameScan {
	return scanRetiredNamesOpening(ctx, root, pinned, openRetiredFile)
}

func scanRetiredNamesWith(
	root string,
	pinned map[string]bool,
	open func(string) (io.ReadCloser, error),
) professor.RetiredNameScan {
	return scanRetiredNamesOpening(context.Background(), root, pinned, open)
}

func scanRetiredNamesOpening(
	ctx context.Context,
	root string,
	pinned map[string]bool,
	open func(string) (io.ReadCloser, error),
) professor.RetiredNameScan {
	scan := scanRetiredNamesRaw(ctx, root, pinned, open)
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		scan.Hits = dropDefinedNames(resolved, scan.Hits)
	}
	return scan
}

// scanRetiredNamesRaw is the scan before the project's own definitions are
// weighed.
func scanRetiredNamesRaw(
	ctx context.Context,
	root string,
	pinned map[string]bool,
	open func(string) (io.ReadCloser, error),
) professor.RetiredNameScan {
	scan := professor.RetiredNameScan{Hits: []professor.RetiredNameHit{}, Failures: []professor.RetiredNameFailure{}}
	fail := func(relative string, err error) {
		scan.Failures = append(scan.Failures, professor.RetiredNameFailure{Path: relative, Error: err.Error()})
	}
	scanFile := func(current, relative string) {
		hits, incomplete, err := scanRetiredFile(ctx, open, current, relative)
		if incomplete != "" {
			scan.Incomplete = append(scan.Incomplete, professor.RetiredNameFailure{Path: relative, Error: incomplete})
		}
		if err != nil {
			fail(relative, err)
		}
		scan.Hits = append(scan.Hits, hits...)
	}
	// A root reached through a symlink is the directory it names: WalkDir
	// would lstat the link itself and visit nothing.
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		fail(".", fmt.Errorf("resolve project root %s: %w", root, err))
		return scan
	}
	if info, statErr := os.Stat(resolved); statErr != nil || !info.IsDir() {
		if statErr == nil {
			statErr = fmt.Errorf("project root %s is not a directory", resolved)
		}
		fail(".", statErr)
		return scan
	}
	listed, inWorkTree, listErr := gitProjectFiles(ctx, resolved)
	if listErr != nil {
		fail(".", listErr)
		return scan
	}
	if inWorkTree {
		for _, relative := range listed {
			if err := ctx.Err(); err != nil {
				fail(".", fmt.Errorf("scan project files: %w", err))
				break
			}
			current := filepath.Join(resolved, filepath.FromSlash(relative))
			if retiredInSkippedDir(relative) || retiredSkippedFile(relative, pinned) {
				continue
			}
			info, statErr := os.Lstat(current)
			switch {
			case errors.Is(statErr, fs.ErrNotExist):
				continue // tracked, deleted from the work tree
			case statErr != nil:
				fail(relative, statErr)
				continue
			case !info.Mode().IsRegular():
				continue
			}
			scanFile(current, relative)
		}
		return scan
	}
	walkErr := filepath.WalkDir(resolved, func(current string, entry fs.DirEntry, err error) error {
		if contextErr := ctx.Err(); contextErr != nil {
			return fmt.Errorf("walk project files: %w", contextErr)
		}
		relative, relErr := filepath.Rel(resolved, current)
		if relErr != nil {
			fail(current, relErr)
			return nil
		}
		relative = filepath.ToSlash(relative)
		if err != nil {
			fail(relative, err)
			if entry != nil && entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if relative != "." && (retiredSkippedDirs[entry.Name()] || retiredWalkSkippedDirs[entry.Name()]) {
				return fs.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() || retiredSkippedFile(relative, pinned) {
			return nil
		}
		scanFile(current, relative)
		return nil
	})
	if walkErr != nil {
		fail(".", walkErr)
	}
	return scan
}

// gitProjectFiles lists, slash-separated and sorted, the files git tracks or
// leaves unignored under root. inWorkTree is false only when git is absent or
// says root is not inside a work tree, where the caller walks instead; any
// other failure of git, before or during the listing, is an error — a failed
// enumeration of the project's own text never widens into reading everything.
func gitProjectFiles(ctx context.Context, root string) (files []string, inWorkTree bool, err error) {
	// LC_ALL=C keeps git's "not a git repository" message in the one language
	// it is matched in.
	probe, err := updateGitCapture(ctx, root, []string{"LC_ALL=C"}, "rev-parse", "--is-inside-work-tree")
	switch {
	case errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist):
		return nil, false, nil
	case err != nil:
		return nil, true, fmt.Errorf("inspect git work tree: %w", err)
	case probe.ExitCode != 0 && strings.Contains(string(probe.Stderr), "not a git repository"):
		return nil, false, nil
	case probe.ExitCode != 0:
		return nil, true, fmt.Errorf(
			"inspect git work tree: git rev-parse exited %d: %s",
			probe.ExitCode, strings.TrimSpace(string(probe.Stderr)),
		)
	case strings.TrimSpace(string(probe.Stdout)) != "true":
		return nil, false, nil
	}
	listing, err := updateGitCapture(
		ctx,
		root,
		[]string{"LC_ALL=C"},
		"ls-files",
		"-z",
		"--cached",
		"--others",
		"--exclude-standard",
		"--deduplicate",
	)
	if err != nil {
		return nil, true, fmt.Errorf("list the project's files: %w", err)
	}
	if listing.ExitCode != 0 || len(bytes.TrimSpace(listing.Stderr)) != 0 {
		return nil, true, fmt.Errorf(
			"list the project's files: git ls-files exited %d: %s",
			listing.ExitCode,
			strings.TrimSpace(string(listing.Stderr)),
		)
	}
	for _, name := range strings.Split(string(listing.Stdout), "\x00") {
		if name != "" {
			files = append(files, name)
		}
	}
	sort.Strings(files)
	return slices.Compact(files), true, nil
}

// retiredInSkippedDir: some directory on the path is one the scan never enters.
func retiredInSkippedDir(relative string) bool {
	parts := strings.Split(relative, "/")
	for _, part := range parts[:len(parts)-1] {
		if retiredSkippedDirs[part] {
			return true
		}
	}
	return false
}

// scanRetiredFile probes the first retiredBinaryProbe bytes for binary content,
// then streams complete logical lines. Memory holds at most retiredMaxLine of
// one line plus the actual hits; a longer line ends the scan with incomplete
// naming where the unscanned text starts, the hits before it kept.
func scanRetiredFile(
	ctx context.Context,
	open func(string) (io.ReadCloser, error),
	current, relative string,
) (hits []professor.RetiredNameHit, incomplete string, err error) {
	file, err := open(current)
	if err != nil {
		return nil, "", fmt.Errorf("open %s: %w", relative, err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close %s: %w", relative, closeErr))
		}
	}()
	content := make([]byte, retiredBinaryProbe)
	limited := &io.LimitedReader{R: file, N: retiredMaxRead + 1}
	count, err := io.ReadFull(limited, content)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, "", fmt.Errorf("read %s: %w", relative, err)
	}
	content = content[:count]
	if bytes.IndexByte(content, 0) >= 0 {
		return nil, "", nil
	}
	reader := bufio.NewReader(io.MultiReader(bytes.NewReader(content), limited))
	scanned := 0
	for lineNumber := 1; ; lineNumber++ {
		if contextErr := ctx.Err(); contextErr != nil {
			return hits, "", fmt.Errorf("scan %s: %w", relative, contextErr)
		}
		line, readErr := readRetiredLine(reader)
		if errors.Is(readErr, errRetiredLineTooLong) {
			return hits, fmt.Sprintf(
				"line %d is longer than %d bytes; the text from there on was not scanned", lineNumber, retiredMaxLine,
			), nil
		}
		if readErr == nil || errors.Is(readErr, io.EOF) {
			scanned += len(line)
			if scanned > retiredMaxRead {
				return hits, fmt.Sprintf(
					"%d-byte scan limit reached; line %d and the text from there on were not scanned",
					retiredMaxRead,
					lineNumber,
				), nil
			}
			for _, hit := range retiredNameHits(relative, line) {
				hit.Line = lineNumber
				hits = append(hits, hit)
			}
		}
		if errors.Is(readErr, io.EOF) {
			return hits, "", nil
		}
		if readErr != nil {
			return hits, "", fmt.Errorf("read %s: %w", relative, readErr)
		}
	}
}

var errRetiredLineTooLong = errors.New("retired-name scan: line over the read bound")

// readRetiredLine reads one line, newline included, refusing one longer than
// retiredMaxLine before it is held whole.
func readRetiredLine(reader *bufio.Reader) (string, error) {
	var line []byte
	for {
		chunk, err := reader.ReadSlice('\n')
		if len(line)+len(chunk) > retiredMaxLine {
			return "", errRetiredLineTooLong
		}
		line = append(line, chunk...)
		if !errors.Is(err, bufio.ErrBufferFull) {
			return string(line), err
		}
	}
}

// retiredSkippedFile: a pinned file (the project-tier diff already covers
// it), pfm's own pin ledger, and the generated engine mirrors — every
// AGENTS.md and everything under .codex/ but the hand-written config.toml.
// A symlink never reaches here (only regular files are read), so a
// machine-global link into the project is not the project's text.
func retiredSkippedFile(relative string, pinned map[string]bool) bool {
	if pinned[relative] || relative == ".professor/baseline.json" {
		return true
	}
	base := path.Base(relative)
	if base == "AGENTS.md" {
		return true
	}
	inCodex := strings.HasPrefix(relative, ".codex/") || strings.Contains(relative, "/.codex/")
	return inCodex && base != "config.toml"
}

// retiredNameHits returns one hit per retired name per line it appears on.
func retiredNameHits(relative, content string) []professor.RetiredNameHit {
	var hits []professor.RetiredNameHit
	for index, line := range strings.Split(content, "\n") {
		for _, retired := range retiredNames {
			if retired.foundIn(line) {
				hits = append(hits, professor.RetiredNameHit{
					Path:      relative,
					Line:      index + 1,
					Name:      retired.name,
					Kind:      retired.kind,
					Successor: retired.successor,
				})
			}
		}
	}
	return hits
}

func (r retiredName) foundIn(line string) bool {
	for offset := 0; offset < len(line); {
		index := strings.Index(line[offset:], r.name)
		if index < 0 {
			return false
		}
		start := offset + index
		if !insideURL(line, start) && r.boundedAt(line, start, start+len(r.name)) {
			return true
		}
		offset = start + 1
	}
	return false
}

// boundedAt: the match is a whole reference, never part of a longer name. On
// the left no name byte may touch it; a name that opens with `/` or `.` also
// may not follow a `/` or `.` (`~/.claude/commands/pfm.md` is the live
// machine-global guide, `.claude/commands/pfm.md` the retired project file).
// On the right a name byte ends the match for every kind but a prefix, and a
// `:` continues it only for a family.
func (r retiredName) boundedAt(line string, start, end int) bool {
	if r.name == "/tokens" || r.name == "/reminder" {
		if end < len(line) && (line[end] == '/' || line[end] == '{') {
			return false
		}
		before := strings.Fields(strings.TrimRight(line[:start], " `\"'"))
		if len(before) != 0 {
			switch strings.ToUpper(before[len(before)-1]) {
			case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "CONNECT", "TRACE":
				return false
			}
		}
	}
	if start > 0 {
		before := line[start-1]
		if isRetiredNameByte(before) {
			return false
		}
		if (r.name[0] == '/' || r.name[0] == '.') && (before == '/' || before == '.') {
			return false
		}
	}
	if r.match == matchPrefix || end == len(line) {
		return true
	}
	next := line[end]
	if next == ':' {
		return r.match == matchFamily
	}
	return !isRetiredNameByte(next)
}

// insideURL: the match sits in a URL — the whitespace-, quote- or
// bracket-delimited token holding it has a scheme separator before it
// (`https://opencode.ai/docs/agents/` names a web page, not a project path).
func insideURL(line string, start int) bool {
	tokenStart := strings.LastIndexAny(line[:start], " \t`\"'(<[") + 1
	return strings.Contains(line[tokenStart:start], "://")
}

func isRetiredNameByte(b byte) bool {
	return b == '_' || b == '-' || ('a' <= b && b <= 'z') || ('A' <= b && b <= 'Z') || ('0' <= b && b <= '9')
}

// openRetiredFile opens a file for the scan.
func openRetiredFile(name string) (io.ReadCloser, error) { return os.Open(name) }

// RetiredRegistryNames derives from retiredNames the names the registry
// ownership scan matches against an unmarked entry: an agent by file stem, a
// command by its path below the commands registry (`/qa:live` is
// qa/live), a skill by directory name (`skills/transcript` is transcript). A
// name that is not one registry entry (a phrase, an MCP tool, a path) is left out.
func RetiredRegistryNames() installer.RetiredRegistryNames {
	names := installer.RetiredRegistryNames{
		Agents: map[string]bool{}, Commands: map[string]bool{}, Skills: map[string]bool{},
	}
	for _, retired := range retiredNames {
		name := retired.name
		if retired.kind == retiredAgent && retired.probe != "" {
			name = strings.TrimSuffix(path.Base(retired.probe), ".md")
		}
		if strings.ContainsAny(name, " ") {
			continue
		}
		switch retired.kind {
		case retiredAgent:
			if !strings.ContainsAny(name, "/:") {
				names.Agents[name] = true
			}
		case retiredCommand:
			if command, found := strings.CutPrefix(name, "/"); found && command != "" {
				names.Commands[strings.ReplaceAll(strings.TrimSuffix(command, ":"), ":", "/")] = true
			}
		case retiredSkill:
			skill := strings.TrimPrefix(name, "skills/")
			if !strings.ContainsAny(skill, "/:") {
				names.Skills[skill] = true
			}
		}
	}
	return names
}
