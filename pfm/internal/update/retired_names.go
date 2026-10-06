package update

import (
	"bytes"
	"io/fs"
	"os"
	"path"
	"path/filepath"
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

// retiredNames is the one registry of retired names `pfm doctor
// --project-updates` scans a project's unpinned files for. Every entry cites
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
	{name: "mono-architect", kind: retiredAgent, match: matchExact},
	{name: "mono-documenter", kind: retiredAgent, match: matchExact},
	{name: "mono-planner", kind: retiredAgent, match: matchExact},
	{name: "rndier", kind: retiredAgent, match: matchExact},
	{name: "role-wrapper", kind: retiredAgent, match: matchExact},
	{name: "qa-wrapper", kind: retiredAgent, match: matchExact},
	// releases/v0.78.0.md "Global: rr family": "rr-super is retired"; its
	// stronger variant is super-rr.
	{name: "rr-super", kind: retiredAgent, successor: "super-rr", match: matchExact},
}

// retiredSkippedDirs are directory names the scan never enters at any depth:
// git's store, the flights' nested worktrees (other checkouts of this same
// project), installed dependencies, and OpenCode's generated engine mirror.
var retiredSkippedDirs = map[string]bool{".git": true, ".worktrees": true, "node_modules": true, ".opencode": true}

// retiredBinaryProbe is how many leading bytes are searched for a NUL: one
// there marks the file binary and unscanned.
const retiredBinaryProbe = 8000

// ScanRetiredNames is the retired-name pass of `pfm doctor --project-updates`:
// every text file under root except the pinned ones (slash-separated,
// root-relative), pfm's own baseline and the generated engine mirrors, each
// line checked against retiredNames. A file or directory it cannot read is a
// Failure, never a clean result.
func ScanRetiredNames(root string, pinned map[string]bool) professor.RetiredNameScan {
	return scanRetiredNamesWith(root, pinned, os.ReadFile)
}

func scanRetiredNamesWith(
	root string,
	pinned map[string]bool,
	readFile func(string) ([]byte, error),
) professor.RetiredNameScan {
	scan := professor.RetiredNameScan{Hits: []professor.RetiredNameHit{}, Failures: []professor.RetiredNameFailure{}}
	fail := func(relative string, err error) {
		scan.Failures = append(scan.Failures, professor.RetiredNameFailure{Path: relative, Error: err.Error()})
	}
	walkErr := filepath.WalkDir(root, func(current string, entry fs.DirEntry, err error) error {
		relative, relErr := filepath.Rel(root, current)
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
			if relative != "." && retiredSkippedDirs[entry.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() || retiredSkippedFile(relative, pinned) {
			return nil
		}
		content, readErr := readFile(current)
		if readErr != nil {
			fail(relative, readErr)
			return nil
		}
		if bytes.IndexByte(content[:min(len(content), retiredBinaryProbe)], 0) >= 0 {
			return nil
		}
		scan.Hits = append(scan.Hits, retiredNameHits(relative, string(content))...)
		return nil
	})
	if walkErr != nil {
		fail(".", walkErr)
	}
	return scan
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
		if r.boundedAt(line, start, start+len(r.name)) {
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

func isRetiredNameByte(b byte) bool {
	return b == '_' || b == '-' || ('a' <= b && b <= 'z') || ('A' <= b && b <= 'Z') || ('0' <= b && b <= '9')
}
