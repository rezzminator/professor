package statusline

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/pricing"
	"github.com/rezzminator/professor/pfm/internal/pyvalue"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// subagentTask is one running agent as its meta file names it
// (runningAgents): its id, the model it was spawned with ("" when it runs on
// its definition's or the session's) and its start.
type subagentTask struct {
	ID        string
	Model     string
	StartTime int64 // Unix milliseconds
}

// agentActivity is what a sub-agent's own transcript, agent-<id>.jsonl, says. cacheHit is -1
// until the first model turn lands; effort is the level its latest request
// went out at, "" until one records it; err means the transcript could not be
// read, told as a failure, never as zero or empty.
// spend is its own billing plus every agent's below it.
type agentActivity struct {
	effort      string
	tools       int
	errors      int
	compactions int
	cacheHit    int
	model       string
	last        time.Time
	err         error
	nest        agentNesting
	spend       agentSpend
	// turn is its last message entry.
	turn    agentTurn
	context int64 // newest completed call's last executor request: input + cache read + cache creation; 0 until one lands, and after a compaction
}

const (
	entryAssistant = "assistant"
	entryUser      = "user"
	entryCostState = "cost-state"
	blockToolUse   = "tool_use"
)

// LoadServedPrices is the machine's served price table (pricing.LoadPrices:
// the clone file or the embedded copy, the override beside pfmConfigPath
// merged), never fetched. A table that cannot load is nil — every dollar then
// renders "$?" — and its cause goes to stderr under who, as does a clone file
// the embedded copy stood in for. An override that cannot be used is no reason
// to lose the table: the published one prices, and the override's cause goes
// to stderr.
func LoadServedPrices(home, pfmConfigPath string, stderr io.Writer, who string) *pricing.Prices {
	table, err := pricing.LoadPrices(home, pfmConfigPath)
	if err != nil {
		if table.FetchedAt == "" { // no published table came back to price by
			fmt.Fprintf(stderr, "%s: price table (dollars show $?): %v\n", who, err)
			return nil
		}
		fmt.Fprintf(stderr, "%s: price override unusable, pricing from the published table: %v\n", who, err)
	}
	if table.FileError != nil {
		fmt.Fprintf(
			stderr,
			"%s: clone prices unusable, pricing from the %s table: %v\n",
			who,
			table.From,
			table.FileError,
		)
	}
	return &table
}

// readAgentActivity reads the sub-agent's own files, which Claude Code keeps
// beside the session's: <session>/subagents/agent-<id>.{meta.json,jsonl}. It
// counts distinct tool_use blocks, errored tool results and compact
// boundaries, takes the cache hit and the effort from the newest assistant
// entry that carries each, the newest entry timestamp, and what its responses
// billed, folding the transcript from its "activity" checkpoint (an unfinished
// fold leaves floors and an unread turn). A torn final line — the agent is
// mid-write — is never kept in the checkpoint.
func readAgentActivity(sessionTranscript, id string, prices *pricing.Prices, scans transcriptScans) agentActivity {
	activity := agentActivity{cacheHit: -1}
	if strings.TrimSpace(sessionTranscript) == "" {
		activity.err = errors.New("payload names no session transcript")
		return activity
	}
	path := transcript.SubagentTranscript(transcript.SubagentsDir(sessionTranscript), id)
	fold := newActivityFold(prices)
	done, err := scans.fold(path, "activity", fold)
	if err != nil {
		var opened bool
		if opened, activity.err = transcriptError(path, err); !opened {
			return activity
		}
	}
	activity.errors, activity.compactions = fold.Errors, fold.Compactions
	activity.effort, activity.model = fold.Effort, fold.Model
	activity.tools = len(fold.Seen)
	activity.spend = fold.Spend.total()
	activity.spend.tools = activity.tools
	if err == nil && !done { // counts and spend are floors; the newest entry, turn, context and hit unknown
		activity.spend.partial, activity.spend.behind = true, true
		activity.turn.err = fmt.Errorf("sub-agent transcript %s not read to its end within the budget", path)
		return activity
	}
	activity.last, activity.cacheHit, activity.context = fold.Last, fold.CacheHit, fold.Context
	// No message entry yet: the first turn is under way.
	activity.turn = agentTurn{open: fold.Turn.Open || !fold.Turn.Seen, seen: fold.Turn.Seen}
	return activity
}

func recordTranscriptLine(line []byte, fold *activityFold) {
	var entry struct {
		Type      string    `json:"type"`
		Subtype   string    `json:"subtype"`
		RequestID string    `json:"requestId"`
		Timestamp time.Time `json:"timestamp"`
		Effort    any       `json:"effort"`
		Message   struct {
			ID      string           `json:"id"`
			Model   string           `json:"model"`
			Content json.RawMessage  `json:"content"`
			Usage   *transcriptUsage `json:"usage"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &entry) != nil {
		return
	}
	if entry.Timestamp.After(fold.Last) {
		fold.Last = entry.Timestamp
	}
	if entry.Type == "system" && entry.Subtype == "compact_boundary" {
		fold.Compactions++
		fold.Context = 0 // a call from before the boundary no longer describes the context
		return
	}
	if entry.Type != entryAssistant && entry.Type != entryUser {
		return
	}
	blocks, _ := transcript.DecodeContent(entry.Message.Content)
	for index := range blocks {
		block := &blocks[index]
		id, _ := pyvalue.String(block.ID)
		switch {
		case entry.Type == entryAssistant && block.Kind() == blockToolUse && id != "":
			fold.Seen[id] = struct{}{}
		case entry.Type == entryUser && block.Kind() == "tool_result" && string(block.IsError) == "true":
			fold.Errors++
		}
	}
	if entry.Type != entryAssistant {
		return
	}
	// The level the request went out at; a model that takes none leaves it out.
	if level, ok := entry.Effort.(string); ok && strings.TrimSpace(level) != "" {
		fold.Effort = strings.TrimSpace(level)
	}
	if model := entry.Message.Model; model != "" && model != "<synthetic>" {
		fold.Model = model
	}
	usage := entry.Message.Usage
	if usage == nil {
		return
	}
	fold.Spend.record(
		entry.Message.ID,
		entry.RequestID,
		entry.Message.Model,
		usage,
		entry.Timestamp.Format(time.RFC3339Nano),
	)
	// A line that reports no cache counters is a call still streaming (the
	// gateway's message_start carries its whole-prompt estimate in
	// input_tokens and nothing else): reading it as a call would drop the
	// newest completed call's share to 0%.
	if usage.reportsCache {
		if hit := cacheHitPercent(usage.CacheRead, usage.CacheCreation, usage.Input); hit >= 0 {
			fold.CacheHit = hit
		}
	}
	// The context is the prompt of the response's last executor request, as the
	// main session's gauge reads it: a response that ran several requests
	// carries their sum at the top level, or only zeros there and its counts in
	// usage.iterations. Iterations mean the call completed even without cache
	// counters at the top level.
	if usage.reportsCache || len(usage.Iterations) > 0 {
		if requests := usage.Requests(); len(requests) > 0 {
			if size := requests[len(requests)-1].Context(); size > 0 {
				fold.Context = size
			}
		}
	}
}
