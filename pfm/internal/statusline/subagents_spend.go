package statusline

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"github.com/rezzminator/professor/pfm/internal/pricing"
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

// billedUsage is an assistant entry's usage block. A response can carry zeros
// in every top-level count and its real counts only in iterations (seen on
// claude-opus-5-5), the same fold /tokens makes.
type billedUsage struct {
	Input         int64 `json:"input_tokens"`
	Output        int64 `json:"output_tokens"`
	CacheRead     int64 `json:"cache_read_input_tokens"`
	CacheCreation int64 `json:"cache_creation_input_tokens"`
	Breakdown     *struct {
		Write5m int64 `json:"ephemeral_5m_input_tokens"`
		Write1h int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
	Iterations []billedUsage `json:"iterations"`
}

// billed folds iterations when the top level is empty and splits the cache
// write by TTL: the part the breakdown does not cover is the 5-minute default.
func (usage billedUsage) billed() pricing.ClaudeUsage {
	if usage.Input+usage.Output+usage.CacheRead+usage.CacheCreation == 0 && len(usage.Iterations) > 0 {
		var sum pricing.ClaudeUsage
		for _, iteration := range usage.Iterations {
			part := iteration.billed()
			sum.Input += part.Input
			sum.Output += part.Output
			sum.CacheRead += part.CacheRead
			sum.Write5m += part.Write5m
			sum.Write1h += part.Write1h
		}
		return sum
	}
	var split5m, split1h int64
	if usage.Breakdown != nil {
		split5m, split1h = usage.Breakdown.Write5m, usage.Breakdown.Write1h
	}
	return pricing.ClaudeUsage{
		Input:     usage.Input,
		Output:    usage.Output,
		CacheRead: usage.CacheRead,
		Write5m:   split5m + max(usage.CacheCreation-split5m-split1h, 0),
		Write1h:   split1h,
	}
}

// spendScan prices one transcript's responses. Claude Code writes a response
// as several lines while it streams, each repeating its usage and the output
// count growing to the final one on the last, so per message id the last line
// counts; a line with no id counts on its own.
type spendScan struct {
	prices    *pricing.Table
	responses map[string]response
	unkeyed   agentSpend
	tools     map[string]struct{}
}

type response struct {
	model  string
	billed pricing.ClaudeUsage
}

func newSpendScan(prices *pricing.Table) *spendScan {
	return &spendScan{prices: prices, responses: map[string]response{}, tools: map[string]struct{}{}}
}

func (scan *spendScan) record(messageID, model string, usage *billedUsage) {
	if usage == nil {
		return
	}
	if messageID != "" {
		scan.responses[messageID] = response{model: model, billed: usage.billed()}
		return
	}
	scan.unkeyed.add(scan.price(model, usage.billed()))
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
	if !ok {
		spend.partial = true
		return spend
	}
	spend.usd = row.ClaudeCost(billed)
	spend.priced = 1
	return spend
}

// line records one transcript line's response and tool calls, if it carries
// them.
func (scan *spendScan) line(line []byte) {
	var entry struct {
		Type    string `json:"type"`
		Message struct {
			ID      string       `json:"id"`
			Model   string       `json:"model"`
			Usage   *billedUsage `json:"usage"`
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
	scan.record(entry.Message.ID, entry.Message.Model, entry.Message.Usage)
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
		spend, err := readTranscriptSpend(filepath.Join(tree.dir, "agent-"+id+".jsonl"), nil)
		spend.partial = err != nil
		total.add(spend)
	}
	return total, nil
}

// readTranscriptSpend prices a whole transcript; a torn final line is the
// agent mid-write and is skipped.
func readTranscriptSpend(path string, prices *pricing.Table) (agentSpend, error) {
	file, err := os.Open(path)
	if err != nil {
		return agentSpend{}, fmt.Errorf("open sub-agent transcript %s: %w", path, err)
	}
	defer func() { _ = file.Close() }() // read-only handle; a close error loses nothing
	scan := newSpendScan(prices)
	reader := bufio.NewReaderSize(file, 1<<16)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			scan.line(line)
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				return scan.total(), fmt.Errorf("read sub-agent transcript %s: %w", path, readErr)
			}
			return scan.total(), nil
		}
	}
}

// nestedSpend sums what every agent below one task billed, at any depth, each
// transcript read once per render. A tree that could not be scanned, or a
// transcript that could not be read, leaves the sum partial and its cause in
// the tree's warnings.
func (tree *agentTree) nestedSpend(id string, prices *pricing.Table) agentSpend {
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
			spend, err = readTranscriptSpend(filepath.Join(tree.dir, "agent-"+next+".jsonl"), prices)
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
