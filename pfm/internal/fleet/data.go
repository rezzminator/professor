package fleet

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/store"
)

// Data is the index DB's side of one scan: every indexed chat the view can
// compose, plus the operator's kills.
type Data struct {
	Transcripts      []store.Transcript
	Rollouts         []store.Rollout
	OpenCodeSessions []store.OpenCodeSession
	CxNames          map[string]string
	Killed           []store.Killed
	// CachedCounts, when set, carries the default view's killed/suppressed
	// totals — LoadDefaultData reads capped candidates, so compose cannot
	// count them from the rows it was given.
	CachedCounts *store.CachedCounts
}

// LoadData reads every indexed chat — the all and killed views.
func LoadData(ctx context.Context, database *store.Store) (Data, error) {
	transcripts, err := database.Transcripts(ctx)
	if err != nil {
		return Data{}, err
	}
	rollouts, err := database.Rollouts(ctx)
	if err != nil {
		return Data{}, err
	}
	openCodeSessions, err := database.OpenCodeSessions(ctx)
	if err != nil {
		return Data{}, err
	}
	cxNames, err := database.CxNames(ctx)
	if err != nil {
		return Data{}, err
	}
	killed, err := database.KilledChats(ctx)
	if err != nil {
		return Data{}, err
	}
	return Data{
		Transcripts:      transcripts,
		Rollouts:         rollouts,
		OpenCodeSessions: openCodeSessions,
		CxNames:          cxNames,
		Killed:           killed,
	}, nil
}

// LoadDefaultData reads the default view's capped resume candidates.
func LoadDefaultData(ctx context.Context, database *store.Store) (Data, error) {
	transcripts, rollouts, counts, err := database.DefaultCandidates(ctx, 30, 15)
	if err != nil {
		return Data{}, err
	}
	// The default view caps resume rows per engine; the OpenCode mirror is a
	// full read (it has no per-file delta machinery), so it bypasses
	// DefaultCandidates by design and compose applies openCodeResumeCap itself.
	openCodeSessions, err := database.OpenCodeSessions(ctx)
	if err != nil {
		return Data{}, err
	}
	cxNames, err := database.CxNames(ctx)
	if err != nil {
		return Data{}, err
	}
	killed, err := database.KilledChats(ctx)
	if err != nil {
		return Data{}, err
	}
	return Data{
		Transcripts:      transcripts,
		Rollouts:         rollouts,
		OpenCodeSessions: openCodeSessions,
		CxNames:          cxNames,
		Killed:           killed,
		CachedCounts:     &counts,
	}, nil
}

// EnrichLive adds the index rows a capped load left out but a live chat
// needs: the transcript behind every crumb and agent, and the whole rollout
// lineage behind every live Codex process.
func EnrichLive(
	ctx context.Context,
	database *store.Store,
	data Data,
	live gather.Snapshot,
) (Data, error) {
	transcriptIDs := make(map[string]int, len(data.Transcripts))
	for index := range data.Transcripts {
		transcript := data.Transcripts[index]
		transcriptIDs[transcript.UUID] = index
	}
	wantedTranscripts := make(map[string]struct{})
	for _, crumb := range live.Crumbs {
		id := strings.TrimSuffix(
			filepath.Base(crumb.TranscriptPath),
			filepath.Ext(crumb.TranscriptPath),
		)
		if id != "" {
			wantedTranscripts[id] = struct{}{}
		}
	}
	for _, agent := range live.Agents {
		if agent.SessionID != "" {
			wantedTranscripts[agent.SessionID] = struct{}{}
		}
	}
	// Each live chat's whole continued-in chain is loaded, not just the id
	// its crumb or agent names: Claude moves a running chat into a background
	// job under a new session id while the host keeps the old one, and
	// followContinuations can only follow the handoff to a successor it was
	// given.
	for id := range wantedTranscripts {
		seen := make(map[string]struct{})
		for id != "" {
			if _, cycle := seen[id]; cycle {
				break
			}
			seen[id] = struct{}{}
			index, found := transcriptIDs[id]
			if !found {
				transcript, stored, err := database.Transcript(ctx, id)
				if err != nil {
					return Data{}, err
				}
				if !stored {
					break
				}
				data.Transcripts = append(data.Transcripts, transcript)
				index = len(data.Transcripts) - 1
				transcriptIDs[id] = index
			}
			id = data.Transcripts[index].ContinuedIn
		}
	}

	rolloutIDs := make(map[string]struct{}, len(data.Rollouts))
	for index := range data.Rollouts {
		rollout := data.Rollouts[index]
		rolloutIDs[rollout.ID] = struct{}{}
	}
	for _, process := range live.Codex {
		id := rolloutIDFromPath(process.RolloutPath)
		if id == "" {
			continue
		}
		if _, found := rolloutIDs[id]; found {
			continue
		}
		family, err := database.RolloutLineage(ctx, id)
		if err != nil {
			return Data{}, err
		}
		for index := range family {
			rollout := family[index]
			if _, found := rolloutIDs[rollout.ID]; found {
				continue
			}
			data.Rollouts = append(data.Rollouts, rollout)
			rolloutIDs[id] = struct{}{}
			rolloutIDs[rollout.ID] = struct{}{}
		}
	}
	return data, nil
}

// rolloutIDFromPath strips a rollout file name down to its thread id:
// rollout-2026-01-02T03-04-05-<id>.jsonl → <id>.
// An empty path — a rollout-less Codex process, the normal shape since Codex
// 0.146.1 — has no id; filepath.Base would answer ".".
func rolloutIDFromPath(path string) string {
	if path == "" {
		return ""
	}
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	rest := strings.TrimPrefix(stem, "rollout-")
	if len(rest) > 20 &&
		rest[4] == '-' &&
		rest[7] == '-' &&
		rest[10] == 'T' &&
		rest[13] == '-' &&
		rest[16] == '-' &&
		rest[19] == '-' {
		return rest[20:]
	}
	return rest
}
