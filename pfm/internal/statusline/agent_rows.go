package statusline

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

const (
	agentRowsWho = "statusline: agent rows"
	// agentRowsMax is the most rows the agents take below the main line; with
	// more running, the last says how many are not shown.
	agentRowsMax = 4
	// agentQuietBound is how long an agent that never ended (no finished mark,
	// no end notification: its Claude Code died under it) keeps its row once
	// its transcript stops growing. Live agents have sat half an hour quiet on
	// an unanswered request, so the bound is well past that.
	agentQuietBound = 2 * time.Hour
	// agentResumeSlack is how long after an agent's end its transcript may
	// still be written before that end no longer holds: past it, the agent was
	// resumed (a SendMessage to an agent that ended) and runs again. Claude Code
	// writes an agent's last line before its finished mark and end notice.
	agentResumeSlack = 10 * time.Second
	// agentEndsKind is the scan checkpoint of a spawner transcript's end notifications.
	agentEndsKind = "agent-ends"
)

// agentRowLines are the lines Render draws below the main line: one per agent
// row, each opening with a reset; none for an engine other than Claude, whose
// sub-agents keep no transcripts beside the session's.
func agentRowLines(runtime Runtime, data input, directory string, now time.Time) string {
	if runtime.Engine != pfmengine.Claude {
		return ""
	}
	var lines strings.Builder
	for _, row := range agentRows(runtime, data, directory, now) {
		lines.WriteString(reset + row + "\n")
	}
	return lines.String()
}

// agentRows are the main line's rows for the session's running sub-agents,
// oldest first: none when no agent runs (or the session has no subagents
// directory), at most agentRowsMax, the last saying "+N more agents" when
// more run. A directory that cannot be listed is one row naming the cause,
// never an absence.
func agentRows(runtime Runtime, data input, directory string, now time.Time) []string {
	tasks, err := runningAgents(data.TranscriptPath, now, runtime.scans)
	if err != nil {
		fmt.Fprintf(runtime.Warn, "%s: %v\n", agentRowsWho, err)
		return []string{cBad + "agents ?: " + strings.Join(strings.Fields(err.Error()), " ") + reset}
	}
	if len(tasks) == 0 {
		return nil
	}
	live := make(map[string]bool, len(tasks))
	for _, task := range tasks {
		live[task.ID] = true
	}
	shown, hidden := tasks, 0
	if len(tasks) > agentRowsMax {
		shown, hidden = tasks[:agentRowsMax-1], len(tasks)-(agentRowsMax-1)
	}
	rows := renderAgentRows(agentRowsInput{
		SessionID:      data.SessionID,
		TranscriptPath: data.TranscriptPath,
		Cwd:            directory,
		Columns:        runtime.Columns,
		SessionModel:   data.Model.ID,
		SessionWindow:  data.ContextWindow.ContextWindowSize,
		Tasks:          shown,
	}, live, now, runtime.SIDDir, runtime.Prices, runtime.Home, runtime.pathsEnv(), runtime.scans, runtime.Warn)
	if hidden > 0 {
		rows = append(rows, cMuted+fmt.Sprintf("+%d more agents", hidden)+reset)
	}
	return rows
}

// runningAgents are the session's sub-agents that run, at any depth, oldest
// first. An agent runs from its meta file's birth until one of: its meta file
// says finished (Claude Code writes it when the agent ends) or stoppedByUser;
// its spawner's transcript — the session's, or the parent agent's — carries a
// task notification ending it (completed, failed, killed: a stopped agent gets
// no finished mark); or its own transcript has not grown for agentQuietBound.
// An end holds only while the agent's transcript was not written more than
// agentResumeSlack after it: a resumed agent runs again. Only transcripts
// written inside the bound are looked at, and only their spawners' tails past
// the last checkpoint are read. No subagents directory is no agent; one that
// cannot be listed is the error.
func runningAgents(sessionTranscript string, now time.Time, scans transcriptScans) ([]subagentTask, error) {
	if strings.TrimSpace(sessionTranscript) == "" {
		return nil, nil
	}
	dir := transcript.SubagentsDir(sessionTranscript)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", dir, err)
	}
	type candidate struct {
		task    subagentTask
		spawner string
		written time.Time // its transcript's last write
	}
	var candidates []candidate
	for _, entry := range entries {
		id, ok := transcript.SubagentID(entry.Name())
		if !ok || entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil || now.Sub(info.ModTime()) > agentQuietBound {
			continue // gone since the listing, or quiet past the bound
		}
		metaPath := transcript.SubagentMetaPath(filepath.Join(dir, entry.Name()))
		meta, err := transcript.ReadSubagentMeta(metaPath)
		// The meta file's model is the one the agent was spawned with, "" when it
		// runs on its definition's or the session's; the row takes the model its
		// transcript observed once a call lands (renderAgentRows).
		task := subagentTask{ID: id, Name: meta.Name, Label: meta.Description, Model: meta.Model, Effort: meta.Effort}
		// An unreadable meta file runs on: its row shows "role ?" and the cause.
		metaInfo, statErr := os.Stat(metaPath)
		if statErr == nil {
			task.StartTime = metaInfo.ModTime().UnixMilli()
		}
		if err == nil && statErr == nil && (meta.Finished || meta.StoppedByUser) &&
			!resumedAfter(info.ModTime(), metaInfo.ModTime()) {
			continue
		}
		spawner := sessionTranscript
		if parent := strings.TrimSpace(meta.ParentAgentID); parent != "" {
			spawner = transcript.SubagentTranscript(dir, parent)
		}
		candidates = append(candidates, candidate{task: task, spawner: spawner, written: info.ModTime()})
	}
	ended := map[string]map[string]time.Time{}
	var running []subagentTask
	for index := range candidates {
		each := &candidates[index]
		ends, read := ended[each.spawner]
		if !read {
			fold := &agentEndsFold{Ended: map[string]time.Time{}}
			done, err := scans.fold(each.spawner, agentEndsKind, fold)
			switch {
			case err != nil:
				fmt.Fprintf(scans.warn, "%s: end notifications of %s (its agents count as running): %v\n",
					agentRowsWho, each.spawner, err)
			case !done:
				fmt.Fprintf(scans.warn, "%s: end notifications of %s not read to its end within the budget "+
					"(an agent ended past that point counts as running)\n", agentRowsWho, each.spawner)
			}
			ends = fold.Ended
			ended[each.spawner] = ends
		}
		if at, over := ends[each.task.ID]; !over || resumedAfter(each.written, at) {
			running = append(running, each.task)
		}
	}
	sort.Slice(running, func(a, b int) bool {
		if running[a].StartTime != running[b].StartTime {
			return running[a].StartTime < running[b].StartTime
		}
		return running[a].ID < running[b].ID
	})
	return running, nil
}

// resumedAfter reports whether an agent's transcript, last written at written,
// grew more than agentResumeSlack past the end at ended. A zero end — a notice
// whose line carries no timestamp — always holds.
func resumedAfter(written, ended time.Time) bool {
	return !ended.IsZero() && written.After(ended.Add(agentResumeSlack))
}
