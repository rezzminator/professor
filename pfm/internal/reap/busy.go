package reap

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rezzminator/professor/pfm/internal/action"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// BusyProbe reports the sessions the engine itself considers busy.
//
// It returns an ERROR rather than an empty set when it cannot answer: a
// deliberately detached chat grinding a long task is indistinguishable from an
// idle orphan without this, so an unanswerable probe has to fail closed rather
// than read as "nobody is working."
type BusyProbe interface {
	BusySessions(ctx context.Context) (map[string]struct{}, error)
}

// ClaudeAgents asks each installed account's `claude agents --json` which
// sessions are busy.
type ClaudeAgents struct {
	Binary     string
	ConfigDirs []string
	Timeout    time.Duration
}

// NewClaudeAgents wires the probe to the accounts on this machine.
func NewClaudeAgents(resolved paths.Values) ClaudeAgents {
	directories := make([]string, 0, len(resolved.Roots[pfmengine.Claude]))
	for _, root := range resolved.Roots[pfmengine.Claude] {
		directories = append(directories, filepath.Dir(root))
	}
	return NewClaudeAgentsConfigured(resolved, pfmengine.MustLookup(pfmengine.Claude).Binary, directories)
}

// NewClaudeAgentsConfigured probes exactly the configured roster with the
// configured Claude command.
func NewClaudeAgentsConfigured(
	resolved paths.Values,
	binaryName string,
	directories []string,
) ClaudeAgents {
	if binaryName == "" {
		binaryName = pfmengine.MustLookup(pfmengine.Claude).Binary
	}
	binary, err := obs.Runner(deps.RealRunner{}).LookPath(binaryName)
	if err != nil {
		if filepath.IsAbs(binaryName) {
			binary = binaryName
		} else {
			binary = filepath.Join(resolved.Home, ".local", "bin", binaryName)
		}
	}
	available := make([]string, 0, len(directories))
	for _, directory := range directories {
		if info, err := os.Stat(directory); err == nil && info.IsDir() {
			available = append(available, directory)
		}
	}
	return ClaudeAgents{
		Binary:     binary,
		ConfigDirs: available,
		Timeout:    20 * time.Second,
	}
}

type agentRow struct {
	SessionID string `json:"sessionId"`
	Status    string `json:"status"`
}

// BusySessions queries every account. Each account is asked separately so one
// account's error output cannot poison another's parse, and ANY failure fails
// the whole probe: a partial busy set is a busy set with holes in it, and the
// holes are exactly the chats a sweep would kill.
func (agents ClaudeAgents) BusySessions(
	ctx context.Context,
) (map[string]struct{}, error) {
	if len(agents.ConfigDirs) == 0 {
		return nil, fmt.Errorf("no Claude account directories to query")
	}
	busy := make(map[string]struct{})
	for _, directory := range agents.ConfigDirs {
		queryCtx, cancel := context.WithTimeout(ctx, agents.timeout())
		// A registry read starts no conversation, so it carries no prompt
		// material and no autonomy flags — but it still takes the fleet's one
		// hygiene strip, so a probe fired from inside a chat cannot inherit
		// that chat's session identity or endpoint. The probe holds a config
		// directory rather than a machine config, so it states that seat as a
		// one-account roster.
		command, err := action.ClaudeSpawn{
			Purpose: action.PurposeQuery,
			Account: 1,
			Args:    []string{"agents", "--json"},
			Machine: pfmconfig.Config{
				Claude:   pfmconfig.ClaudePrefs{Binary: agents.Binary},
				Accounts: []pfmconfig.Account{{ID: 1, ConfigDir: directory}},
			},
		}.Command(queryCtx)
		if err != nil {
			cancel()
			return nil, fmt.Errorf("build busy-agent query for %s: %w", directory, err)
		}
		output, err := command.Output()
		cancel()
		if err != nil {
			return nil, fmt.Errorf(
				"query busy agents for %s: %w",
				directory,
				err,
			)
		}
		var rows []agentRow
		if err := json.Unmarshal(output, &rows); err != nil {
			return nil, fmt.Errorf(
				"parse busy agents for %s: %w",
				directory,
				err,
			)
		}
		for _, row := range rows {
			if row.Status == "busy" && row.SessionID != "" {
				busy[row.SessionID] = struct{}{}
			}
		}
	}
	return busy, nil
}

func (agents ClaudeAgents) timeout() time.Duration {
	if agents.Timeout <= 0 {
		return 20 * time.Second
	}
	return agents.Timeout
}
