package seat

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/headless"
	"hostops/pfm/internal/paths"
)

var errRolloutNotReady = errors.New("rollout session metadata is not complete yet")

// RolloutSnapshot is the set of Codex rollouts that existed before a seat
// started. A seat can never resolve to one of them.
type RolloutSnapshot struct {
	paths map[string]struct{}
}

// RolloutMatch contains the independent facts a new rollout must match.
type RolloutMatch struct {
	Name    string
	CWD     string
	Socket  string
	Session string
}

// RolloutLocator discovers a seat transcript without fleet.db. Snapshot is
// taken before spawn; Locate may be polled while Codex creates and names the
// new rollout.
type RolloutLocator interface {
	Snapshot(context.Context) (RolloutSnapshot, error)
	Locate(context.Context, RolloutSnapshot, RolloutMatch) (headless.Chat, bool, error)
}

// FilesystemRolloutLocator reads only Codex's session files and append-only
// name index. It never opens pfm's derived database.
type FilesystemRolloutLocator struct {
	CodexRoot string
}

// NewFilesystemRolloutLocator resolves only path names; paths.Resolve does no
// filesystem I/O and this constructor does not touch fleet.db.
func NewFilesystemRolloutLocator() (FilesystemRolloutLocator, error) {
	values, err := paths.Resolve()
	if err != nil {
		return FilesystemRolloutLocator{}, err
	}
	return FilesystemRolloutLocator{CodexRoot: values.FirstRoot(pfmengine.Codex)}, nil
}

func (locator FilesystemRolloutLocator) Snapshot(
	ctx context.Context,
) (RolloutSnapshot, error) {
	rollouts, err := locator.rollouts(ctx)
	if err != nil {
		return RolloutSnapshot{}, err
	}
	snapshot := RolloutSnapshot{paths: make(map[string]struct{}, len(rollouts))}
	for _, path := range rollouts {
		snapshot.paths[path] = struct{}{}
	}
	return snapshot, nil
}

func (locator FilesystemRolloutLocator) Locate(
	ctx context.Context,
	snapshot RolloutSnapshot,
	match RolloutMatch,
) (headless.Chat, bool, error) {
	if match.Name == "" || match.CWD == "" || match.Socket == "" || match.Session == "" {
		return headless.Chat{}, false, errors.New("rollout match requires name, directory, socket and session")
	}
	rollouts, err := locator.rollouts(ctx)
	if err != nil {
		return headless.Chat{}, false, err
	}
	names, err := locator.sessionNames(ctx)
	if err != nil {
		return headless.Chat{}, false, err
	}

	type candidate struct {
		path string
		meta rolloutMeta
	}
	var candidates []candidate
	for _, path := range rollouts {
		if _, old := snapshot.paths[path]; old {
			continue
		}
		meta, err := readRolloutMeta(path)
		if errors.Is(err, errRolloutNotReady) {
			continue
		}
		if err != nil {
			return headless.Chat{}, false, fmt.Errorf("read new Codex rollout %s: %w", path, err)
		}
		if filepath.Clean(meta.CWD) != filepath.Clean(match.CWD) {
			continue
		}
		if names[meta.ID] != match.Name {
			continue
		}
		candidates = append(candidates, candidate{path: path, meta: meta})
	}
	if len(candidates) == 0 {
		return headless.Chat{}, false, nil
	}
	if len(candidates) != 1 {
		candidatePaths := make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			candidatePaths = append(candidatePaths, candidate.path)
		}
		sort.Strings(candidatePaths)
		return headless.Chat{}, false, fmt.Errorf(
			"ambiguous rollout for seat %q in %s: %s",
			match.Name,
			match.CWD,
			strings.Join(candidatePaths, ", "),
		)
	}
	selected := candidates[0]
	return headless.Chat{
		Name:    match.Name,
		ID:      selected.meta.ID,
		Engine:  pfmengine.Codex,
		Path:    selected.path,
		CWD:     selected.meta.CWD,
		Socket:  match.Socket,
		Session: match.Session,
	}, true, nil
}

func (locator FilesystemRolloutLocator) rollouts(ctx context.Context) ([]string, error) {
	if locator.CodexRoot == "" {
		return nil, errors.New("root for Codex is empty")
	}
	root := filepath.Join(locator.CodexRoot, "sessions")
	var result []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrNotExist) && path == root {
				return fs.SkipDir
			}
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if strings.HasPrefix(entry.Name(), "rollout-") &&
			strings.HasSuffix(entry.Name(), ".jsonl") {
			result = append(result, path)
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("enumerate Codex rollouts: %w", err)
	}
	sort.Strings(result)
	return result, nil
}

type rolloutMeta struct {
	ID  string
	CWD string
}

func readRolloutMeta(path string) (meta rolloutMeta, returnErr error) {
	file, err := os.Open(path)
	if err != nil {
		return rolloutMeta{}, err
	}
	defer func() {
		if err := file.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close rollout %s: %w", path, err))
		}
	}()

	reader := bufio.NewReaderSize(file, 64<<10)
	line, err := reader.ReadBytes('\n')
	if err == io.EOF {
		// Codex creates the rollout before it appends the first complete JSONL
		// record. That is a pending file, not malformed evidence. Once a newline
		// lands, the record is parsed strictly on the next poll.
		return rolloutMeta{}, errRolloutNotReady
	}
	if err != nil && err != io.EOF {
		return rolloutMeta{}, err
	}
	if len(line) == 0 {
		return rolloutMeta{}, errors.New("empty rollout")
	}
	var record struct {
		Type    string `json:"type"`
		Payload struct {
			ID  string `json:"id"`
			CWD string `json:"cwd"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(line, &record); err != nil {
		return rolloutMeta{}, fmt.Errorf("decode session metadata: %w", err)
	}
	if record.Type != "session_meta" || record.Payload.ID == "" || record.Payload.CWD == "" {
		return rolloutMeta{}, errors.New("first rollout record is not complete session_meta")
	}
	return rolloutMeta{ID: record.Payload.ID, CWD: record.Payload.CWD}, nil
}

func (locator FilesystemRolloutLocator) sessionNames(
	ctx context.Context,
) (map[string]string, error) {
	path := filepath.Join(locator.CodexRoot, "session_index.jsonl")
	content, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Codex session name index: %w", err)
	}
	// The index is append-only. Ignore only its unterminated final fragment;
	// every complete line remains strict. This makes a concurrent append a
	// pending rename rather than a false corruption verdict.
	lastNewline := strings.LastIndexByte(string(content), '\n')
	if lastNewline < 0 {
		return map[string]string{}, nil
	}
	content = content[:lastNewline+1]

	names := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line := scanner.Bytes()
		if strings.TrimSpace(string(line)) == "" {
			continue
		}
		var row struct {
			ID   string `json:"id"`
			Name string `json:"thread_name"`
		}
		if err := json.Unmarshal(line, &row); err != nil {
			return nil, fmt.Errorf("decode Codex session name index line %d: %w", lineNumber, err)
		}
		if row.ID == "" || row.Name == "" {
			return nil, fmt.Errorf("session-name index line %d for Codex lacks id or thread_name", lineNumber)
		}
		names[row.ID] = row.Name
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read Codex session name index: %w", err)
	}
	return names, nil
}
