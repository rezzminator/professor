package statusline

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/transcript"
)

const (
	agentsWho = "statusline: agents"
	// agentQuietBound is how long an agent that never ended (no finished mark,
	// no end notification: its Claude Code died under it) counts as running until
	// its transcript has been quiet this long. Live agents have sat half an hour quiet on
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
		task, meta, born, err := metaTask(dir, id)
		// An unreadable meta file runs on: its facts carry what its transcript says.
		if err == nil && !born.IsZero() && (meta.Finished || meta.StoppedByUser) &&
			!resumedAfter(info.ModTime(), born) {
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
					agentsWho, each.spawner, err)
			case !done:
				fmt.Fprintf(scans.warn, "%s: end notifications of %s not read to its end within the budget "+
					"(an agent ended past that point counts as running)\n", agentsWho, each.spawner)
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

// metaTask is agent id's task from its meta file in the subagents directory
// dir: the model it was spawned with, "" when it runs on its definition's or
// the session's (its facts take the model its transcript observed once a call
// lands, agentFacts), and its start, the meta file's birth, which born also
// gives, zero when the file cannot be stat'ed. err is the meta file's read.
func metaTask(dir, id string) (task subagentTask, meta transcript.SubagentMeta, born time.Time, err error) {
	metaPath := transcript.SubagentMetaPath(transcript.SubagentTranscript(dir, id))
	meta, err = transcript.ReadSubagentMeta(metaPath)
	task = subagentTask{ID: id, Model: meta.Model}
	if metaInfo, statErr := os.Stat(metaPath); statErr == nil {
		born = metaInfo.ModTime()
		task.StartTime = born.UnixMilli()
	}
	return task, meta, born, err
}

// agentIDPattern is an id a caller may name an agent by: one file-name
// element, so it never reaches outside the subagents directory.
var agentIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

// namedAgents are the agents ids names, in the order each is first named: a
// running one as runningAgents found it, any other from its meta file
// (metaTask), ended or never started alike, so a caller keeps an agent's facts
// current after it stops. An id with no files on disk is still answered; its
// facts say it is absent. An id that cannot name a file (a teammate in a pane
// of its own goes by its name@team address) is answered too, with no look at
// the disk: agentFacts marks it absent (unnamableAgentFacts).
func namedAgents(sessionTranscript string, ids []string, running []subagentTask) []subagentTask {
	dir := transcript.SubagentsDir(sessionTranscript)
	byID := make(map[string]subagentTask, len(running))
	for _, task := range running {
		byID[task.ID] = task
	}
	named := make([]subagentTask, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		task, ok := byID[id]
		switch {
		case ok:
		case agentIDPattern.MatchString(id):
			task, _, _, _ = metaTask(dir, id) // an unreadable meta file leaves the model to the transcript
		default:
			task = subagentTask{ID: id}
		}
		named = append(named, task)
	}
	return named
}

// resumedAfter reports whether an agent's transcript, last written at written,
// grew more than agentResumeSlack past the end at ended. A zero end — a notice
// whose line carries no timestamp — always holds.
func resumedAfter(written, ended time.Time) bool {
	return !ended.IsZero() && written.After(ended.Add(agentResumeSlack))
}
