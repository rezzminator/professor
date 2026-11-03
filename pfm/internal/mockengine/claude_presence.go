package mockengine

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
)

func (session *claudeSession) presencePath() string {
	return filepath.Join(session.configDir, "sessions", strconv.Itoa(session.proc.pid)+".json")
}

func (session *claudeSession) presence() error {
	// The live registry's identity fields are observed; optional peer messaging is not implemented.
	record := struct {
		PID        int    `json:"pid"`
		SessionID  string `json:"sessionId"`
		CWD        string `json:"cwd"`
		StartedAt  int64  `json:"startedAt"`
		Version    string `json:"version"`
		Kind       string `json:"kind"`
		Entrypoint string `json:"entrypoint"`
	}{
		session.proc.pid, session.sessionID, session.proc.cwd, time.Now().UnixMilli(),
		strings.Fields(session.proc.script.Version)[0], "interactive", sourceCLI,
	}
	if session.call.headless {
		record.Kind = "headless"
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode native session: %w", err)
	}
	return atomicfile.Write(session.presencePath(), raw, 0o600)
}

func (session *claudeSession) removePresence() error {
	err := os.Remove(session.presencePath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
