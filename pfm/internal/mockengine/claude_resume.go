package mockengine

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const claudeCustomTitle = "custom-title"

func claudeTranscriptTitle(path string) (string, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read transcript %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()
	var custom, ai string
	reader := bufio.NewReader(file)
	for {
		line, readErr := reader.ReadBytes('\n')
		var record struct {
			Type        string `json:"type"`
			CustomTitle string `json:"customTitle"`
			AITitle     string `json:"aiTitle"`
		}
		if json.Unmarshal(line, &record) == nil {
			switch record.Type {
			case claudeCustomTitle:
				custom = record.CustomTitle
			case "ai-title":
				ai = record.AITitle
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", fmt.Errorf("read transcript %s: %w", path, readErr)
		}
	}
	if custom != "" {
		return custom, nil
	}
	return ai, nil
}

// pendingFork is a fork Claude Code has not written yet: it writes a fork's transcript, the parent's
// records first, at its first prompt, so a promptless fork has none (113be757). Records written before
// that prompt wait in held.
type pendingFork struct {
	parent string
	held   []claudeRecord
}

// landFork writes a pending fork's transcript: the parent's records seeded, then the held ones.
func (session *claudeSession) landFork() error {
	fork := session.fork
	if fork == nil {
		return nil
	}
	session.fork = nil
	if err := seedClaudeFork(fork.parent, session.transcript, session.sessionID); err != nil {
		return err
	}
	if err := touchFile(session.transcript); err != nil {
		return err
	}
	for i := range fork.held {
		if err := session.write(fork.held[i]); err != nil {
			return err
		}
	}
	return nil
}

func seedClaudeFork(parent, child, sessionID string) error {
	if parent == child {
		return fmt.Errorf("fork transcript %s equals parent", parent)
	}
	data, err := os.ReadFile(parent)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read transcript %s: %w", parent, err)
	}
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var record map[string]json.RawMessage
		if err := json.Unmarshal(line, &record); err != nil {
			return fmt.Errorf("decode transcript %s: %w", parent, err)
		}
		if string(record["type"]) == `"`+claudeCustomTitle+`"` {
			continue
		}
		record["sessionId"], _ = json.Marshal(sessionID)
		encoded, err := json.Marshal(record)
		if err != nil {
			return fmt.Errorf("encode fork of %s: %w", parent, err)
		}
		if err := appendLine(child, encoded); err != nil {
			return fmt.Errorf("seed fork %s: %w", child, err)
		}
	}
	return nil
}

// inAppResumer is an engine whose composer takes `/resume {session}`: Claude Code switches the live pane to
// that conversation in place, its argv still naming the session it was launched with.
type inAppResumer interface {
	resumeInApp(id string) (refusal string, err error)
}

// resumeInApp answers a typed `/resume {session}`; false leaves the line to the engine as a prompt.
func (loop *paneLoop) resumeInApp(id string) bool {
	resumer, ok := loop.engine.(inAppResumer)
	if !ok {
		return false
	}
	refusal, err := resumer.resumeInApp(id)
	switch {
	case err != nil:
		warn(loop.proc.stderr, "resume: %v", err)
	case refusal != "":
		loop.screen.say(refusal)
	default:
		loop.screen.history = nil
		// Claude's statusline names the resumed conversation at its next render, rewriting pfm's crumb.
		loop.refreshStatus(loop.usage)
	}
	loop.paint()
	return true
}

// resumeInApp ends the running conversation (SessionEnd reason resume) and opens id's transcript in the same
// process: SessionStart source resume, its title read back from the transcript. An id with no transcript in
// this project is refused on screen, as Claude Code does, and the running conversation goes on.
func (session *claudeSession) resumeInApp(id string) (string, error) {
	path := filepath.Join(session.configDir, "projects", claudeProjectSlug(session.proc.cwd), id+".jsonl")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) || id == "" {
		return "No conversation found with session ID: " + id, nil
	} else if err != nil {
		return "", fmt.Errorf("resume %s: %w", id, err)
	}
	if err := session.finish(sourceResume); err != nil {
		return "", err
	}
	title, err := claudeTranscriptTitle(path)
	if err != nil {
		return "", err
	}
	session.sessionID, session.transcript, session.title = id, path, title
	return "", session.begin(sourceResume)
}
