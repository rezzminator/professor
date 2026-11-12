package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// ResumeOnRestore reads the on-disk Herdr config: whether Herdr resumes a
// pane's agent when it restores a session (resume_agents_on_restore, on when
// unset or when no config exists). A debug config beside the real one is an
// error: which one the server reads cannot be told.
func ResumeOnRestore(env paths.Env) (resume bool, err error) {
	path := env.Get("HERDR_CONFIG_PATH")
	if path == "" {
		directory := env.Get("XDG_CONFIG_HOME")
		if directory == "" {
			home, err := paths.HomeFrom(env)
			if err != nil {
				return false, fmt.Errorf("resolve Herdr config home: %w", err)
			}
			directory = filepath.Join(home, ".config")
		}
		debugPath := filepath.Join(directory, "herdr-dev", "config.toml")
		if _, err := os.Stat(debugPath); err == nil {
			return false, fmt.Errorf(
				"debug Herdr config exists at %s; set HERDR_CONFIG_PATH to identify the server's config",
				debugPath,
			)
		} else if !errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("inspect debug Herdr config %s: %w", debugPath, err)
		}
		path = filepath.Join(directory, "herdr", "config.toml")
	}
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	var config struct {
		Session struct {
			ResumeAgentsOnRestore *bool `toml:"resume_agents_on_restore"`
		} `toml:"session"`
	}
	if _, err := toml.Decode(strings.TrimPrefix(string(content), "\uFEFF"), &config); err != nil {
		return false, fmt.Errorf("parse %s: %w", path, err)
	}
	resume = config.Session.ResumeAgentsOnRestore == nil || *config.Session.ResumeAgentsOnRestore
	return resume, nil
}

// ResumePending reports whether Herdr resumes pane's agent itself: Herdr's
// restore is on and pane carries the agent session Herdr restored it with and
// no restore error. Herdr types that agent's resume command into the pane's
// shell as it starts, so whatever else would open there (the shim's auto-open
// picker) yields. A fresh pane, or one whose agent ended, carries no session.
// Every failed read is an error, never "not pending".
func ResumePending(ctx context.Context, env paths.Env, socket, pane string) (bool, error) {
	resume, err := ResumeOnRestore(env)
	if err != nil {
		return false, fmt.Errorf("herdr restore config: %w", err)
	}
	if !resume {
		return false, nil
	}
	body, err := SeatRequest(ctx, socket, "pane.get", map[string]string{paneIDKey: pane})
	if err != nil {
		return false, err
	}
	var info struct {
		Type string `json:"type"`
		Pane struct {
			ID           string          `json:"pane_id"`
			Session      json.RawMessage `json:"agent_session"`
			RestoreError json.RawMessage `json:"restore_error"`
		} `json:"pane"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return false, fmt.Errorf("decode pane %s: %w", pane, err)
	}
	if info.Type != paneInfoType || info.Pane.ID != pane {
		return false, fmt.Errorf("pane %s answered as another pane: %s", pane, body)
	}
	return !absentJSON(info.Pane.Session) && absentJSON(info.Pane.RestoreError), nil
}

// absentJSON is a field Herdr left out, sent as null or as an empty string.
func absentJSON(field json.RawMessage) bool {
	value := strings.TrimSpace(string(field))
	return value == "" || value == "null" || value == `""`
}
