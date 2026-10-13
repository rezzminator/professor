package update

import (
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
}

// chatlogSuccessor is what replaced the /tokens command and the transcript skill.
const chatlogSuccessor = "the chatlog skill"

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
	{name: "mcp__chat__", kind: retiredTool, successor: "mcp__professor__chat_*", match: matchPrefix},
	{name: "mcp__harvester__", kind: retiredTool, successor: "mcp__professor__harvester_*", match: matchPrefix},
	// releases/v0.62.0.md "Removed: the host-level /chat:* Claude
	// slash-command family"; "drive chat through the MCP tools instead".
	{name: "/chat:", kind: retiredCommand, successor: "mcp__professor__chat_*", match: matchPrefix},
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
	// Commit 73f2ae2b "/reminder becomes /pfm:schedule".
	{name: "pfm chat reminder", kind: retiredCommand, successor: "pfm chat schedule", match: matchExact},
	{name: "build-codex.mjs", kind: retiredPath, successor: "pfm codex build|check", match: matchExact},
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
}

// definitionProbes are the root-relative paths whose existence means the
// project itself still defines the retired name: its own command, agent or
// skill file, or the retired path itself. A phase, an MCP tool and a command
// spelled with a space are defined by no project file.
func (r retiredName) definitionProbes() []string {
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
	case retiredPath:
		return []string{r.name}
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

// retiredMaxRead is the most of one file the scan reads.
const retiredMaxRead = 1 << 20

// ScanRetiredNames is the retired-name pass of `pfm doctor --project-updates`:
// the project's own text files except the ones the caller skips (pinned files
// whose report row covers them; slash-separated, root-relative), pfm's own
// baseline and the generated engine mirrors, each line checked against
// retiredNames. In a git work tree the project's own
// text is git's tracked and unignored files; elsewhere it is a walk of the
// root minus the dependency, build and cache directories. A file or
// directory it cannot read is a Failure, never a clean result.
func ScanRetiredNames(root string, pinned map[string]bool) professor.RetiredNameScan {
	return scanRetiredNamesWith(root, pinned, openRetiredFile)
}

func scanRetiredNamesWith(
	root string,
	pinned map[string]bool,
	open func(string) (io.ReadCloser, error),
) professor.RetiredNameScan {
	scan := scanRetiredNamesRaw(root, pinned, open)
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		scan.Hits = dropDefinedNames(resolved, scan.Hits)
	}
	return scan
}

// scanRetiredNamesRaw is the scan before the project's own definitions are
// weighed.
func scanRetiredNamesRaw(
	root string,
	pinned map[string]bool,
	open func(string) (io.ReadCloser, error),
) professor.RetiredNameScan {
	scan := professor.RetiredNameScan{Hits: []professor.RetiredNameHit{}, Failures: []professor.RetiredNameFailure{}}
	fail := func(relative string, err error) {
		scan.Failures = append(scan.Failures, professor.RetiredNameFailure{Path: relative, Error: err.Error()})
	}
	scanFile := func(current, relative string) {
		hits, incomplete, err := scanRetiredFile(open, current, relative)
		if incomplete {
			scan.Incomplete = append(
				scan.Incomplete,
				professor.RetiredNameFailure{
					Path:  relative,
					Error: fmt.Sprintf("read limit of %d bytes; remaining text was not scanned", retiredMaxRead),
				},
			)
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
	listed, inWorkTree, listErr := gitProjectFiles(resolved)
	if listErr != nil {
		fail(".", listErr)
		return scan
	}
	if inWorkTree {
		for _, relative := range listed {
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
func gitProjectFiles(root string) (files []string, inWorkTree bool, err error) {
	ctx := context.Background()
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

// scanRetiredFile reads one file through open and returns its hits. It reads
// the first retiredBinaryProbe bytes to decide the file is binary (a NUL) and
// stops there; a text file reads at most retiredMaxRead plus one EOF probe, the cap cutting
// at its last whole line. Reaching the cap is visibly incomplete; hits in the
// readable prefix are retained, never presented as full coverage.
func scanRetiredFile(
	open func(string) (io.ReadCloser, error),
	current, relative string,
) (hits []professor.RetiredNameHit, incomplete bool, err error) {
	file, err := open(current)
	if err != nil {
		return nil, false, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	content := make([]byte, retiredBinaryProbe)
	count, err := io.ReadFull(file, content)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, false, err
	}
	content = content[:count]
	if bytes.IndexByte(content, 0) >= 0 {
		return nil, false, nil
	}
	if count == retiredBinaryProbe {
		rest, err := io.ReadAll(io.LimitReader(file, retiredMaxRead-retiredBinaryProbe+1))
		if err != nil {
			return nil, false, err
		}
		content = append(content, rest...)
		if len(content) > retiredMaxRead {
			content = content[:retiredMaxRead]
			if cut := bytes.LastIndexByte(content, '\n'); cut >= 0 {
				content = content[:cut]
			}
			return retiredNameHits(relative, string(content)), true, nil
		}
	}
	return retiredNameHits(relative, string(content)), false, nil
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
