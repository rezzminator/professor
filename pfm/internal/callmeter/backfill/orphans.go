package backfill

// retireOrphans heals what neither the hook nor a transcript ever can:
// Claude Code's own internal sub-agents, which stop with no agent_type, no
// SubagentStart and no transcript on disk (docs/design/hooks/callmeter.md §
// "Found live" — the untypedAgentMissingTranscript hook exemption,
// hookentry/callmeter.go). Rows the hook wrote before that exemption existed
// carry a provisional pending:{tool_use_id} request_id that no transcript
// will ever resolve; storedPending never reaches them because no transcript
// names their message. retireOrphans finds and deletes them instead.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/rezzminator/professor/pfm/internal/callmeter"
)

// OrphanGrace is the hook's own resolve window: a pending request younger
// than this is left alone, since a resolve may still land for it.
const OrphanGrace = 10 * time.Minute

// orphanCandidate is one pending request old enough to consider.
type orphanCandidate struct {
	requestID string
	sessionID string
	agentID   string
}

// retireOrphans deletes every pending request (and its calls) that is older
// than OrphanGrace, belongs to a non-empty agent_id, is never carried by a
// typed call nor named by an agents row, and whose sub-agent transcript does
// not exist under any of configDirs. The count returned is requests retired.
// The error is a store failure or a stat failure other than fs.ErrNotExist,
// which is a fault surfaced, never treated as absence.
func retireOrphans(
	ctx context.Context,
	store *callmeter.Store,
	configDirs []string,
	now time.Time,
	logf Logf,
) (int, error) {
	cutoff := now.Add(-OrphanGrace).UnixMilli()
	candidates, err := orphanCandidates(ctx, store, cutoff)
	if err != nil {
		return 0, err
	}
	var toRetire []string
	for _, c := range candidates {
		reachable, err := agentReachable(ctx, store, c.requestID, c.agentID)
		if err != nil {
			return 0, err
		}
		if reachable {
			continue
		}
		found, err := transcriptExists(configDirs, c.sessionID, c.agentID)
		if err != nil {
			return 0, fmt.Errorf("callmeter backfill: orphan transcript of session %q agent %q: %w",
				c.sessionID, c.agentID, err)
		}
		if found {
			continue
		}
		toRetire = append(toRetire, c.requestID)
	}
	if len(toRetire) == 0 {
		return 0, nil
	}
	removed, err := store.RetireRequests(ctx, toRetire)
	if err != nil {
		return 0, fmt.Errorf("callmeter backfill: retire orphans: %w", err)
	}
	for _, id := range toRetire {
		logf("callmeter backfill: retired unreachable pending request %s", id)
	}
	return int(removed), nil
}

// orphanCandidates lists every pending request older than cutoff with a
// non-empty agent_id.
func orphanCandidates(ctx context.Context, store *callmeter.Store, cutoff int64) ([]orphanCandidate, error) {
	rows, err := store.DB().QueryContext(ctx,
		`SELECT request_id, session_id, agent_id FROM requests
		WHERE pending = 1 AND ts < ? AND agent_id IS NOT NULL AND agent_id != ''`, cutoff)
	if err != nil {
		return nil, fmt.Errorf("callmeter backfill: list orphan candidates: %w", err)
	}
	var out []orphanCandidate
	for rows.Next() {
		var c orphanCandidate
		var session, agent *string
		if err := rows.Scan(&c.requestID, &session, &agent); err != nil {
			return nil, errors.Join(fmt.Errorf("callmeter backfill: scan orphan candidate: %w", err), rows.Close())
		}
		if session != nil {
			c.sessionID = *session
		}
		if agent != nil {
			c.agentID = *agent
		}
		out = append(out, c)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, fmt.Errorf("callmeter backfill: read orphan candidates: %w", err)
	}
	return out, nil
}

// agentReachable is true when requestID is resolvable some other way than a
// transcript: a typed call (agent_type non-empty on any call carrying it) or
// an existing agents row for agentID.
func agentReachable(ctx context.Context, store *callmeter.Store, requestID, agentID string) (bool, error) {
	var typed int
	if err := store.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM calls WHERE request_id = ? AND agent_type IS NOT NULL AND agent_type != ''`,
		requestID).Scan(&typed); err != nil {
		return false, fmt.Errorf("callmeter backfill: check typed calls of %s: %w", requestID, err)
	}
	if typed > 0 {
		return true, nil
	}
	var known int
	if err := store.DB().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM agents WHERE agent_id = ?", agentID).Scan(&known); err != nil {
		return false, fmt.Errorf("callmeter backfill: check agents row of %s: %w", agentID, err)
	}
	return known > 0, nil
}

// transcriptExists is true when the sub-agent transcript of (sessionID,
// agentID) is on disk under any of configDirs' projects/{slug}/{sessionID}/
// subagents/agent-{agentID}.jsonl, the same layout childTranscript builds
// from a known chat transcript path. A stat failure other than fs.ErrNotExist
// is returned as an error, never read as absence.
func transcriptExists(configDirs []string, sessionID, agentID string) (bool, error) {
	if sessionID == "" || agentID == "" {
		return false, nil
	}
	for _, dir := range configDirs {
		projects := filepath.Join(dir, "projects")
		entries, err := os.ReadDir(projects)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("read %s: %w", projects, err)
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			path := filepath.Join(projects, e.Name(), sessionID, "subagents", "agent-"+agentID+".jsonl")
			_, err := os.Stat(path)
			switch {
			case err == nil:
				return true, nil
			case errors.Is(err, fs.ErrNotExist):
				continue
			default:
				return false, fmt.Errorf("stat %s: %w", path, err)
			}
		}
	}
	return false, nil
}
