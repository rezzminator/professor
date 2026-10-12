package digest

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/codexmeta"
	"github.com/rezzminator/professor/pfm/internal/index"
	"github.com/rezzminator/professor/pfm/internal/pyvalue"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// nameTail is how much of a session transcript's end the name lookup reads.
const nameTail = 256 * 1024

var (
	agentPathTarget = regexp.MustCompile(`^/root(?:/[0-9A-Za-z_-]+)+$`)
	idTarget        = regexp.MustCompile(`^[0-9A-Za-z_-]{6,}$`)
	tailStamp       = regexp.MustCompile(`"timestamp"[ \t\n\r\f\v]*:[ \t\n\r\f\v]*"([^"]+)"`)
)

// resolver turns a target into one transcript path over pfm's roots and the
// run's extra --root directories; out collects its warnings.
type resolver struct {
	ctx      context.Context
	env      Env
	extra    []string
	out      *Output
	openCode struct {
		read   bool
		stores int
		hits   []openCodeHit
		failed []error
	}
}

// resolve is the transcript a target names: a file as given, an OpenCode
// locator as given, a Codex agent path by its rollout's session_meta, an id or
// prefix by file name or OpenCode session id, else a Claude chat name by its
// last title or an OpenCode session by its title.
func (finder *resolver) resolve(target string) (string, error) {
	if info, err := os.Stat(target); err == nil && info.Mode().IsRegular() {
		return target, nil
	}
	if _, _, ok := splitOpenCodeLocator(target); ok {
		return target, nil
	}
	if agentPathTarget.MatchString(target) {
		hits, unreadable, errs := codexmeta.AgentPathRollouts(finder.ctx, finder.env.CodexHomes, finder.extra, target)
		listed := make([]string, 0, len(unreadable)+len(errs))
		for _, entry := range unreadable {
			listed = append(listed, fmt.Sprintf("%s (%v)", entry.Path, entry.Err))
		}
		for _, err := range errs {
			listed = append(listed, err.Error())
		}
		return finder.pick(target, hits, listed)
	}
	if filepath.IsAbs(target) || strings.HasPrefix(target, "~") || strings.HasPrefix(target, ".") ||
		strings.HasSuffix(target, ".jsonl") {
		return "", runtimeFailure("no such transcript file: %s", target)
	}
	id := strings.TrimPrefix(target, "agent-")
	var hits []string
	var failed []error
	if idTarget.MatchString(id) {
		hits, failed = finder.idHits(id)
	}
	if len(hits) != 0 {
		finder.warnUnreadable(target, failed)
		return finder.pick(target, hits, nil)
	}
	return finder.byName(target, failed)
}

// warnUnreadable warns of the places the id search could not read, beside
// the transcript it resolved.
func (finder *resolver) warnUnreadable(target string, failed []error) {
	if len(failed) != 0 {
		finder.out.Warnings = append(finder.out.Warnings, fmt.Sprintf("%s%d places unreadable while resolving %s: %s",
			warningPrefix, len(failed), target, joinErrors(failed)))
	}
}

// idHits lists every transcript whose file name carries id: a Claude session
// or sub-agent, a Codex rollout, any of them below an extra root, and every
// OpenCode session whose id starts with id (read only for an id shaped like
// one, so a Claude or Codex id never waits on or warns of the store).
func (finder *resolver) idHits(id string) (hits []string, failed []error) {
	if strings.HasPrefix(id, openCodeSessionPrefix) {
		sessions, errs := finder.openCodeSessions()
		failed = append(failed, errs...)
		for at := range sessions {
			if strings.HasPrefix(sessions[at].session.ID, id) {
				hits = append(hits, sessions[at].locator())
			}
		}
	}
	claude, errs := transcript.FindSessions(finder.env.ClaudeRoots, id)
	hits, failed = append(hits, claude...), append(failed, errs...)
	agents, errs := transcript.FindSubagents(finder.env.ClaudeRoots, id)
	hits, failed = append(hits, agents...), append(failed, errs...)
	for _, home := range finder.env.CodexHomes {
		rollouts, errs := codexmeta.WalkRollouts(finder.ctx, home, codexmeta.SessionsDir, codexmeta.ArchivedSessionsDir)
		failed = append(failed, errs...)
		for _, rollout := range rollouts {
			if strings.Contains(strings.TrimPrefix(filepath.Base(rollout.Path), rolloutPrefix), id) {
				hits = append(hits, rollout.Path)
			}
		}
	}
	for _, root := range finder.extra {
		found, err := extraRootHits(root, id)
		hits = append(hits, found...)
		if err != nil {
			failed = append(failed, err)
		}
	}
	return hits, failed
}

const rolloutPrefix = "rollout-"

// extraRootHits walks root for {id}*.jsonl, agent-{id}*.jsonl and
// rollout-*{id}*.jsonl, a dot-named entry skipped; a missing root holds
// nothing.
func extraRootHits(root, id string) ([]string, error) {
	var hits []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if path == root && errors.Is(walkErr, fs.ErrNotExist) {
				return filepath.SkipAll
			}
			return walkErr
		}
		name := entry.Name()
		if path != root && strings.HasPrefix(name, ".") {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() || !strings.HasSuffix(name, ".jsonl") {
			return nil
		}
		if strings.HasPrefix(name, id) || strings.HasPrefix(name, "agent-"+id) ||
			(strings.HasPrefix(name, rolloutPrefix) && strings.Contains(name[len(rolloutPrefix):], id)) {
			hits = append(hits, path)
		}
		return nil
	})
	if err != nil {
		return hits, fmt.Errorf("search %s: %w", root, err)
	}
	return hits, nil
}

// pick is the one transcript hits name: none is NOT FOUND, several are
// AMBIGUOUS; unreadable lists the candidates that could not be read, a
// warning beside a hit.
func (finder *resolver) pick(target string, hits, unreadable []string) (string, error) {
	hits = uniqueResolved(hits)
	if len(hits) == 0 {
		tail := ""
		if len(unreadable) != 0 {
			tail = fmt.Sprintf("; %d rollouts unreadable: %s", len(unreadable), strings.Join(unreadable, " "))
		}
		return "", runtimeFailure("NOT FOUND %s — searched %s%s", target, finder.searched(), tail)
	}
	if len(hits) > 1 {
		sort.Strings(hits)
		return "", runtimeFailure("AMBIGUOUS %s — %d transcripts: %s", target, len(hits), strings.Join(hits, " "))
	}
	if len(unreadable) != 0 {
		finder.out.Warnings = append(finder.out.Warnings, fmt.Sprintf("%s%d rollouts unreadable while resolving %s: %s",
			warningPrefix, len(unreadable), target, strings.Join(unreadable, " ")))
	}
	return hits[0], nil
}

// searched names every root that exists, Claude roots first, an OpenCode
// root by its store.
func (finder *resolver) searched() string {
	var roots []string
	for _, group := range [][]string{finder.env.ClaudeRoots, finder.env.CodexHomes} {
		for _, root := range group {
			if info, err := os.Stat(root); err == nil && info.IsDir() {
				roots = append(roots, root)
			}
		}
	}
	for _, root := range finder.env.OpenCodeRoots {
		if _, err := os.Stat(index.OpenCodeStorePath(root)); err == nil {
			roots = append(roots, index.OpenCodeStorePath(root))
		}
	}
	for _, root := range finder.extra {
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			roots = append(roots, root)
		}
	}
	if len(roots) == 0 {
		return "no root exists"
	}
	return strings.Join(roots, ", ")
}

// byName is the Claude session transcript whose last title is target, over
// every directory below the Claude roots and the extra roots, or the OpenCode
// session titled target. A place that
// could not be read makes the lookup INCOMPLETE, never NOT FOUND; idFailed,
// the places the id search before it could not read (a Codex home among
// them), is a warning beside a found chat and named in a NOT FOUND. A done
// context stops the walk between directories and files and is returned as it
// is.
func (finder *resolver) byName(target string, idFailed []error) (string, error) {
	var files, unreadable []string
	var pending []string
	for _, root := range finder.env.ClaudeRoots {
		info, err := os.Stat(root)
		switch {
		case err == nil && info.IsDir():
			pending = append(pending, root)
		case err == nil:
			unreadable = append(unreadable, root+" (not a directory)")
		case errors.Is(err, fs.ErrNotExist):
			if _, linkErr := os.Lstat(root); linkErr == nil {
				unreadable = append(unreadable, fmt.Sprintf("%s (%v)", root, err))
			}
		default:
			unreadable = append(unreadable, fmt.Sprintf("%s (%v)", root, err))
		}
	}
	pending = append(pending, finder.extra...)
	visited := map[string]bool{}
	for len(pending) != 0 {
		if err := finder.ctx.Err(); err != nil {
			return "", err
		}
		folder := resolvedPath(pending[len(pending)-1])
		pending = pending[:len(pending)-1]
		if visited[folder] {
			continue
		}
		visited[folder] = true
		entries, err := os.ReadDir(folder)
		if err != nil {
			unreadable = append(unreadable, fmt.Sprintf("%s (%v)", folder, err))
			continue
		}
		for _, entry := range entries {
			path := filepath.Join(folder, entry.Name())
			info, err := os.Stat(path)
			switch {
			case err != nil:
				unreadable = append(unreadable, fmt.Sprintf("%s (%v)", path, err))
			case info.IsDir():
				pending = append(pending, path)
			case strings.HasSuffix(entry.Name(), ".jsonl") && !strings.HasPrefix(entry.Name(), "agent-") &&
				!strings.HasPrefix(entry.Name(), rolloutPrefix):
				files = append(files, path)
			}
		}
	}
	files = uniqueResolved(files)
	type hit struct {
		path string
		tail []byte
	}
	var hits []hit
	for _, path := range files {
		if err := finder.ctx.Err(); err != nil {
			return "", err
		}
		tail, _, err := transcript.ReadTailLines(path, nameTail)
		if err != nil {
			unreadable = append(unreadable, fmt.Sprintf("%s (%v)", path, err))
			continue
		}
		if name := transcript.TailTitle(tail); name != "" && strings.EqualFold(name, target) {
			hits = append(hits, hit{path: path, tail: tail})
		}
	}
	sessions, errs := finder.openCodeSessions()
	for _, err := range errs {
		unreadable = append(unreadable, err.Error())
	}
	var named []openCodeHit
	for at := range sessions {
		if strings.EqualFold(sessions[at].session.Title, target) {
			named = append(named, sessions[at])
		}
	}
	if len(unreadable) != 0 {
		return "", runtimeFailure("INCOMPLETE name lookup for %s — %d discovery/read errors: %s",
			pyvalue.ReprString(target), len(unreadable), strings.Join(unreadable, " "))
	}
	switch len(hits) + len(named) {
	case 1:
		finder.warnUnreadable(target, idFailed)
		if len(named) == 1 {
			return named[0].locator(), nil
		}
		return hits[0].path, nil
	case 0:
		missed := ""
		if len(idFailed) != 0 {
			missed = fmt.Sprintf("; %d places unreadable while resolving: %s", len(idFailed), joinErrors(idFailed))
		}
		titles := ""
		if finder.openCode.stores != 0 {
			titles = fmt.Sprintf(" and %d OpenCode session titles", len(sessions))
		}
		return "", runtimeFailure("NOT FOUND %s — searched %s for an id or prefix; the name lookup over %d session "+
			"files (the last %d KB of each)%s found no chat whose last title is %s (a seat name resolves to its id "+
			"with `pfm chat resolve %s`, third column)%s", target, finder.searched(), len(files), nameTail/1024, titles,
			pyvalue.ReprString(target), target, missed)
	}
	sort.Slice(hits, func(left, right int) bool { return hits[left].path < hits[right].path })
	rows := make([]string, len(hits))
	for row, found := range hits {
		last := "without a timestamp"
		if stamps := tailStamp.FindAllSubmatch(found.tail, -1); len(stamps) != 0 {
			last = string(stamps[len(stamps)-1][1])
		}
		size := "size unreadable"
		if info, err := os.Stat(found.path); err != nil {
			size += ": " + err.Error()
		} else {
			size = strconv.FormatInt(info.Size(), 10) + " bytes"
		}
		rows[row] = found.path + " (" + size + ", last record " + last + ")"
	}
	if len(named) != 0 {
		rows = append(rows, openCodeNameRows(named)...)
		sort.Strings(rows)
	}
	return "", runtimeFailure("AMBIGUOUS %s — %d transcripts named %s: %s", target, len(rows),
		pyvalue.ReprString(target), strings.Join(rows, " "))
}

// resolvedPath is path absolute with every symlink resolved, or absolute as
// it stands when it cannot be resolved.
func resolvedPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	if absolute, err := filepath.Abs(path); err == nil {
		return absolute
	}
	return path
}

// uniqueResolved is paths resolved, each once, in first-seen order.
func uniqueResolved(paths []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, path := range paths {
		path = resolvedPath(path)
		if !seen[path] {
			seen[path] = true
			out = append(out, path)
		}
	}
	return out
}

// joinErrors is errs' messages joined by spaces.
func joinErrors(errs []error) string {
	texts := make([]string, len(errs))
	for at, err := range errs {
		texts[at] = err.Error()
	}
	return strings.Join(texts, " ")
}
