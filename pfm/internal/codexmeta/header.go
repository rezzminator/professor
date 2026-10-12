// Package codexmeta reads bounded rollout identity metadata without indexing a
// transcript or depending on fleet state.
package codexmeta

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/rezzminator/professor/pfm/internal/jsvalue"
)

type Kind uint8

const (
	Unknown Kind = iota
	User
	Subagent
)

// Fields are the session_meta identity fields a rollout's first record
// carries, at its top level or under payload.
type Fields struct {
	ID             string          `json:"id"`
	SessionID      string          `json:"session_id"`
	ParentThread   string          `json:"parent_thread"`
	ParentThreadID string          `json:"parent_thread_id"`
	ThreadSource   string          `json:"thread_source"`
	Source         json.RawMessage `json:"source"`
	// CWD, Timestamp, AgentPath and ContextWindow are read loosely (extraFields):
	// a member of another type is absent, never a failed identity decode.
	CWD string `json:"-"`
	// Timestamp is the payload's thread start (RFC 3339, UTC); the record's
	// own top-level timestamp is the write time and never fills it.
	Timestamp string `json:"-"`
	// AgentPath is the orchestrator's name for a sub-agent ("/root/fix_1a"):
	// the payload's agent_path, else source.subagent.thread_spawn.agent_path.
	AgentPath     string `json:"-"`
	ContextWindow struct {
		WindowID string
	} `json:"-"`
}

// extraFields are the session_meta members only pfm tokens reads.
type extraFields struct {
	CWD           looseString `json:"cwd"`
	Timestamp     looseString `json:"timestamp"`
	AgentPath     looseString `json:"agent_path"`
	ContextWindow looseWindow `json:"context_window"`
}

// looseString is a member's value when it is a string, else "".
type looseString string

func (value *looseString) UnmarshalJSON(raw []byte) error {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		*value = looseString(text)
	}
	return nil
}

// looseWindow is context_window's window_id when it is an object holding one.
type looseWindow struct{ WindowID looseString }

func (window *looseWindow) UnmarshalJSON(raw []byte) error {
	var object struct {
		WindowID looseString `json:"window_id"`
	}
	if json.Unmarshal(raw, &object) == nil {
		window.WindowID = object.WindowID
	}
	return nil
}

// ErrNoSessionMeta is ReadHeader's answer for a rollout whose bounded prefix
// holds no session_meta record: the file was read, it names no thread.
var ErrNoSessionMeta = errors.New("rollout has no session_meta in its first 20 records")

// Role is the thread's role label: source when it is a string; when it is an
// object, its subagent member if that is a string, else its first member's
// value, else that member's key; else thread_source. "" when none is a string.
func (fields Fields) Role() string {
	var label string
	if json.Unmarshal(fields.Source, &label) == nil && label != "" {
		return label
	}
	if role := objectRole(fields.Source); role != "" {
		return role
	}
	return fields.ThreadSource
}

// objectRole reads source's object form in JavaScript member order, the order
// the Node engine's Object.values read.
func objectRole(source json.RawMessage) string {
	members, ok := jsvalue.Members(source)
	if !ok {
		return ""
	}
	for _, member := range members {
		if member.Key == "subagent" && string(member.Value) != "null" {
			var role string
			_ = json.Unmarshal(member.Value, &role)
			return role
		}
	}
	if len(members) == 0 {
		return ""
	}
	if first := members[0]; string(first.Value) != "null" {
		var role string
		_ = json.Unmarshal(first.Value, &role)
		return role
	}
	return members[0].Key
}

type Header struct {
	Fields
	Kind          Kind
	LineageParent string
}

func Decode(line []byte) (Header, error) {
	var record struct {
		Fields
		extraFields
		Type    string `json:"type"`
		Payload struct {
			Fields
			extraFields
		} `json:"payload"`
	}
	if err := json.Unmarshal(line, &record); err != nil {
		return Header{}, err
	}
	if record.Type != "session_meta" {
		return Header{}, errors.New("record is not session_meta")
	}
	fields := Fields{
		ID:        first(record.ID, record.Payload.ID),
		SessionID: first(record.SessionID, record.Payload.SessionID),
		ParentThread: first(
			record.ParentThread,
			record.Payload.ParentThread,
		),
		ParentThreadID: first(record.ParentThreadID, record.Payload.ParentThreadID),
		ThreadSource:   first(record.ThreadSource, record.Payload.ThreadSource),
		Source:         record.Source,
		CWD:            first(string(record.extraFields.CWD), string(record.Payload.extraFields.CWD)),
		Timestamp:      string(record.Payload.extraFields.Timestamp),
		AgentPath:      first(string(record.extraFields.AgentPath), string(record.Payload.extraFields.AgentPath)),
	}
	fields.ContextWindow.WindowID = first(
		string(record.extraFields.ContextWindow.WindowID),
		string(record.Payload.extraFields.ContextWindow.WindowID),
	)
	if len(fields.Source) == 0 {
		fields.Source = record.Payload.Source
	}
	var source string
	_ = json.Unmarshal(fields.Source, &source)
	var nested struct {
		Subagent json.RawMessage `json:"subagent"`
	}
	_ = json.Unmarshal(fields.Source, &nested)
	if len(nested.Subagent) > 0 {
		var spawned struct {
			ThreadSpawn struct {
				ParentThreadID string `json:"parent_thread_id"`
				AgentPath      string `json:"agent_path"`
			} `json:"thread_spawn"`
		}
		_ = json.Unmarshal(nested.Subagent, &spawned)
		fields.ParentThreadID = first(fields.ParentThreadID, spawned.ThreadSpawn.ParentThreadID)
		fields.AgentPath = first(fields.AgentPath, spawned.ThreadSpawn.AgentPath)
	}
	kind := Unknown
	switch {
	case fields.ThreadSource == "subagent" || len(nested.Subagent) > 0:
		kind = Subagent
	case fields.ThreadSource == "user":
		kind = User
	case fields.ThreadSource == "" && fields.ParentThread == "" && fields.ParentThreadID == "" && (source == "cli" || source == "vscode" || source == "exec" || source == "mcp"):
		kind = User
	}
	return Header{
		Fields: fields,
		Kind:   kind,
		LineageParent: first(
			record.SessionID,
			record.Payload.SessionID,
			record.ParentThread,
			record.ParentThreadID,
			record.Payload.ParentThread,
			record.Payload.ParentThreadID,
			fields.ParentThreadID,
		),
	}, nil
}

func ReadHeader(path string) (header Header, returnErr error) {
	file, err := os.Open(path)
	if err != nil {
		return Header{}, err
	}
	defer func() {
		if err := file.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close rollout %s: %w", path, err))
		}
	}()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for lines := 0; lines < 20 && scanner.Scan(); lines++ {
		var record struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return Header{}, fmt.Errorf("decode rollout metadata: %w", err)
		}
		if record.Type == "session_meta" {
			return Decode(scanner.Bytes())
		}
	}
	if err := scanner.Err(); err != nil {
		return Header{}, err
	}
	return Header{}, ErrNoSessionMeta
}

func first(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
