package tokens

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// roots are the transcript roots a scan walks: --root when given (each must
// resolve), else pfm's resolved Claude roots, each resolved and deduplicated.
func (a *auditor) roots() ([]string, error) {
	given := a.opts.roots
	candidates := given
	if len(given) == 0 {
		candidates = a.claudeRoots
	}
	seen, out := map[string]bool{}, []string{}
	for _, candidate := range candidates {
		resolved, err := filepath.Abs(candidate)
		if err == nil {
			resolved, err = filepath.EvalSymlinks(resolved)
		}
		if err != nil {
			if len(given) != 0 {
				return nil, failure("--root %s is not readable: %v", candidate, err)
			}
			continue
		}
		if !seen[resolved] {
			seen[resolved] = true
			out = append(out, resolved)
		}
	}
	if len(out) == 0 {
		return nil, failure("no transcript root found; pass --root DIR")
	}
	return out, nil
}

// ownership is the domain a call's owner is decided in and its transcripts,
// one per session path (a file is the same transcript under every root that
// holds it: the last three path segments name it).
func (a *auditor) ownership(file string) (domain, files []string, err error) {
	if file != "" && len(a.opts.roots) == 0 {
		// A timeline uses its enclosing transcript root: Claude encodes a cwd as a
		// dash-prefixed directory immediately below projects/.
		projectDir := filepath.Dir(file)
		if session, inside := transcript.SessionDir(file); inside {
			projectDir = filepath.Dir(session)
		}
		domain = []string{projectDir}
		if strings.HasPrefix(filepath.Base(projectDir), "-") {
			domain = []string{filepath.Dir(projectDir)}
		}
	} else if domain, err = a.roots(); err != nil {
		return nil, nil, err
	}
	var all []string
	for _, root := range domain {
		a.walk(root, &all)
	}
	var duplicates int
	files, duplicates = uniqueTranscripts(all)
	a.scan.dupFiles += duplicates
	a.transcripts, _ = uniqueTranscripts(a.transcripts)
	return domain, files, nil
}

// uniqueTranscripts keeps the same ownership root for replay and whole-session joins.
func uniqueTranscripts(paths []string) (files []string, duplicates int) {
	seen := map[string]bool{}
	for _, path := range paths {
		parts := strings.Split(path, string(filepath.Separator))
		key := strings.Join(parts[max(0, len(parts)-3):], "/")
		if seen[key] {
			duplicates++
			continue
		}
		seen[key] = true
		files = append(files, path)
	}
	return files, duplicates
}

// holder is one transcript holding a call.
type holder struct {
	session string
	named   map[string]bool
	fork    bool
}

// callOrigins names the owner of every call more than one transcript holds:
// the holder another copy names as its origin (session_id or forkedFrom),
// else the first holder not marked forkedFrom, in path order; "" when every
// holder is a marked fork copy and the origin lies outside the scan. Indexed
// before any replay, so attribution never depends on scan order; a
// session_id mismatch alone marks no copy.
func (a *auditor) callOrigins(files []string) map[string]string {
	holders := map[string]map[string]*holder{}
	var keys []string
	for _, file := range files {
		number := 0
		err := eachLine(file, func(raw []byte) {
			number++
			line, decoded := transcript.DecodeClaudeRecord(raw)
			if kind, _ := line.Kind(); !decoded || kind != "assistant" {
				return // malformed lines are counted by the replay
			}
			message, usage, usageErr := claudeMessage(&line)
			if usageErr != nil || usage == nil {
				return // usage gaps are counted once by collect, not the ownership index
			}
			key := callKey(&line, message, file, number)
			session := firstOf(line.SessionID)
			if session == "" {
				session = transcript.TranscriptStem(file)
			}
			var forked struct {
				SessionID json.RawMessage `json:"sessionId"`
			}
			if len(line.ForkedFrom) != 0 && line.ForkedFrom[0] == '{' {
				_ = json.Unmarshal(line.ForkedFrom, &forked)
			}
			named := firstOf(forked.SessionID, line.NamedSession)
			byFile := holders[key]
			if byFile == nil {
				byFile = map[string]*holder{}
				holders[key] = byFile
				keys = append(keys, key)
			}
			h := byFile[file]
			if h == nil {
				h = &holder{session: session, named: map[string]bool{}}
				byFile[file] = h
			}
			if named != "" && named != session {
				h.named[named] = true
			}
			if truthy(line.ForkedFrom) {
				h.fork = true
			}
		})
		if err != nil {
			a.scan.readErrors = append(a.scan.readErrors, file+": "+describe(err))
		}
	}
	owners := map[string]string{}
	for _, key := range keys {
		byFile := holders[key]
		if len(byFile) < 2 {
			continue
		}
		paths := make([]string, 0, len(byFile))
		named := map[string]bool{}
		for path, h := range byFile {
			paths = append(paths, path)
			for name := range h.named {
				named[name] = true
			}
		}
		sort.Strings(paths)
		owner := ""
		for _, path := range paths {
			if h := byFile[path]; !h.fork && named[h.session] {
				owner = path
				break
			}
		}
		if owner == "" {
			for _, path := range paths {
				if !byFile[path].fork {
					owner = path
					break
				}
			}
		}
		owners[key] = owner
	}
	return owners
}

// audit reads and replays one transcript; nil when it holds no counted call.
func (a *auditor) audit(file string, owners map[string]string) (*run, error) {
	collected, err := a.collect(file, owners)
	if err != nil {
		return nil, err
	}
	return a.replay(file, collected), nil
}

// collectRuns replays every transcript in the window once, the session
// selected, then folds sub-folder chats onto their shortest enclosing project.
func (a *auditor) collectRuns() error {
	domain, files, err := a.ownership("")
	if err != nil {
		return err
	}
	a.scan.roots = domain
	owners := a.callOrigins(files)
	session := a.opts.session
	skipped := 0
	var unreadable []string
	for _, file := range files {
		if session != "" && !strings.HasPrefix(transcript.TranscriptSession(file), session) {
			skipped++
			continue
		}
		a.scan.files++
		r, err := a.audit(file, owners)
		if err != nil {
			a.scan.readErrors = append(a.scan.readErrors, file+": "+describe(err))
			unreadable = append(unreadable, file+": "+describe(err))
			continue
		}
		if r != nil {
			a.runs = append(a.runs, r)
		}
	}
	// --session: the selector a sub-agent-orchestrated run HAS. A chat title exists only on a
	// main chat, so --family alone cannot select a family whose work an agent orchestrated.
	if session != "" {
		if len(a.runs) == 0 && len(a.scan.copiesOnly) == 0 {
			if a.scan.files != 0 {
				suffix := ""
				if len(unreadable) != 0 {
					suffix = "; unreadable: " + strings.Join(unreadable, "; ")
				}
				return failure("--session %s matched %d transcripts in this window but read no call from them%s",
					session, a.scan.files, suffix)
			}
			return usageError("--session %s matched none of the %d transcripts in this window; pass a session-id "+
				"prefix as it appears in the transcript path", session, len(files))
		}
		if len(a.runs) == 0 {
			var sids []string
			seen := map[string]bool{}
			for _, item := range a.scan.copiesOnly {
				if !seen[item.sid] {
					seen[item.sid] = true
					sids = append(sids, item.sid)
				}
			}
			a.scan.notes = append(a.scan.notes, "--session "+session+
				": matched only transcripts holding copied calls (billed to their origin): "+strings.Join(sids, ", "))
		} else {
			a.scan.notes = append(a.scan.notes, "--session "+session+": "+num(float64(skipped))+
				" transcripts outside that session not replayed; ownership read "+num(float64(len(files)))+" transcripts")
		}
	}
	// fold sub-folder chats onto the shortest enclosing project root (never onto a bare home dir)
	var projects []string
	seen := map[string]bool{}
	for _, r := range a.runs {
		if !seen[r.project] {
			seen[r.project] = true
			if r.project != "" && !isHome(r.project) {
				projects = append(projects, r.project)
			}
		}
	}
	sort.SliceStable(
		projects,
		func(left, right int) bool { return utf16Len(projects[left]) < utf16Len(projects[right]) },
	)
	for _, r := range a.runs {
		for _, root := range projects {
			if r.project == root || strings.HasPrefix(r.project, root+"/") {
				r.project = root
				break
			}
		}
	}
	return nil
}
