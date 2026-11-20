package statusline

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
)

// AgentsReport is what Agents answers for one Claude Code session: the facts
// of every sub-agent that runs in it, at any depth, oldest first, or of
// exactly the agents a caller names, in its order, running or not — the facts
// Claude Code's own AgentInfo does not hand the professor plugin's AgentRow
// mod. No agent running is an empty Agents, never an error.
type AgentsReport struct {
	SessionID string       `json:"sessionId"`
	At        int64        `json:"at"` // Unix milliseconds the report was read at
	Agents    []AgentFacts `json:"agents"`
	Look      AgentsLook   `json:"look"`
}

// AgentFacts is one sub-agent, read from its own transcript and meta file and
// the cost ledger. Error is set when its transcript could not be read, or not
// to its end within the scan budget: its counts, cache and activity are then
// unknown, never zero. Absent adds that
// the transcript is not on disk at all (an agent named by id that has written
// none yet, or none any more); Error then names the path looked at.
type AgentFacts struct {
	ID string `json:"id"`
	// Model is the model its calls went to, else the one it was spawned with; "" when neither is known.
	Model string `json:"model"`
	// Effort is the level its latest request went out at; "" until one records it.
	Effort string `json:"effort"`
	// Tools counts its distinct tool calls, ToolErrors its errored tool results,
	// Compactions its compact boundaries.
	Tools       int `json:"tools"`
	ToolErrors  int `json:"toolErrors"`
	Compactions int `json:"compactions"`
	// CacheHit is the share, 0-100, of its newest completed call's prompt read
	// from the cache; -1 before its first reply.
	CacheHit int `json:"cacheHit"`
	// CacheLifetime is false for a model whose provider names no cache
	// lifetime (a gpt- model through the gateway): no countdown applies.
	CacheLifetime bool `json:"cacheLifetime"`
	// Context is its newest completed call's prompt in tokens; 0 until one
	// lands, and after a compaction.
	Context int64 `json:"context"`
	// ContextWindow is the gateway catalog's window for a gpt- model; 0 for any
	// other, whose window Claude Code's contextWindowSize already gives.
	ContextWindow int64 `json:"contextWindow"`
	// LastActivity is its transcript's newest entry, Unix milliseconds; 0 when unknown.
	LastActivity int64        `json:"lastActivity"`
	Nested       AgentNesting `json:"nested"`
	// Spend is its billing plus every agent's below it; null when its
	// transcript could not be read and the cost ledger holds no row of it.
	Spend  *AgentSpend `json:"spend"`
	Text   AgentText   `json:"text"`
	Error  string      `json:"error,omitempty"`
	Absent bool        `json:"absent,omitempty"`
}

// AgentNesting counts the agents below one agent, at every depth: Total, how
// many of them are working (they run and their own turn is open), and how
// many could not be read. Error is a failure to look: the counts are unknown.
type AgentNesting struct {
	Total   int    `json:"total"`
	Working int    `json:"working"`
	Unknown int    `json:"unknown"`
	Error   string `json:"error,omitempty"`
}

// AgentSpend is what an agent and every agent below it billed over their
// whole transcripts. USDKnown is false when nothing could be priced; Floor
// means the sum is a lower bound (a model the price table cannot price, a
// transcript not read to its end). AdvisorUSD is the part of USD an advisor
// model billed, AdvisorUnpriced the consultations it could not price. Ledger
// means USD, In and Out are the cost ledger's rows; APIEquivalent that a part
// is the gateway's API-equivalent price, not a bill.
type AgentSpend struct {
	USD             float64 `json:"usd"`
	USDKnown        bool    `json:"usdKnown"`
	In              int64   `json:"in"`
	Out             int64   `json:"out"`
	Tools           int     `json:"tools"`
	AdvisorUSD      float64 `json:"advisorUsd"`
	AdvisorUnpriced int     `json:"advisorUnpriced"`
	Floor           bool    `json:"floor"`
	Ledger          bool    `json:"ledger"`
	APIEquivalent   bool    `json:"apiEquivalent"`
}

// Agents is the one door to a session's sub-agents, read from the session
// transcript's path: with no ids every running agent, with ids exactly the
// agents they name (namedAgents), each transcript folded from its scan
// checkpoint, so a call per refresh costs one process for all rows. A path
// that names no session, or a subagents directory that cannot be listed, is
// the error; an id that cannot name an agent's file is answered absent; a
// cause that leaves one agent's facts partial goes to runtime.Warn and into
// that agent's facts.
func Agents(sessionTranscript string, ids []string, runtime Runtime) (AgentsReport, error) {
	runtime = runtime.normalized()
	now := runtime.now()
	sessionTranscript = strings.TrimSpace(sessionTranscript)
	if sessionTranscript == "" || filepath.Ext(sessionTranscript) != ".jsonl" {
		return AgentsReport{}, fmt.Errorf("session transcript %q is no .jsonl path", sessionTranscript)
	}
	report := AgentsReport{
		SessionID: strings.TrimSuffix(filepath.Base(sessionTranscript), ".jsonl"),
		At:        now.UnixMilli(),
		Agents:    []AgentFacts{},
		Look:      agentsLook(),
	}
	tasks, err := runningAgents(sessionTranscript, now, runtime.scans)
	if err != nil {
		return AgentsReport{}, err
	}
	live := make(map[string]bool, len(tasks))
	for _, task := range tasks {
		live[task.ID] = true
	}
	if len(ids) > 0 {
		tasks = namedAgents(sessionTranscript, ids, tasks)
	}
	report.Agents = append(report.Agents, agentFacts(runtime, report.SessionID, sessionTranscript, tasks, live)...)
	return report, nil
}

// agentFacts reads each task's facts, in order, on a normalized runtime. live
// is every agent of the session that runs: it decides which agents below a
// task count as working. A session with a cost ledger prices each agent from
// it: the ledger rows of that agent and every agent below it
// (ledger_spend.go); an agent it holds no row of yet keeps its transcripts'
// price. A nil runtime.Prices leaves every dollar unpriced.
func agentFacts(
	runtime Runtime,
	sessionID, sessionTranscript string,
	tasks []subagentTask,
	live map[string]bool,
) []AgentFacts {
	tree := scanAgentTree(sessionTranscript, runtime.Prices, runtime.scans)
	tree.judgeLiveness(live)
	ledger := loadLedgerView(sessionID, runtime.Home, runtime.pathsEnv(), runtime.Warn)
	facts := make([]AgentFacts, 0, len(tasks))
	for _, task := range tasks {
		if !agentIDPattern.MatchString(task.ID) {
			facts = append(facts, unnamableAgentFacts(task.ID))
			continue
		}
		read := *tree.read(task.ID)
		if read.model != "" { // the model its calls went to; the spawn model is an alias or absent
			task.Model = read.model
		}
		read.nest = tree.nesting(task.ID)
		read.spend.add(tree.nestedSpend(task.ID))
		if ledger != nil {
			if booked, ok := ledger.spend(task.ID, tree.children); ok && (booked.priced > 0 || read.err != nil) {
				// a ledger that prices nothing still counts the tokens of an agent whose transcript did not read
				read.spend = read.spend.withLedger(booked)
			}
		}
		for _, cause := range tree.drainWarnings() {
			fmt.Fprintf(runtime.Warn, "%s: %s: nested agents: %s\n", agentsWho, task.ID, cause)
		}
		if read.err != nil {
			fmt.Fprintf(runtime.Warn, "%s: %s: %v\n", agentsWho, task.ID, read.err)
		}
		var window int64
		if openAIModel(task.Model) {
			window, _ = runtime.contextCatalog.capacity(task.Model)
		}
		facts = append(facts, newAgentFacts(task, &read, window))
	}
	return facts
}

// unnamableAgentFacts is the facts of an id that names no file of the
// subagents directory: absent, its counts unknown, looked for nowhere.
func unnamableAgentFacts(id string) AgentFacts {
	facts := AgentFacts{
		ID: id, CacheHit: -1, CacheLifetime: true, Absent: true,
		Error: fmt.Sprintf("agent id %q names no sub-agent transcript (letters, digits, _ and -)", id),
	}
	facts.Text = newAgentText(&facts)
	return facts
}

// newAgentFacts is one agent's facts from its read.
func newAgentFacts(task subagentTask, read *agentActivity, window int64) AgentFacts {
	facts := AgentFacts{
		ID: task.ID, Model: task.Model, Effort: read.effort,
		Tools: read.tools, ToolErrors: read.errors, Compactions: read.compactions,
		CacheHit: read.cacheHit, CacheLifetime: !openAIModel(task.Model),
		Context: read.context, ContextWindow: window,
		Nested: AgentNesting{Total: read.nest.total, Working: read.nest.active, Unknown: read.nest.unknown},
	}
	if read.nest.err != nil {
		facts.Nested = AgentNesting{Error: read.nest.err.Error()}
	}
	if !read.last.IsZero() {
		facts.LastActivity = read.last.UnixMilli()
	}
	switch {
	case read.err != nil:
		facts.Error = read.err.Error()
		facts.Absent = errors.Is(read.err, fs.ErrNotExist)
	case read.turn.err != nil:
		// read short of its end: its counts are floors, its activity and cache hit unknown, never "none yet"
		facts.Error = read.turn.err.Error()
	}
	if read.err == nil || read.spend.fromLedger {
		spend := read.spend
		facts.Spend = &AgentSpend{
			USD: spend.usd, USDKnown: spend.priced > 0 || !spend.partial, In: spend.in, Out: spend.out,
			Tools: spend.tools, AdvisorUSD: spend.advisorUSD, AdvisorUnpriced: spend.advisorUnpriced,
			Floor:  spend.partial && (spend.priced > 0 || spend.behind),
			Ledger: spend.fromLedger, APIEquivalent: spend.gateway.present,
		}
	}
	facts.Text = newAgentText(&facts)
	return facts
}
