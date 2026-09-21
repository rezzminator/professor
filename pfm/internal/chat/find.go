package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

var (
	// ErrNoExcerpt: the excerpt holds nothing to search for.
	ErrNoExcerpt = errors.New("excerpt has no line of 20+ characters to search for")
	// ErrNoTranscriptRegistry: no Claude transcript root exists to search.
	ErrNoTranscriptRegistry = errors.New("no transcript registry is available")
	// ErrNoExcerptMatch: the registry was searched and no transcript holds the
	// excerpt — an answer, distinct from a registry that could not be read.
	// It names its own scope: only Claude transcripts are searched, so a Codex
	// or OpenCode chat holding the text is "not looked at", never "not there".
	ErrNoExcerptMatch = errors.New(
		"no session contains the excerpt in the Claude transcripts searched " +
			"(Codex and OpenCode sessions are not searched); try a longer or more distinctive chunk",
	)
)

// TranscriptMatch is one Claude transcript an excerpt was found in: how many
// of the excerpt's needles it holds, and the first and last timestamps it
// records.
type TranscriptMatch struct {
	ID, Path, First, Last string
	Hits, Needles         int
}

// FindRequest asks which transcripts hold an excerpt. Self is the asking
// session's id, whose own transcript is left out (an excerpt copied from it
// always matches it); the surface supplies it — AskingSession where its
// process runs inside the asking chat — and "" leaves nothing out.
type FindRequest struct {
	Excerpt string
	Self    string
}

// Find is the shared excerpt search behind `pfm chat find`, `pfm chat read
// <excerpt-file>`, resume-by-excerpt and MCP chat_find: every match, most
// needles first, then by path. Each surface trims the list to its own
// contract; none re-ranks it.
func Find(ctx context.Context, runtime *pfmconfig.Runtime, request FindRequest) ([]TranscriptMatch, error) {
	needles := ExcerptNeedles(request.Excerpt)
	if len(needles) == 0 {
		return nil, ErrNoExcerpt
	}
	files, err := ClaudeTranscripts(runtime)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, ErrNoTranscriptRegistry
	}
	self := request.Self
	var matches []TranscriptMatch
	for _, path := range files {
		id := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		if self != "" && id == self {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read transcript %s: %w", path, err)
		}
		hits := 0
		for _, needle := range needles {
			encoded, _ := json.Marshal(needle)
			if len(encoded) >= 2 && bytes.Contains(raw, encoded[1:len(encoded)-1]) {
				hits++
			}
		}
		if hits == 0 {
			continue
		}
		first, last := transcriptRange(raw)
		matches = append(matches, TranscriptMatch{
			ID: id, Path: path, First: first, Last: last, Hits: hits, Needles: len(needles),
		})
	}
	if len(matches) == 0 {
		return nil, ErrNoExcerptMatch
	}
	sort.Slice(matches, func(left, right int) bool {
		if matches[left].Hits != matches[right].Hits {
			return matches[left].Hits > matches[right].Hits
		}
		return matches[left].Path < matches[right].Path
	})
	return matches, nil
}

// AskingSession is the Claude session this process runs in
// (CLAUDE_CODE_SESSION_ID), or "" outside one — the Self a surface whose
// process is the asking chat hands Find.
func AskingSession() string {
	return (paths.OSEnv{}).Get("CLAUDE_CODE_SESSION_ID")
}

// ExcerptNeedles splits an excerpt into the needles Find searches for: its
// five longest lines of 20+ characters, quote and list markers stripped — or
// the whole trimmed excerpt when no line is that long.
func ExcerptNeedles(value string) []string {
	var candidates []string
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSuffix(line, "\r")
		line = strings.TrimLeftFunc(line, func(r rune) bool {
			return unicode.IsSpace(r) || strings.ContainsRune(">#*-", r)
		})
		line = strings.TrimRightFunc(line, unicode.IsSpace)
		if len(line) >= 20 {
			candidates = append(candidates, line)
		}
	}
	sort.SliceStable(candidates, func(left, right int) bool {
		return len(candidates[left]) > len(candidates[right])
	})
	if len(candidates) > 5 {
		candidates = candidates[:5]
	}
	if len(candidates) == 0 {
		if query := strings.TrimSpace(value); query != "" {
			candidates = []string{query}
		}
	}
	return candidates
}

// ClaudeTranscripts lists every Claude transcript under the runtime's Claude
// roots, sorted, each distinct file once under the first root that reaches it.
// Without a config-file account list — or with no runtime at
// all — the legacy primary root (~/.claude/projects) is searched first too.
func ClaudeTranscripts(runtime *pfmconfig.Runtime) ([]string, error) {
	var resolved paths.Values
	includeLegacyPrimary := true
	if runtime != nil {
		resolved = runtime.Paths
		includeLegacyPrimary = runtime.Config.Source("accounts") != pfmconfig.SourceFile
	} else {
		var err error
		resolved, err = paths.Resolve()
		if err != nil {
			return nil, err
		}
	}
	roots := append([]string(nil), resolved.Roots[pfmengine.Claude]...)
	if includeLegacyPrimary {
		roots = append([]string{filepath.Join(resolved.Home, ".claude", "projects")}, roots...)
	}
	seen := make(map[string]struct{})
	var files []string
	for _, root := range roots {
		projects, err := os.ReadDir(root)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read transcript registry %s: %w", root, err)
		}
		for _, project := range projects {
			if !project.IsDir() {
				continue
			}
			directory := filepath.Join(root, project.Name())
			entries, err := os.ReadDir(directory)
			if err != nil {
				return nil, fmt.Errorf("read transcript project %s: %w", directory, err)
			}
			for _, entry := range entries {
				if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
					continue
				}
				path := filepath.Join(directory, entry.Name())
				// Seat roots commonly share ONE projects directory by
				// symlink, so the same transcript is reachable under every
				// root: key by the resolved file, not by its spelling, or one
				// session is listed once per root. A path that will not
				// resolve (a dangling link) keys by its literal self — it is
				// still listed once, never dropped and never an error for the
				// whole search.
				key := path
				if physical, err := filepath.EvalSymlinks(path); err == nil {
					key = physical
				}
				if _, exists := seen[key]; exists {
					continue
				}
				seen[key] = struct{}{}
				files = append(files, path)
			}
		}
	}
	sort.Strings(files)
	return files, nil
}

// transcriptRange is the first and last record timestamps in a transcript.
func transcriptRange(raw []byte) (string, string) {
	var first, last string
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		var record struct {
			Timestamp string `json:"timestamp"`
		}
		if json.Unmarshal(line, &record) == nil && record.Timestamp != "" {
			if first == "" {
				first = record.Timestamp
			}
			last = record.Timestamp
		}
	}
	return first, last
}
