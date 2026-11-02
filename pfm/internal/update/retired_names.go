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

	"github.com/rezzminator/professor/pfm/internal/professor"
	"github.com/rezzminator/professor/pfm/internal/retired"
)

// RetiredPfmCommand answers the dispatch of a retired `pfm {command}`: the
// retired name and the successor names gives it. ok is false for a command
// names does not retire as a command with a successor.
func RetiredPfmCommand(names []retired.Name, command string) (name, successor string, ok bool) {
	name = "pfm " + command
	for _, entry := range names {
		if entry.Kind == retired.KindCommand && entry.Name == name && entry.Successor != "" {
			return entry.Name, entry.Successor, true
		}
	}
	return "", "", false
}

// definitionProbes are the root-relative paths whose existence means the
// project itself still defines the retired name: its own command, agent or
// skill file. Retired paths, phases, MCP tools and commands spelled with a
// space are defined by no project file; a legacy path cannot exempt itself.
func definitionProbes(r retired.Name) []string {
	if r.Probe != "" {
		return []string{r.Probe}
	}
	switch r.Kind {
	case retired.KindCommand:
		command := strings.ReplaceAll(strings.TrimSuffix(strings.TrimPrefix(r.Name, "/"), ":"), ":", "/")
		if command == "" || strings.ContainsAny(command, " ") {
			return nil
		}
		return []string{".claude/commands/" + command + ".md", ".claude/commands/" + command}
	case retired.KindAgent:
		return []string{".claude/agents/" + r.Name + ".md"}
	case retired.KindSkill:
		return []string{".claude/skills/" + strings.TrimSuffix(strings.TrimPrefix(r.Name, "skills/"), " skill")}
	}
	return nil
}

// dropDefinedNames removes the hits on a retired name the project defines
// itself: a project that keeps its own /km command is not naming a retired
// one. A probe that cannot be statted for any reason but absence leaves the
// name flagged.
func dropDefinedNames(names []retired.Name, root string, hits []professor.RetiredNameHit) []professor.RetiredNameHit {
	defined := map[string]bool{}
	for _, entry := range names {
		for _, probe := range definitionProbes(entry) {
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(probe))); err == nil {
				defined[entry.Name] = true
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

// RetiredNameScanner is the retired-name pass of `pfm doctor
// --project-updates` over the names of home's clone (retired.LoadLists), read
// once: the project's own text files except the ones the caller skips (pinned
// files whose report row covers them; slash-separated, root-relative), pfm's
// own baseline and the generated engine mirrors, each line checked against
// the names. In a git work tree the project's own text is git's tracked and
// unignored files; elsewhere it is a walk of the root minus the dependency,
// build and cache directories. ctx bounds git enumeration and stops the scan
// between file reads. A file or directory it cannot read, or a list that
// cannot load, is a Failure, never a clean result.
func RetiredNameScanner(ctx context.Context, home string) professor.RetiredNameScanner {
	lists, err := retired.LoadLists(home)
	if err != nil {
		return func(string, map[string]bool) professor.RetiredNameScan {
			return professor.RetiredNameScan{
				Hits:     []professor.RetiredNameHit{},
				Failures: []professor.RetiredNameFailure{{Path: ".", Error: err.Error()}},
			}
		}
	}
	return func(root string, pinned map[string]bool) professor.RetiredNameScan {
		return scanRetiredNames(ctx, lists.Names, root, pinned, openRetiredFile)
	}
}

func scanRetiredNames(
	ctx context.Context,
	names []retired.Name,
	root string,
	pinned map[string]bool,
	open func(string) (io.ReadCloser, error),
) professor.RetiredNameScan {
	scan := scanRetiredNamesRaw(ctx, names, root, pinned, open)
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		scan.Hits = dropDefinedNames(names, resolved, scan.Hits)
	}
	return scan
}

// scanRetiredNamesRaw is the scan before the project's own definitions are
// weighed.
func scanRetiredNamesRaw(
	ctx context.Context,
	names []retired.Name,
	root string,
	pinned map[string]bool,
	open func(string) (io.ReadCloser, error),
) professor.RetiredNameScan {
	scan := professor.RetiredNameScan{Hits: []professor.RetiredNameHit{}, Failures: []professor.RetiredNameFailure{}}
	fail := func(relative string, err error) {
		scan.Failures = append(scan.Failures, professor.RetiredNameFailure{Path: relative, Error: err.Error()})
	}
	scanFile := func(current, relative string) {
		hits, incomplete, err := scanRetiredFile(ctx, names, open, current, relative)
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
	names []retired.Name,
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
			for _, hit := range retiredNameHits(names, relative, line) {
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
func retiredNameHits(names []retired.Name, relative, content string) []professor.RetiredNameHit {
	var hits []professor.RetiredNameHit
	for index, line := range strings.Split(content, "\n") {
		for _, entry := range names {
			if foundIn(entry, line) {
				hits = append(hits, professor.RetiredNameHit{
					Path:      relative,
					Line:      index + 1,
					Name:      entry.Name,
					Kind:      entry.Kind,
					Successor: entry.Successor,
				})
			}
		}
	}
	return hits
}

func foundIn(r retired.Name, line string) bool {
	for offset := 0; offset < len(line); {
		index := strings.Index(line[offset:], r.Name)
		if index < 0 {
			return false
		}
		start := offset + index
		if !insideURL(line, start) && boundedAt(r, line, start, start+len(r.Name)) {
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
// `:` continues it only for a family. A route name followed by `/` or `{`, or
// after an HTTP method, is the route, never the name.
func boundedAt(r retired.Name, line string, start, end int) bool {
	if r.Route {
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
		if (r.Name[0] == '/' || r.Name[0] == '.') && (before == '/' || before == '.') {
			return false
		}
	}
	if r.Match == retired.MatchPrefix || end == len(line) {
		return true
	}
	next := line[end]
	if next == ':' {
		return r.Match == retired.MatchFamily
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
