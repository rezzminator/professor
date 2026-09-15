package dream

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"hostops/pfm/internal/dream/lane"
	"hostops/pfm/internal/dream/organ"
	"hostops/pfm/internal/dream/resources"
)

type HookKind string

const (
	HookAgentInject         HookKind = "agent-inject"
	HookCodexSubagentInject HookKind = "codex-subagent-inject"
	HookNudge               HookKind = "nudge"
)

type HookRequest struct {
	Kind             HookKind
	Input            []byte
	ProjectDirectory string
	Now              time.Time
}

// Hook returns the hook's complete stdout bytes. An empty result is the
// ordinary no-surface/no-nudge outcome; malformed nonempty surfaces are loud.
func Hook(request HookRequest) ([]byte, error) {
	switch request.Kind {
	case HookAgentInject:
		return claudeHook(request.Input, request.ProjectDirectory)
	case HookCodexSubagentInject:
		return codexHook(request.Input)
	case HookNudge:
		return nudgeHook(request.ProjectDirectory, request.Now)
	default:
		return nil, fmt.Errorf("unknown dream hook %q", request.Kind)
	}
}

type orderedObject struct {
	rows []orderedField
}

type orderedField struct {
	key string
	raw json.RawMessage
}

func parseOrderedObject(raw []byte) (orderedObject, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil {
		return orderedObject{}, err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return orderedObject{}, errors.New("JSON value is not an object")
	}
	var result orderedObject
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return orderedObject{}, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return orderedObject{}, errors.New("JSON object key is not a string")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return orderedObject{}, err
		}
		result.rows = append(result.rows, orderedField{key: key, raw: append(json.RawMessage(nil), value...)})
	}
	if _, err := decoder.Token(); err != nil {
		return orderedObject{}, err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return orderedObject{}, errors.New("unexpected JSON after object")
		}
		return orderedObject{}, err
	}
	return result, nil
}

func (object orderedObject) raw(key string) ([]byte, bool) {
	for _, row := range object.rows {
		if row.key == key {
			return row.raw, true
		}
	}
	return nil, false
}

func (object orderedObject) text(key string) (string, bool) {
	raw, ok := object.raw(key)
	if !ok {
		return "", false
	}
	var value string
	if json.Unmarshal(raw, &value) != nil || value == "" {
		return "", false
	}
	return value, true
}

func (object orderedObject) setText(key, value string) orderedObject {
	encoded := encodeJSONString(value)
	copyObject := orderedObject{rows: append([]orderedField(nil), object.rows...)}
	for index := range copyObject.rows {
		if copyObject.rows[index].key == key {
			copyObject.rows[index].raw = encoded
			return copyObject
		}
	}
	copyObject.rows = append(copyObject.rows, orderedField{key: key, raw: encoded})
	return copyObject
}

func encodeJSONString(value string) []byte {
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
	return bytes.TrimSuffix(encoded.Bytes(), []byte("\n"))
}

func (object orderedObject) appendJSON(target *bytes.Buffer) {
	target.WriteByte('{')
	for index, row := range object.rows {
		if index > 0 {
			target.WriteByte(',')
		}
		key, _ := json.Marshal(row.key)
		target.Write(key)
		target.WriteByte(':')
		target.Write(row.raw)
	}
	target.WriteByte('}')
}

func claudeHook(input []byte, projectDirectory string) ([]byte, error) {
	if projectDirectory == "" {
		return nil, nil
	}
	root, err := parseOrderedObject(input)
	if err != nil {
		return nil, nil
	}
	toolRaw, ok := root.raw("tool_input")
	if !ok {
		return nil, nil
	}
	tool, err := parseOrderedObject(toolRaw)
	if err != nil {
		return nil, nil
	}
	agentType, typeOK := tool.text("subagent_type")
	prompt, promptOK := tool.text("prompt")
	if !typeOK || !promptOK {
		return nil, nil
	}
	hookContext, err := organ.ResolveHook(projectDirectory)
	if err != nil {
		return nil, nil
	}
	laneName, err := lane.FromAgentTypeIn(agentType, resources.NewResources(hookContext.Organ))
	if err != nil {
		return nil, nil
	}
	index := filepath.Join(hookContext.Organ, "agents", laneName+".md")
	surface, exists, err := readSurface(hookContext, index)
	if err != nil {
		return nil, err
	}
	if (!exists || surface == "") && laneName == "tracer" {
		surface, exists, err = readSurface(hookContext, filepath.Join(hookContext.Organ, "explorer-index.md"))
		if err != nil {
			return nil, err
		}
	}
	if !exists || surface == "" {
		return nil, nil
	}
	annotated, driftDown := annotateSurfaceDrift(hookContext.GitRoot, hookContext.Organ, surface)
	message := prompt + "\n\n" + surfacePreamble(hookContext.Organ) + "\n" + annotated + driftDown
	updated := tool.setText("prompt", message)

	var output bytes.Buffer
	output.WriteString(`{"hookSpecificOutput":{"hookEventName":"PreToolUse","updatedInput":`)
	updated.appendJSON(&output)
	output.WriteString("}}\n")
	return output.Bytes(), nil
}

func codexHook(input []byte) ([]byte, error) {
	root, err := parseOrderedObject(input)
	if err != nil {
		return nil, nil
	}
	agentType, typeOK := root.text("agent_type")
	cwd, cwdOK := root.text("cwd")
	if !typeOK || !cwdOK {
		return nil, nil
	}
	hookContext, err := organ.ResolveHook(cwd)
	if err != nil {
		return nil, nil
	}
	laneName, err := lane.FromAgentTypeIn(agentType, resources.NewResources(hookContext.Organ))
	if err != nil {
		return nil, nil
	}
	surface, exists, err := readSurface(hookContext, filepath.Join(hookContext.Organ, "agents", laneName+".md"))
	if err != nil {
		return nil, err
	}
	if !exists || surface == "" {
		return nil, nil
	}
	annotated, driftDown := annotateSurfaceDrift(hookContext.GitRoot, hookContext.Organ, surface)
	contextJSON := encodeJSONString(surfacePreamble(hookContext.Organ) + "\n" + annotated + driftDown)
	return append(
		append([]byte(`{"hookSpecificOutput":{"hookEventName":"SubagentStart","additionalContext":`), contextJSON...),
		[]byte("}}\n")...), nil
}

// surfacePreamble is the one instruction block both hook paths inject above
// the lane surface. It carries the consult-time repair duty: a DRIFTED map an
// agent actually opens is re-verified against its moved anchors and repaired
// on the spot, so the cache heals at first use instead of waiting for a sweep.
func surfacePreamble(organRoot string) string {
	return "Cached maps for this repository (bodies under " + organRoot +
		"/maps/). Consult a covering map before re-deriving its subject and cite it when used. " +
		"A DRIFTED mark names the moved anchors: if you open that map, re-verify its claims against those files at HEAD, then repair it — " +
		"claims intact → run `pfm dream restamp maps/{slug}.md`; " +
		"claims changed → rewrite the map body, then the same restamp; " +
		"cannot verify now → append a FLAG line to " + organRoot + "/stm.md:"
}

// annotateSurfaceDrift wraps lane.AnnotateDrift for both hook paths. A failed
// drift check must not render as freshness: the consumer sees an explicit
// check-down trailer instead of an unmarked surface, and the error reaches
// stderr for the operator.
func annotateSurfaceDrift(gitRoot, organRoot, surface string) (annotated, trailer string) {
	drift, err := lane.AnnotateDrift(gitRoot, filepath.Join(organRoot, "maps"), surface)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pfm dream hook: anchor-drift check failed for %s: %v\n", organRoot, err)
		return surface, "\n⚠ anchor-drift check FAILED — unmarked rows are NOT verified fresh; spot-check a map's anchors before trusting it."
	}
	return strings.TrimSuffix(drift.Surface, "\n"), ""
}

func readSurface(hookContext organ.HookContext, path string) (surface string, exists bool, err error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read lane surface %s: %w", path, err)
	}
	if len(raw) == 0 {
		return "", true, nil
	}
	validated, err := lane.ValidSurface(string(raw))
	if err != nil {
		migrated, migrationErr := migrateLegacySurface(hookContext, path)
		if migrationErr != nil {
			return "", true, errors.Join(
				fmt.Errorf("validate lane surface %s: %w", path, err),
				migrationErr,
			)
		}
		return migrated, true, nil
	}
	return validated, true, nil
}

var sweepFilename = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})(?:-(\d+))?\.md$`)

func nudgeHook(projectDirectory string, now time.Time) ([]byte, error) {
	if projectDirectory == "" {
		return nil, nil
	}
	hookContext, err := organ.ResolveNudge(projectDirectory)
	if err != nil {
		return nil, err
	}
	if info, err := os.Lstat(hookContext.Organ); err != nil ||
		info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, nil
	}
	failedPath := filepath.Join(organ.ScratchRoot(hookContext.Organ), "nudge.failed")
	output, checkErr := checkNudge(hookContext.Organ, now)
	if checkErr != nil {
		if err := writeNudgeFailure(failedPath, checkErr, now); err != nil {
			return nil, errors.Join(checkErr, err)
		}
		return nil, nil
	}
	if err := os.Remove(failedPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("clear nudge failure %s: %w", failedPath, err)
	}
	if output != "" {
		return []byte(output), nil
	}
	return nil, nil
}

func checkNudge(organRoot string, now time.Time) (string, error) {
	nightFailedPath := nightFailurePath(organRoot)
	var nightMarkerErr error
	if info, err := os.Lstat(nightFailedPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			nightMarkerErr = fmt.Errorf("night failure marker is not regular: %s", nightFailedPath)
		} else {
			return "🌙 dreamer-night failed — inspect " + nightFailedPath + "\n", nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		nightMarkerErr = fmt.Errorf("inspect night failure %s: %w", nightFailedPath, err)
	}

	failedPath := filepath.Join(organ.ScratchRoot(organRoot), "nudge.failed")
	if info, err := os.Lstat(failedPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return "", fmt.Errorf("nudge failure marker is not regular: %s", failedPath)
		}
		return "🌙 dreamer-night staleness check is broken — inspect " + failedPath + "\n", nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect nudge failure %s: %w", failedPath, err)
	}
	if nightMarkerErr != nil {
		return "", nightMarkerErr
	}

	dreamerDirectory := filepath.Join(organRoot, "dreamer")
	sweeps, err := os.ReadDir(dreamerDirectory)
	if err != nil {
		return "", fmt.Errorf("read dreamer directory %s: %w", dreamerDirectory, err)
	}
	type candidate struct {
		path, date string
		sequence   int
	}
	var candidates []candidate
	for _, entry := range sweeps {
		match := sweepFilename.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}
		if !entry.Type().IsRegular() {
			return "", fmt.Errorf("sweep is not a regular file: %s", filepath.Join(dreamerDirectory, entry.Name()))
		}
		sequence := 1
		if match[2] != "" {
			sequence, err = strconv.Atoi(match[2])
			if err != nil {
				return "", fmt.Errorf("parse sweep sequence %s: %w", entry.Name(), err)
			}
		}
		path := filepath.Join(dreamerDirectory, entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read sweep %s: %w", path, err)
		}
		if containsExactLine(string(raw), "END-OF-SWEEP") {
			candidates = append(candidates, candidate{path: path, date: match[1], sequence: sequence})
		}
	}
	if len(candidates) == 0 {
		return "", nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].date != candidates[j].date {
			return candidates[i].date < candidates[j].date
		}
		return candidates[i].sequence < candidates[j].sequence
	})
	latest := candidates[len(candidates)-1]
	raw, err := os.ReadFile(latest.path)
	if err != nil {
		return "", fmt.Errorf("read newest sweep %s: %w", latest.path, err)
	}
	appliedText := ""
	for _, row := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(row, "Applied: ") {
			appliedText = strings.TrimPrefix(row, "Applied: ")
		}
	}
	applied, parseErr := parseNudgeTime(appliedText)
	if parseErr != nil {
		applied, parseErr = time.ParseInLocation("2006-01-02", latest.date, now.Location())
	}
	if parseErr != nil {
		return "", fmt.Errorf("parse newest sweep time %s: %w", latest.path, parseErr)
	}
	age := now.Sub(applied)
	if age <= 48*time.Hour {
		return "", nil
	}
	return fmt.Sprintf(
		"🌙 dreamer-night stale — newest applied sweep is %dd old; run /dreamer\n",
		int64(age/(24*time.Hour)),
	), nil
}

func writeNudgeFailure(path string, cause error, now time.Time) error {
	directory := filepath.Dir(path)
	if info, err := os.Lstat(directory); errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(directory, 0o700); err != nil {
			return fmt.Errorf("create nudge failure directory: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("inspect nudge failure directory: %w", err)
	} else if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("nudge failure directory is not a real directory: %s", directory)
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("nudge failure marker is not a regular non-symlink file: %s", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect nudge failure marker %s: %w", path, err)
	}
	body := now.Format(time.RFC3339) + "\t" + strings.ReplaceAll(cause.Error(), "\n", " ") + "\n"
	temporary, err := os.CreateTemp(filepath.Dir(path), ".nudge.failed.*")
	if err != nil {
		return fmt.Errorf("create nudge failure marker beside %s: %w", path, err)
	}
	temporaryPath := temporary.Name()
	clean := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}
	if err := temporary.Chmod(0o600); err != nil {
		clean()
		return fmt.Errorf("set nudge failure marker mode: %w", err)
	}
	if _, err := temporary.WriteString(body); err != nil {
		clean()
		return fmt.Errorf("write nudge failure marker: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		clean()
		return fmt.Errorf("sync nudge failure marker: %w", err)
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("close nudge failure marker: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("write nudge failure %s: %w", path, err)
	}
	return nil
}

func parseNudgeTime(value string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05Z07:00"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported time %q", value)
}

func containsExactLine(body, want string) bool {
	for _, row := range strings.Split(body, "\n") {
		if row == want {
			return true
		}
	}
	return false
}
