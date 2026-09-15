// Package recovery reconstructs the useful conversation in a Codex rollout.
//
// A rollout is append-only durable data. Codex's paginated history projection
// can still open a thread without showing that data, so recovery deliberately
// reads the rollout directly and writes a small replacement-seat bundle.
package recovery

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"hostops/pfm/internal/atomicfile"
)

var threadIDPattern = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

// Result describes one extraction and is also the machine-facing summary.
type Result struct {
	ThreadID  string
	Rollout   string
	Messages  int
	Carried   int
	Malformed int
}

type turn struct {
	stamp string
	role  string
	body  string
}

// Run locates target as either a readable rollout path or a thread id beneath
// codexRoot/sessions, then rebuilds all three recovered files from scratch.
func Run(ctx context.Context, codexRoot, target string) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(target) == "" {
		return Result{}, errors.New("a thread id or rollout path is required")
	}
	rollout, err := locate(codexRoot, target)
	if err != nil {
		return Result{}, err
	}
	threadID := threadIDPattern.FindString(filepath.Base(rollout))
	if threadID == "" {
		threadID = target
	}
	if strings.ContainsAny(threadID, `/\\`) || threadID == "." || threadID == ".." {
		return Result{}, fmt.Errorf("invalid thread id %q", threadID)
	}
	turns, carried, malformed, err := parse(ctx, rollout)
	if err != nil {
		return Result{}, err
	}
	out := filepath.Join(codexRoot, "recovered-"+threadID)
	if err := os.MkdirAll(out, 0o700); err != nil {
		return Result{}, fmt.Errorf("create recovery directory %q: %w", out, err)
	}
	if err := writeBundle(out, threadID, rollout, turns, carried); err != nil {
		return Result{}, err
	}
	return Result{
		ThreadID:  threadID,
		Rollout:   rollout,
		Messages:  len(turns),
		Carried:   len(carried),
		Malformed: malformed,
	}, nil
}

func locate(codexRoot, target string) (string, error) {
	if info, err := os.Stat(target); err == nil {
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("rollout is not a regular file: %s", target)
		}
		return target, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("stat rollout %q: %w", target, err)
	}
	sessions := filepath.Join(codexRoot, "sessions")
	var candidates []candidate
	err := filepath.WalkDir(sessions, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() || filepath.Ext(path) != ".jsonl" ||
			!strings.Contains(filepath.Base(path), target) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("stat candidate %q: %w", path, err)
		}
		candidates = append(candidates, candidate{path: path, mod: info.ModTime().UnixNano()})
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("no rollout found for thread %q under %s", target, sessions)
	}
	if err != nil {
		return "", fmt.Errorf("find rollouts under %q: %w", sessions, err)
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("no rollout found for thread %q under %s", target, sessions)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].mod != candidates[j].mod {
			return candidates[i].mod > candidates[j].mod
		}
		return candidates[i].path > candidates[j].path
	})
	return candidates[0].path, nil
}

type candidate struct {
	path string
	mod  int64
}

func parse(ctx context.Context, path string) ([]turn, []turn, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("open rollout %q: %w", path, err)
	}
	defer file.Close()
	var turns, carried []turn
	malformed := 0
	scanner := bufio.NewScanner(file)
	// A message can contain a large tool result. Scanner's default 64 KiB cap
	// would silently turn a valid rollout into a malformed one.
	scanner.Buffer(make([]byte, 64*1024), 32*1024*1024)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, nil, malformed, err
		}
		var record map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			malformed++
			continue
		}
		kind, _ := record["type"].(string)
		stamp, _ := record["timestamp"].(string)
		payload, _ := record["payload"].(map[string]any)
		switch {
		case kind == "compacted":
			items, _ := payload["replacement_history"].([]any)
			for _, raw := range items {
				item, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				body := textOf(item)
				if body != "" && !boilerplate(body) {
					role, _ := item["role"].(string)
					carried = append(carried, turn{stamp: stamp, role: roleOrUnknown(role), body: body})
				}
			}
		case kind == "response_item" && messagePayload(payload):
			role, _ := payload["role"].(string)
			body := textOf(payload)
			if (role == "user" || role == "assistant") && body != "" && !boilerplate(body) {
				turns = append(turns, turn{stamp: stamp, role: role, body: body})
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, malformed, fmt.Errorf("read rollout %q: %w", path, err)
	}
	return turns, carried, malformed, nil
}

func messagePayload(payload map[string]any) bool {
	kind, _ := payload["type"].(string)
	return kind == "message"
}

func roleOrUnknown(role string) string {
	if role == "" {
		return "?"
	}
	return role
}

func boilerplate(body string) bool {
	return strings.HasPrefix(body, "# AGENTS.md instructions") ||
		strings.HasPrefix(body, "<permissions instructions>")
}

func textOf(payload map[string]any) string {
	content, _ := payload["content"].([]any)
	parts := make([]string, 0, len(content))
	for _, raw := range content {
		chunk, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		value, _ := chunk["text"].(string)
		if strings.TrimSpace(value) != "" {
			parts = append(parts, value)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func writeBundle(out, threadID, rollout string, turns, carried []turn) error {
	if err := atomicfile.Write(
		filepath.Join(out, "transcript.md"),
		[]byte(transcript(threadID, rollout, turns)),
		0o600,
	); err != nil {
		return err
	}
	if err := atomicfile.Write(
		filepath.Join(out, "compaction-memory.md"),
		[]byte(memory(threadID, carried)),
		0o600,
	); err != nil {
		return err
	}
	if err := atomicfile.Write(
		filepath.Join(out, "brief.md"),
		[]byte(brief(out, threadID, rollout, turns, carried)),
		0o600,
	); err != nil {
		return err
	}
	return nil
}

func transcript(threadID, rollout string, turns []turn) string {
	var b strings.Builder
	fmt.Fprintf(
		&b,
		"# Recovered conversation — thread %s\n\n%d user/assistant messages, parsed from %s\n\n",
		threadID,
		len(turns),
		rollout,
	)
	for _, item := range turns {
		fmt.Fprintf(&b, "## %s · %s\n\n%s\n\n", item.role, item.stamp, item.body)
	}
	return b.String()
}

func memory(threadID string, carried []turn) string {
	var b strings.Builder
	fmt.Fprintf(
		&b,
		"# What survived compaction — thread %s\n\n%d messages carried through this thread's compactions.\n\n",
		threadID,
		len(carried),
	)
	for _, item := range carried {
		fmt.Fprintf(&b, "## %s · %s\n\n%s\n\n", item.role, item.stamp, item.body)
	}
	return b.String()
}

func brief(out, threadID, rollout string, turns, carried []turn) string {
	tail := turns
	if len(tail) > 12 {
		tail = tail[len(tail)-12:]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Recovery brief — thread %s\n\n", threadID)
	fmt.Fprintf(
		&b,
		"You are the seat for thread `%s`. Codex brought you up with no memory: this thread's\nhistory store could not supply it, so `codex resume` opened an empty session. Nothing was lost —\nyour rollout is complete and your conversation is reconstructed below.\n\n",
		threadID,
	)
	fmt.Fprintf(
		&b,
		"| File | Holds |\n|------|-------|\n| `%s/compaction-memory.md` | %d messages carried through your compactions — your condensed long-term state |\n| `%s/transcript.md` | all %d user/assistant messages, in order |\n| `%s` | the raw rollout this was parsed from |\n\n",
		out,
		len(carried),
		out,
		len(turns),
		rollout,
	)
	b.WriteString(
		"Read `compaction-memory.md` first, then the tail of `transcript.md` for the immediate position.\n\nA pane's emptiness is not evidence: the status line's context/token counts are what decide\nwhether a seat is warm.\n\n---\n\n",
	)
	b.WriteString(
		"Came up empty? Run `pfm chat recover " + threadID + "` again to rebuild this bundle from the rollout.\n\n",
	)
	fmt.Fprintf(&b, "## The last %d exchanges before the thread went dark\n\n", len(tail))
	for _, item := range tail {
		fmt.Fprintf(&b, "### %s · %s\n\n%s\n\n", item.role, item.stamp, item.body)
	}
	return b.String()
}
