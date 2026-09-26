// Package codexmeta reads bounded rollout identity metadata without indexing a
// transcript or depending on fleet state.
package codexmeta

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

type Kind uint8

const (
	Unknown Kind = iota
	User
	Subagent
)

type Fields struct {
	ID             string          `json:"id"`
	SessionID      string          `json:"session_id"`
	ParentThread   string          `json:"parent_thread"`
	ParentThreadID string          `json:"parent_thread_id"`
	ThreadSource   string          `json:"thread_source"`
	Source         json.RawMessage `json:"source"`
}

type Header struct {
	Fields
	Kind          Kind
	LineageParent string
}

func Decode(line []byte) (Header, error) {
	var record struct {
		Fields
		Type    string `json:"type"`
		Payload Fields `json:"payload"`
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
	}
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
			} `json:"thread_spawn"`
		}
		_ = json.Unmarshal(nested.Subagent, &spawned)
		fields.ParentThreadID = first(fields.ParentThreadID, spawned.ThreadSpawn.ParentThreadID)
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
	return Header{}, errors.New("rollout has no session_meta in first 20 records")
}

func first(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
