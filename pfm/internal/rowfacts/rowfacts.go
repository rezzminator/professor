// Package rowfacts reads, for each chat row of the picker, the facts the fleet
// index does not hold: which model it runs and at what effort, whether it is
// mid-turn right now, and how many sub-agents are working for it.
//
// Every fact comes from the tail of a transcript or rollout, never the whole
// file: a transcript is megabytes and the picker asks about every row on every
// refresh. A Reader caches what it learned per file, keyed by size and modified
// time, so a refresh that finds a file unchanged reads nothing, and one that
// finds it grown reads one bounded tail.
//
// A fact it could not read is absent, never invented: an unreadable file leaves
// the row with no model, no effort and not working, and the failure is returned
// beside the rows so the caller can say so.
package rowfacts

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"
	"time"

	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/statusline"
)

const (
	// workingFreshNS is how recently a transcript must have been written for a
	// turn that looks open to still count as working. A tool that runs silent
	// for longer than this reads as idle: the transcript is the only evidence,
	// and a chat that wrote nothing for three minutes is indistinguishable from
	// one that was closed mid-turn.
	workingFreshNS = int64(180 * time.Second)
	// agentFreshNS is the same bound for one sub-agent's transcript.
	agentFreshNS = int64(90 * time.Second)
	// maxAgents bounds how many agent transcripts one chat is asked about.
	maxAgents = 32
)

// Facts is what the picker shows beside a chat that its row does not carry.
type Facts struct {
	// Model is the model id as the engine reports it, "" when unknown.
	Model string
	// Effort is the effort level, "" when unknown.
	Effort string
	// Working is true while the chat itself is mid-turn.
	Working bool
	// AgentsWorking counts the chat's sub-agents mid-turn right now.
	AgentsWorking int
}

// Reader reads and remembers row facts. It is safe for concurrent use.
type Reader struct {
	sidDir string

	mutex sync.Mutex
	files map[string]cachedFile
}

// cachedFile is what one file's tail said, valid while its size and modified
// time still match.
type cachedFile struct {
	size    int64
	modNS   int64
	model   string
	effort  string
	working bool
}

// NewReader returns a Reader that finds the Claude statusline's per-session
// records in sidDir.
func NewReader(sidDir string) *Reader {
	return &Reader{sidDir: sidDir, files: make(map[string]cachedFile)}
}

// Enrich returns rows with the facts filled in, and the failures met on the
// way. Rows are cloned only when one changed; the caller's slice is never
// written. A row of a kind that carries no facts is passed through untouched.
func (reader *Reader) Enrich(rows []compose.Row, nowNS int64) ([]compose.Row, []error) {
	var failures []error
	out := rows
	cloned := false
	for index := range rows {
		row := &rows[index]
		facts, err := reader.factsFor(row, nowNS)
		if err != nil {
			failures = append(failures, fmt.Errorf("row %q: %w", row.Name, err))
		}
		if facts == (Facts{}) {
			continue
		}
		if !cloned {
			out = append([]compose.Row(nil), rows...)
			cloned = true
		}
		out[index].Model = facts.Model
		out[index].Effort = facts.Effort
		out[index].Working = facts.Working
		out[index].AgentsWorking = facts.AgentsWorking
	}
	return out, failures
}

// factsFor reads one row's facts.
func (reader *Reader) factsFor(row *compose.Row, nowNS int64) (Facts, error) {
	if row.Path == "" {
		return Facts{}, nil
	}
	switch row.Kind {
	case compose.LiveClaude, compose.ResumeClaude:
		return reader.claudeFacts(row, nowNS)
	case compose.LiveCodex, compose.ResumeCodex:
		return reader.codexFacts(row, nowNS)
	}
	return Facts{}, nil
}

// load returns a file's cached tail facts, reading them through read when the
// file moved. A file that is gone is not an error here — a row whose transcript
// was swept simply has no facts.
func (reader *Reader) load(path string, read func(string) (cachedFile, error)) (cachedFile, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return cachedFile{}, nil
	}
	if err != nil {
		return cachedFile{}, fmt.Errorf("stat %s: %w", path, err)
	}
	reader.mutex.Lock()
	cached, ok := reader.files[path]
	reader.mutex.Unlock()
	if ok && cached.size == info.Size() && cached.modNS == info.ModTime().UnixNano() {
		return cached, nil
	}
	fresh, err := read(path)
	if err != nil {
		return cachedFile{}, err
	}
	fresh.size, fresh.modNS = info.Size(), info.ModTime().UnixNano()
	reader.mutex.Lock()
	reader.files[path] = fresh
	reader.mutex.Unlock()
	return fresh, nil
}

// sessionRecord reads the statusline's record for a Claude session.
func (reader *Reader) sessionRecord(sessionID string) (statusline.SessionRecord, error) {
	return statusline.ReadSession(reader.sidDir, sessionID)
}
