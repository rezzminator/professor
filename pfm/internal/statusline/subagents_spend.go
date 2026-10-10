package statusline

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strconv"

	"github.com/rezzminator/professor/pfm/internal/costledger"
	"github.com/rezzminator/professor/pfm/internal/pricing"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// agentSpend is what an agent billed over its whole transcript — every
// response before and after a compaction — in USD, tokens and tool calls: in
// is every prompt token (uncached, cache read, cache write), out every output
// token, tools every distinct tool_use. An advisor the agent consulted bills at
// its own model: its dollars and tokens are counted in usd, in and out, and
// its dollars alone in advisorUSD.
// partial means the sum is a floor: a response named a model the price table
// cannot price, carried a usage iteration of a type pfm does not know, or a
// transcript below the agent could not be read.
type agentSpend struct {
	usd     float64
	in, out int64
	// advisorUSD is the part of usd the advisor model billed.
	advisorUSD float64
	// advisorUnpriced counts the consultations the table could not price.
	advisorUnpriced int
	tools           int
	priced          int
	partial         bool
	// fromLedger means usd, in and out are the cost ledger's rows of the agent and every agent
	// below it (ledgerSpend), not a price of its transcripts.
	fromLedger bool
	// costStates are the cost-state lines of the transcript this spend was read from, with the exit
	// each was written at; add leaves them out of a sum, which has no one exit.
	costStates []costledger.CostState
}

func (spend *agentSpend) add(other agentSpend) {
	spend.usd += other.usd
	spend.advisorUSD += other.advisorUSD
	spend.advisorUnpriced += other.advisorUnpriced
	spend.in += other.in
	spend.out += other.out
	spend.tools += other.tools
	spend.priced += other.priced
	spend.partial = spend.partial || other.partial
}

// spendScan prices one transcript's responses. Claude Code writes a response
// as several lines while it streams, each repeating its usage, so per
// response key (transcript.ResponseKey) the last line counts; a line with no
// key counts on its own.
type spendScan struct {
	prices    *pricing.Prices
	responses map[string]response
	unkeyed   agentSpend
	tools     map[string]struct{}
	anchors   costledger.Anchors
}

type response struct {
	model    string
	billed   pricing.ClaudeUsage
	requests []pricing.ClaudeUsage
	advisors []pricing.AdvisorUsage
	unknown  bool
}

func newSpendScan(prices *pricing.Prices) *spendScan {
	return &spendScan{prices: prices, responses: map[string]response{}, tools: map[string]struct{}{}}
}

func (scan *spendScan) record(messageID, requestID, model string, usage *pricing.UsageBlock) {
	if usage == nil {
		return
	}
	billed, _ := usage.Billed()
	seen := response{
		model: model, billed: billed, requests: usage.Requests(), advisors: usage.Advisors(),
		unknown: len(usage.UnknownIterations()) != 0,
	}
	if key := transcript.ResponseKey(messageID, requestID, "", ""); key != "" {
		scan.responses[key] = seen
		return
	}
	scan.unkeyed.add(scan.priceResponse(seen))
}

// total is every response the scan saw, each priced once, and its distinct
// tool calls.
func (scan *spendScan) total() agentSpend {
	spend := scan.unkeyed
	spend.tools = len(scan.tools)
	spend.costStates = scan.anchors.States()
	for _, seen := range scan.responses {
		spend.add(scan.priceResponse(seen))
	}
	return spend
}

// priceResponse is the response's own spend, each request at its own tier,
// plus each advisor consultation's, the advisor priced at the model it names.
// An iteration of a type pfm does not know leaves the sum a floor.
func (scan *spendScan) priceResponse(seen response) agentSpend {
	spend := scan.price(seen.model, seen.billed, seen.requests)
	spend.partial = spend.partial || seen.unknown
	for _, advisor := range seen.advisors {
		consulted := scan.price(advisor.Model, advisor.Usage, []pricing.ClaudeUsage{advisor.Usage})
		consulted.advisorUSD = consulted.usd
		if consulted.partial {
			consulted.advisorUnpriced = 1
		}
		spend.add(consulted)
	}
	return spend
}

// price is billed's tokens and the USD of its requests, which sum to it.
func (scan *spendScan) price(model string, billed pricing.ClaudeUsage, requests []pricing.ClaudeUsage) agentSpend {
	spend := agentSpend{in: billed.Context(), out: billed.Output}
	if billed == (pricing.ClaudeUsage{}) {
		return spend
	}
	row, ok := pricing.Row{}, false
	if scan.prices != nil && model != "" {
		row, ok = scan.prices.Resolve(model)
	}
	if ok {
		var parts pricing.ClaudeParts
		parts, ok = row.ResponseRequestsCostParts(requests)
		spend.usd = parts.Total()
	}
	if !ok {
		spend.partial = true
		return spend
	}
	spend.priced = 1
	return spend
}

// line records one transcript line's response and tool calls, if it carries
// them.
func (scan *spendScan) line(line []byte) {
	var entry struct {
		Type          string   `json:"type"`
		Timestamp     string   `json:"timestamp"`
		TotalCostUSD  *float64 `json:"totalCostUSD"`
		StartTime     float64  `json:"startTime"`
		TotalDuration float64  `json:"totalDuration"`
		RequestID     string   `json:"requestId"`
		Message       struct {
			ID      string              `json:"id"`
			Model   string              `json:"model"`
			Usage   *pricing.UsageBlock `json:"usage"`
			Content []struct {
				Type string `json:"type"`
				ID   string `json:"id"`
			} `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &entry) != nil {
		return
	}
	if entry.Timestamp != "" {
		scan.anchors.Stamp(entry.Timestamp)
	}
	if entry.Type == entryCostState && entry.TotalCostUSD != nil {
		scan.anchors.State(*entry.TotalCostUSD, entry.StartTime, entry.TotalDuration)
	}
	if entry.Type != entryAssistant {
		return
	}
	for _, block := range entry.Message.Content {
		if block.Type == blockToolUse && block.ID != "" {
			scan.tools[block.ID] = struct{}{}
		}
	}
	scan.record(entry.Message.ID, entry.RequestID, entry.Message.Model, entry.Message.Usage)
}

// sessionSpend is what a whole chat billed: its own transcript and every
// sub-agent's beside it, priced at prices (nil prices nothing). An unpriced
// response, a sub-agent transcript that cannot be read, or a subagents
// directory that cannot be listed leaves the sum partial; a session that never
// spawned one has no directory, which is not a failure.
func sessionSpend(sessionTranscript string, prices *pricing.Prices) (agentSpend, error) {
	total, err := readTranscriptSpend(sessionTranscript, prices)
	if err != nil {
		return agentSpend{}, err
	}
	tree := scanAgentTree(sessionTranscript)
	if tree.err != nil {
		if !errors.Is(tree.err, fs.ErrNotExist) {
			total.partial = true
		}
		return total, nil
	}
	for _, id := range tree.ids {
		spend, err := readTranscriptSpend(transcript.SubagentTranscript(tree.dir, id), prices)
		spend.partial = spend.partial || err != nil
		total.add(spend)
	}
	return total, nil
}

// readTranscriptSpend prices a whole transcript; a torn final line is the
// agent mid-write and is skipped.
func readTranscriptSpend(path string, prices *pricing.Prices) (agentSpend, error) {
	scan := newSpendScan(prices)
	if _, _, err := transcript.ReadLines(path, 0, func(line []byte, _ bool) { scan.line(line) }); err != nil {
		_, wrapped := transcriptError(path, err)
		return scan.total(), wrapped
	}
	return scan.total(), nil
}

// transcriptError names a sub-agent transcript that failed to open or to
// read; opened is false when the file could not be opened at all.
func transcriptError(path string, err error) (opened bool, wrapped error) {
	var openErr *transcript.OpenError
	if errors.As(err, &openErr) {
		return false, fmt.Errorf("open sub-agent transcript %s: %w", path, openErr.Err)
	}
	if cause := errors.Unwrap(err); cause != nil {
		err = cause
	}
	return true, fmt.Errorf("read sub-agent transcript %s: %w", path, err)
}

// nestedSpend sums what every agent below one task billed, at any depth, each
// transcript read once per render. A tree that could not be scanned, or a
// transcript that could not be read, leaves the sum partial and its cause in
// the tree's warnings.
func (tree *agentTree) nestedSpend(id string, prices *pricing.Prices) agentSpend {
	if tree.err != nil {
		return agentSpend{partial: true}
	}
	var total agentSpend
	seen := map[string]bool{id: true}
	queue := append([]string(nil), tree.children[id]...)
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		if seen[next] {
			continue
		}
		seen[next] = true
		queue = append(queue, tree.children[next]...)
		spend, ok := tree.spent[next]
		if !ok {
			var err error
			spend, err = readTranscriptSpend(transcript.SubagentTranscript(tree.dir, next), prices)
			if err != nil {
				tree.warnings = append(tree.warnings, err.Error())
				spend.partial = true
			}
			tree.spent[next] = spend
		}
		total.add(spend)
	}
	return total
}

// spendSegment renders "$1.24/3.1M/42K/12": USD, prompt and output tokens,
// then tool calls, for the agent and every agent below it (with a cost ledger,
// its rows' dollars and tokens: see agentSpend.fromLedger); advisor spend, part
// of that USD, follows as " adv $0.30", or " adv n/a" when the table could not
// price a consultation. A floor (partial) gets a "+?" in the
// warning colour; with nothing priced at all the dollar part is "$?". An own
// transcript that could not be read is "$?/?/?/?": nothing it says is a total.
func spendSegment(activity agentActivity) string {
	spend := activity.spend
	if activity.err != nil && !spend.fromLedger {
		return cWarn + "$?/?/?/?" + reset
	}
	dollars := "$?"
	if spend.priced > 0 || !spend.partial {
		dollars = "$" + strconv.FormatFloat(spend.usd, 'f', 2, 64)
	}
	tools := cTools + strconv.Itoa(spend.tools) + reset
	if activity.err != nil {
		// The ledger priced the row; the transcript that holds its tool calls did not read.
		tools = cWarn + "?" + reset
	}
	line := cCost + dollars + reset + cMuted + "/" + reset +
		cTokens + formatContextTokens(spend.in) + reset + cMuted + "/" + reset +
		cTokens + formatContextTokens(spend.out) + reset + cMuted + "/" + reset +
		tools
	switch {
	case spend.advisorUnpriced > 0:
		line += cMuted + " adv " + reset + cWarn + "n/a" + reset
	case spend.advisorUSD > 0:
		line += cMuted + " adv " + reset + cCost + "$" + strconv.FormatFloat(spend.advisorUSD, 'f', 2, 64) + reset
	}
	if spend.partial && spend.priced > 0 {
		line += cWarn + "+?" + reset
	}
	return line
}
