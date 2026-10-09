package statusline

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strconv"

	"github.com/rezzminator/professor/pfm/internal/pricing"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// agentSpend is what an agent billed over its whole transcript — every
// response before and after a compaction — in USD, tokens and tool calls: in
// is every prompt token (uncached, cache read, cache write), out every output
// token, tools every distinct tool_use.
// partial means the sum is a floor: a response named a model the price table
// cannot price, or a transcript below the agent could not be read.
type agentSpend struct {
	usd     float64
	in, out int64
	tools   int
	priced  int
	partial bool
}

func (spend *agentSpend) add(other agentSpend) {
	spend.usd += other.usd
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
}

type response struct {
	model  string
	billed pricing.ClaudeUsage
}

func newSpendScan(prices *pricing.Prices) *spendScan {
	return &spendScan{prices: prices, responses: map[string]response{}, tools: map[string]struct{}{}}
}

func (scan *spendScan) record(messageID, requestID, model string, usage *pricing.UsageBlock) {
	if usage == nil {
		return
	}
	billed, _ := usage.Billed()
	if key := transcript.ResponseKey(messageID, requestID, "", ""); key != "" {
		scan.responses[key] = response{model: model, billed: billed}
		return
	}
	scan.unkeyed.add(scan.price(model, billed))
}

// total is every response the scan saw, each priced once, and its distinct
// tool calls.
func (scan *spendScan) total() agentSpend {
	spend := scan.unkeyed
	spend.tools = len(scan.tools)
	for _, seen := range scan.responses {
		spend.add(scan.price(seen.model, seen.billed))
	}
	return spend
}

func (scan *spendScan) price(model string, billed pricing.ClaudeUsage) agentSpend {
	spend := agentSpend{
		in:  billed.Input + billed.CacheRead + billed.Write5m + billed.Write1h,
		out: billed.Output,
	}
	if billed == (pricing.ClaudeUsage{}) {
		return spend
	}
	row, ok := pricing.Row{}, false
	if scan.prices != nil && model != "" {
		row, ok = scan.prices.Resolve(model)
	}
	if ok {
		spend.usd, ok = row.ClaudeCost(billed)
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
		Type      string `json:"type"`
		RequestID string `json:"requestId"`
		Message   struct {
			ID      string              `json:"id"`
			Model   string              `json:"model"`
			Usage   *pricing.UsageBlock `json:"usage"`
			Content []struct {
				Type string `json:"type"`
				ID   string `json:"id"`
			} `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &entry) != nil || entry.Type != entryAssistant {
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
// sub-agent's beside it. A sub-agent transcript that cannot be read, or a
// subagents directory that cannot be listed, leaves the sum partial; a session
// that never spawned one has no directory, which is not a failure.
func sessionSpend(sessionTranscript string) (agentSpend, error) {
	// Read without prices: the dollars are Claude Code's, so an unpriced
	// response is no floor here, only an unreadable transcript is.
	total, err := readTranscriptSpend(sessionTranscript, nil)
	if err != nil {
		return agentSpend{}, err
	}
	total.partial = false
	tree := scanAgentTree(sessionTranscript)
	if tree.err != nil {
		if !errors.Is(tree.err, fs.ErrNotExist) {
			total.partial = true
		}
		return total, nil
	}
	for _, id := range tree.ids {
		spend, err := readTranscriptSpend(transcript.SubagentTranscript(tree.dir, id), nil)
		spend.partial = err != nil
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
// then tool calls, for the agent and every agent below it. A floor (partial) gets a "+?" in the
// warning colour; with nothing priced at all the dollar part is "$?". An own
// transcript that could not be read is "$?/?/?/?": nothing it says is a total.
func spendSegment(activity agentActivity) string {
	if activity.err != nil {
		return cWarn + "$?/?/?/?" + reset
	}
	spend := activity.spend
	dollars := "$?"
	if spend.priced > 0 || !spend.partial {
		dollars = "$" + strconv.FormatFloat(spend.usd, 'f', 2, 64)
	}
	line := cCost + dollars + reset + cMuted + "/" + reset +
		cTokens + formatContextTokens(spend.in) + reset + cMuted + "/" + reset +
		cTokens + formatContextTokens(spend.out) + reset + cMuted + "/" + reset +
		cTools + strconv.Itoa(spend.tools) + reset
	if spend.partial && spend.priced > 0 {
		line += cWarn + "+?" + reset
	}
	return line
}
