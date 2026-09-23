// Package corpussan owns the callmeter test-corpus sanitizer: it turns a real
// Claude Code session transcript ({session}.jsonl, and every
// {session}/subagents/agent-*.jsonl and .meta.json beside it) into a fixture
// safe to commit, while everything callmeter's backfill reads keeps its
// meaning.
//
// What survives: numbers, booleans and nulls; object keys and array shapes;
// ids, types, tool names, models, statuses and timestamps (keepKeys). What is
// rewritten: paths and commands (scrubKeys) go through scrubText — the project
// root becomes /tmp/demo-proj, every other absolute path lands under
// /tmp/demo-home, and every word not on the allowlist becomes filler of its
// own length. Everything else — prose, thinking, prompts, descriptions, file
// contents, tool output — becomes filler of the same byte length (fillerOf),
// so every byte measure backfill takes is unchanged. The same input gives
// byte-identical output.
package corpussan

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Options name the machine the transcript was recorded on.
type Options struct {
	Project string   // absolute project root; its paths become /tmp/demo-proj/…
	Home    string   // absolute home dir; paths outside the project land under /tmp/demo-home/…
	Allow   []string // words kept in paths and commands, besides the built-in list
	Deny    []string // words never kept, whatever allows them (case-insensitive substring)
}

// Sanitizer rewrites transcript lines under one set of Options.
type Sanitizer struct {
	prefixes []prefix // absolute and encoded roots, longest first
	allow    map[string]bool
	deny     []string // lower-case
}

// NewSanitizer validates o. The home dir's own path components (the user name among
// them) are always denied.
func NewSanitizer(o Options) (*Sanitizer, error) {
	if !filepath.IsAbs(o.Project) || !filepath.IsAbs(o.Home) {
		return nil, fmt.Errorf("corpussan: project %q and home %q must both be absolute", o.Project, o.Home)
	}
	project, home := filepath.Clean(o.Project), filepath.Clean(o.Home)
	if project == "/" || home == "/" {
		return nil, fmt.Errorf("corpussan: project %q or home %q is the filesystem root", project, home)
	}
	s := &Sanitizer{allow: map[string]bool{}}
	for _, w := range builtinWords {
		s.allow[w] = true
	}
	for _, w := range o.Allow {
		s.allow[w] = true
	}
	deny := append(strings.Split(strings.Trim(home, "/"), "/"), o.Deny...)
	for _, d := range deny {
		if d = strings.ToLower(strings.TrimSpace(d)); len(d) >= 3 {
			s.deny = append(s.deny, d)
		}
	}
	s.prefixes = newPrefixes(project, home)
	return s, nil
}

// Session sanitizes the transcript at src and every file of its subagents/
// directory into outDir, as {outDir}/{session}.jsonl and
// {outDir}/{session}/subagents/…, and returns the paths it wrote. It writes
// nothing until every input is sanitized, and never overwrites a file.
func (s *Sanitizer) Session(src, outDir string) ([]string, error) {
	base := filepath.Base(src)
	session, ok := strings.CutSuffix(base, ".jsonl")
	if !ok {
		return nil, fmt.Errorf("corpussan: %s is not a .jsonl transcript", src)
	}
	type job struct{ in, out string }
	jobs := []job{{src, filepath.Join(outDir, base)}}
	subDir := filepath.Join(filepath.Dir(src), session, "subagents")
	entries, err := os.ReadDir(subDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("corpussan: read %s: %w", subDir, err)
	}
	for _, e := range entries { // os.ReadDir sorts by name
		name := e.Name()
		if e.Type().IsRegular() && (strings.HasSuffix(name, ".jsonl") || strings.HasSuffix(name, ".meta.json")) {
			jobs = append(jobs, job{filepath.Join(subDir, name), filepath.Join(outDir, session, "subagents", name)})
		}
	}
	outputs := make([][]byte, len(jobs))
	for i, j := range jobs {
		raw, err := os.ReadFile(j.in)
		if err != nil {
			return nil, fmt.Errorf("corpussan: %w", err)
		}
		if strings.HasSuffix(j.in, ".jsonl") {
			outputs[i], err = s.lines(raw)
		} else {
			outputs[i], err = s.Line(raw)
		}
		if err != nil {
			return nil, fmt.Errorf("corpussan: %s %w", j.in, err)
		}
	}
	written := make([]string, 0, len(jobs))
	for i, j := range jobs {
		if err := writeNew(j.out, outputs[i]); err != nil {
			return written, err
		}
		written = append(written, j.out)
	}
	return written, nil
}

// lines sanitizes a JSONL body line by line; an empty line stays empty and
// the final newline stays as the input had it.
func (s *Sanitizer) lines(raw []byte) ([]byte, error) {
	var out bytes.Buffer
	for n := 1; len(raw) > 0; n++ {
		line, rest, found := bytes.Cut(raw, []byte("\n"))
		raw = rest
		if len(line) > 0 {
			clean, err := s.Line(line)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", n, err)
			}
			out.Write(clean)
		}
		if found {
			out.WriteByte('\n')
		}
	}
	return out.Bytes(), nil
}

// Line sanitizes one JSON value; anything but exactly one value is an error.
func (s *Sanitizer) Line(raw []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out bytes.Buffer
	if err := s.value(dec, "", &out); err != nil {
		return nil, fmt.Errorf("not JSON: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("not JSON: trailing data after the value (%v)", err)
	}
	return out.Bytes(), nil
}

// value copies the next JSON value from dec to out; key is the object key the
// value sits under (inherited by array elements), which picks its treatment.
func (s *Sanitizer) value(dec *json.Decoder, key string, out *bytes.Buffer) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	switch t := tok.(type) {
	case json.Delim:
		return s.container(dec, t, key, out)
	case string:
		return writeJSONString(out, s.stringValue(key, t))
	case json.Number:
		out.WriteString(t.String())
	case bool:
		fmt.Fprint(out, t)
	case nil:
		out.WriteString("null")
	default:
		return fmt.Errorf("unexpected token %v", tok)
	}
	return nil
}

func (s *Sanitizer) container(dec *json.Decoder, open json.Delim, key string, out *bytes.Buffer) error {
	if open != '{' && open != '[' {
		return fmt.Errorf("unexpected %v", open)
	}
	out.WriteByte(byte(open))
	for i := 0; dec.More(); i++ {
		if i > 0 {
			out.WriteByte(',')
		}
		child := key
		if open == '{' {
			tok, err := dec.Token()
			if err != nil {
				return err
			}
			k, ok := tok.(string)
			if !ok {
				return fmt.Errorf("object key %v is not a string", tok)
			}
			if err := writeJSONString(out, s.objectKey(k)); err != nil {
				return err
			}
			out.WriteByte(':')
			child = k
		}
		if err := s.value(dec, child, out); err != nil {
			return err
		}
	}
	if _, err := dec.Token(); err != nil { // the closing delimiter
		return err
	}
	if open == '{' {
		out.WriteByte('}')
	} else {
		out.WriteByte(']')
	}
	return nil
}

// stringValue picks a string's treatment by the key it sits under: kept, a
// path or command scrubbed, or filler.
func (s *Sanitizer) stringValue(key, v string) string {
	switch {
	case keepKeys[key] && safeToken(v) && !s.denied(v):
		return v
	case scrubKeys[key]:
		return s.scrubText(v)
	default:
		return fillerOf(v)
	}
}

// objectKey keeps a key unless it reads like a path or holds a denied word.
func (s *Sanitizer) objectKey(k string) string {
	if strings.ContainsAny(k, "/~") || s.denied(k) {
		return s.scrubText(k)
	}
	return k
}

func (s *Sanitizer) denied(v string) bool {
	low := strings.ToLower(v)
	for _, d := range s.deny {
		if strings.Contains(low, d) {
			return true
		}
	}
	return false
}

// safeToken is a short value with no space, slash or quote: an id, an enum, a
// model name, a timestamp.
func safeToken(v string) bool {
	if len(v) > 120 {
		return false
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		if (!isRunByte(c) || c >= 0x80) && !strings.ContainsRune(".:+@-", rune(c)) {
			return false
		}
	}
	return true
}

func writeJSONString(out *bytes.Buffer, v string) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	out.Write(bytes.TrimSuffix(buf.Bytes(), []byte("\n")))
	return nil
}

// writeNew creates path and its parents and writes raw, refusing a path that
// already exists.
func writeNew(path string, raw []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("corpussan: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("corpussan: %w", err)
	}
	if _, err := f.Write(raw); err != nil {
		return errors.Join(fmt.Errorf("corpussan: write %s: %w", path, err), f.Close())
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("corpussan: close %s: %w", path, err)
	}
	return nil
}

// keepKeys hold ids, enums, names and timestamps: kept verbatim when the value
// is a safeToken.
var keepKeys = setOf(
	"type", "role", "name", "id", "uuid", "parentUuid", "leafUuid", "logicalParentUuid",
	"tool_use_id", "toolUseId", "toolUseID", "sourceToolUseID", "sourceToolAssistantUUID",
	"sessionId", "session_id", "agentId", "agent_id", "parentAgentId", "requestId", "promptId", "messageId",
	"timestamp", "model", "resolvedModel", "agentType", "agent_type", "subagent_type", "status",
	"stop_reason", "stop_sequence", "service_tier", "inference_geo", "speed", "userType", "entrypoint",
	"version", "hookEvent", "hookName", "hook_event_name", "operation", "mode", "effort", "output_mode",
	"subtype", "level", "permissionMode",
)

// scrubKeys hold paths, commands and search patterns: rewritten by scrubText.
var scrubKeys = setOf(
	"cwd", "file_path", "filePath", "path", "notebook_path", "command", "pattern", "glob", "filenames",
	"workingDirectory", "scratchpadDirectory", "persistedOutputPath", "outputFile", "transcript_path",
	"agent_transcript_path", "directory",
)

func setOf(words ...string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}
