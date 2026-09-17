package kill

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/gather"
	"hostops/pfm/internal/store"
)

// IdentifySelf maps the caller environment to its exact indexed identity.
func (manager *Manager) IdentifySelf(
	ctx context.Context,
	environment SelfEnvironment,
) (Target, error) {
	socketPath, socketName := parseTMUX(environment.TMUX)
	if socketPath == "" {
		return Target{}, errors.New("could not identify this chat: TMUX is empty")
	}
	id, known := pfmengine.FromSocket(socketName)
	if known && id == pfmengine.Codex {
		return manager.identifyCodexSelf(
			ctx,
			socketPath,
			socketName,
			environment.TMUXPane,
		)
	}
	return manager.identifyClaudeSelf(
		ctx,
		socketPath,
		socketName,
		environment,
	)
}

func (manager *Manager) identifyClaudeSelf(
	ctx context.Context,
	socketPath, socketName string,
	environment SelfEnvironment,
) (Target, error) {
	if environment.ClaudeSessionID != "" {
		transcript, found, err := manager.database.Transcript(
			ctx,
			environment.ClaudeSessionID,
		)
		if err != nil {
			return Target{}, err
		}
		if found && looksLikeSessionID(transcript.UUID) {
			return Target{
				Engine:     pfmengine.Claude,
				ID:         transcript.UUID,
				DataPath:   transcript.Path,
				SocketPath: socketPath,
				SocketName: socketName,
				PaneID:     environment.TMUXPane,
			}, nil
		}
	}

	transcriptPath := ""
	if environment.TMUXPane != "" {
		transcriptPath = readPath(
			filepath.Join(
				manager.paths.sidDir,
				socketName+"."+environment.TMUXPane,
			),
		)
	}
	if transcriptPath == "" {
		transcriptPath = readPath(filepath.Join(manager.paths.sidDir, socketName))
	}
	id := dataID(transcriptPath)
	if !looksLikeSessionID(id) {
		return Target{}, errors.New(
			"could not identify this Claude chat: no valid session id or crumb",
		)
	}
	if transcript, found, err := manager.database.Transcript(ctx, id); err != nil {
		return Target{}, err
	} else if found {
		transcriptPath = transcript.Path
	}
	return Target{
		Engine:     pfmengine.Claude,
		ID:         id,
		DataPath:   transcriptPath,
		SocketPath: socketPath,
		SocketName: socketName,
		PaneID:     environment.TMUXPane,
	}, nil
}

func (manager *Manager) identifyCodexSelf(
	ctx context.Context,
	socketPath, socketName, paneID string,
) (Target, error) {
	if paneID == "" {
		return Target{}, errors.New("could not identify Codex chat: TMUX_PANE is empty")
	}
	panePID, err := manager.tmux.PanePID(ctx, socketPath, paneID)
	if err != nil {
		return Target{}, fmt.Errorf("read Codex pane pid: %w", err)
	}
	// The state-store resolver is what lets a chat whose Codex session writes
	// no rollout file kill ITSELF; a rollout-backed pane still resolves
	// through its open file descriptor exactly as before.
	live, err := gather.DetectCodexThreadsInRoots(
		manager.proc,
		manager.paths.codexHomes,
		[]gather.Pane{{
			Socket: socketName,
			PaneID: paneID,
			PID:    panePID,
		}},
		store.NewCodexThreadResolverRoots(ctx, manager.paths.codexHomes, manager.CodexPaneBound(ctx)),
	)
	if err != nil {
		return Target{}, err
	}
	if len(live) != 1 {
		return Target{}, errors.New(
			"could not identify Codex chat: no live Codex session under this pane",
		)
	}
	if live[0].IdentityError != "" {
		return Target{}, fmt.Errorf("could not verify Codex root: %s", live[0].IdentityError)
	}
	rolloutPath := live[0].RolloutPath
	// Verified held metadata wins over a reverted rollout's storage filename.
	// State-store-only sessions retain their existing indexed-path resolution.
	id := dataID(rolloutPath)
	if live[0].RolloutHeld {
		id = live[0].ThreadID
	} else if rollout, found := rolloutByPath(manager, ctx, rolloutPath); found {
		id = rollout
	}
	if id == "" {
		id = live[0].ThreadID
	}
	if !looksLikeSessionID(id) {
		return Target{}, fmt.Errorf(
			"could not parse a Codex chat id from rollout %q and thread %q",
			rolloutPath,
			live[0].ThreadID,
		)
	}
	target, err := manager.codexTarget(ctx, id, rolloutPath)
	if err != nil {
		return Target{}, err
	}
	target.SocketPath = socketPath
	target.SocketName = socketName
	target.PaneID = paneID
	return target, nil
}

func rolloutByPath(
	manager *Manager,
	ctx context.Context,
	path string,
) (string, bool) {
	rollouts, err := manager.database.Rollouts(ctx)
	if err != nil {
		return "", false
	}
	clean := filepath.Clean(path)
	for index := range rollouts {
		rollout := &rollouts[index]
		if filepath.Clean(rollout.Path) == clean {
			return rollout.ID, true
		}
	}
	return "", false
}

func readPath(path string) string {
	content, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(content), "\r\n")
}

func dataID(path string) string {
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
	return stem
}

func looksLikeSessionID(id string) bool {
	parts := strings.Split(id, "-")
	if len(parts) != 5 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
	}
	return true
}
