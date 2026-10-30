package statusline

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"time"

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
	gateway gatewaySpend
	usd     float64
	in, out int64
	// advisorUSD is the part of usd the advisor model billed.
	advisorUSD float64
	// advisorUnpriced counts the consultations the table could not price.
	advisorUnpriced int
	tools           int
	priced          int
	partial         bool
	// behind means a transcript fold stopped at the render's budget short of
	// its end (transcriptScans): the sum is a floor, partial too, and says so
	// even before anything was priced.
	behind bool
	// fromLedger means usd, in and out are the cost ledger's rows of the agent and every agent
	// below it (ledgerSpend), not a price of its transcripts.
	fromLedger bool
	// costStates are the cost-state lines of the transcript this spend was read from, with the exit
	// each was written at; add leaves them out of a sum, which has no one exit.
	costStates []costledger.CostState
}

func (spend *agentSpend) add(other agentSpend) {
	spend.gateway.add(other.gateway)
	spend.usd += other.usd
	spend.advisorUSD += other.advisorUSD
	spend.advisorUnpriced += other.advisorUnpriced
	spend.in += other.in
	spend.out += other.out
	spend.tools += other.tools
	spend.priced += other.priced
	spend.partial = spend.partial || other.partial
	spend.behind = spend.behind || other.behind
}

// spendScan prices one transcript's responses. Claude Code writes a response
// as several lines while it streams, each repeating its usage, so per
// response key (transcript.ResponseKey) the last line counts; a line with no
// key counts on its own. The exported fields are its whole fold, which a scan
// checkpoint keeps (transcriptScans); every response is priced in total, at
// the render's table, never at the table of the render that read it.
type spendScan struct {
	prices    *pricing.Prices
	Responses map[string]response `json:"responses"`
	Unkeyed   []response          `json:"unkeyed,omitempty"`
	Tools     map[string]struct{} `json:"tools"`
	Anchors   costledger.Anchors  `json:"anchors"`
}

type response struct {
	At         string                 `json:"at,omitempty"`
	Model      string                 `json:"model,omitempty"`
	Billed     pricing.ClaudeUsage    `json:"billed"`
	Requests   []pricing.ClaudeUsage  `json:"requests,omitempty"`
	Advisors   []pricing.AdvisorUsage `json:"advisors,omitempty"`
	Incomplete bool                   `json:"incomplete,omitempty"`
	Unknown    bool                   `json:"unknown,omitempty"`
}

func newSpendScan(prices *pricing.Prices) *spendScan {
	scan := &spendScan{prices: prices}
	scan.reset()
	return scan
}

func (scan *spendScan) reset() {
	*scan = spendScan{prices: scan.prices, Responses: map[string]response{}, Tools: map[string]struct{}{}}
}

func (scan *spendScan) loaded() error {
	if scan.Responses == nil || scan.Tools == nil {
		return errors.New("spend scan without its responses or tool calls")
	}
	return nil
}

func (scan *spendScan) record(messageID, requestID, model string, usage *transcriptUsage, timestamp ...string) {
	if usage == nil {
		return
	}
	billed, _ := usage.Billed()
	seen := response{
		Model: model, Billed: billed, Requests: usage.Requests(), Advisors: usage.Advisors(),
		Incomplete: gatewayModel(model) && !usage.reportsCache,
		Unknown:    len(usage.UnknownIterations()) != 0,
	}
	if len(timestamp) > 0 {
		seen.At = timestamp[0]
	}
	if key := transcript.ResponseKey(messageID, requestID, "", ""); key != "" {
		scan.Responses[key] = seen
		return
	}
	scan.Unkeyed = append(scan.Unkeyed, seen)
}

// total is every response the scan saw, each priced once, and its distinct
// tool calls.
func (scan *spendScan) total() agentSpend {
	var spend agentSpend
	for index := range scan.Unkeyed {
		spend.add(scan.priceResponse(scan.Unkeyed[index]))
	}
	spend.tools = len(scan.Tools)
	spend.costStates = scan.Anchors.States()
	for key := range scan.Responses {
		spend.add(scan.priceResponse(scan.Responses[key]))
	}
	return spend
}

// priceResponse is the response's own spend, each request at its own tier,
// plus each advisor consultation's, the advisor priced at the model it names.
// An iteration of a type pfm does not know leaves the sum a floor.
func (scan *spendScan) priceResponse(seen response) agentSpend {
	if seen.Incomplete {
		return agentSpend{partial: true, gateway: gatewaySpend{present: true, partial: true}}
	}
	spend := scan.price(seen.Model, seen.Billed, seen.Requests)
	spend.partial = spend.partial || seen.Unknown
	spend.gateway.partial = spend.gateway.partial || (spend.gateway.present && seen.Unknown)
	for _, advisor := range seen.Advisors {
		consulted := scan.price(advisor.Model, advisor.Usage, []pricing.ClaudeUsage{advisor.Usage})
		consulted.advisorUSD = consulted.usd
		if consulted.partial {
			consulted.advisorUnpriced = 1
		}
		spend.add(consulted)
	}
	if spend.gateway.usd > 0 {
		at, _ := time.Parse(time.RFC3339Nano, seen.At)
		spend.gateway.calls = []gatewayCall{{at: at, usd: spend.gateway.usd}}
	}
	return spend
}

// price is billed's tokens and the USD of its requests, which sum to it.
func (scan *spendScan) price(
	model string,
	billed pricing.ClaudeUsage,
	requests []pricing.ClaudeUsage,
) (spend agentSpend) {
	defer func() {
		if gatewayModel(model) {
			spend.gateway = gatewaySpend{
				present: true,
				usd:     spend.usd,
				in:      spend.in,
				out:     spend.out,
				partial: spend.partial,
			}
		}
	}()
	spend = agentSpend{in: billed.Context(), out: billed.Output}
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
			ID      string           `json:"id"`
			Model   string           `json:"model"`
			Usage   *transcriptUsage `json:"usage"`
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
		scan.Anchors.Stamp(entry.Timestamp)
	}
	if entry.Type == entryCostState && entry.TotalCostUSD != nil {
		scan.Anchors.State(*entry.TotalCostUSD, entry.StartTime, entry.TotalDuration)
	}
	if entry.Type != entryAssistant {
		return
	}
	for _, block := range entry.Message.Content {
		if block.Type == blockToolUse && block.ID != "" {
			scan.Tools[block.ID] = struct{}{}
		}
	}
	scan.record(entry.Message.ID, entry.RequestID, entry.Message.Model, entry.Message.Usage, entry.Timestamp)
}

// sessionSpend is what a whole chat billed: its own transcript and every
// sub-agent's beside it, priced at prices (nil prices nothing). An unpriced
// response, a sub-agent transcript that cannot be read or not yet folded to
// its end within the render's budget, or a subagents directory that cannot be
// listed leaves the sum partial; a session that never spawned one has no
// directory, which is not a failure.
func sessionSpend(
	sessionTranscript string,
	prices *pricing.Prices,
	scanned *agentSpend,
	scans transcriptScans,
) (agentSpend, error) {
	var total agentSpend
	if scanned != nil {
		total = *scanned
	} else {
		var err error
		if total, err = readTranscriptSpend(sessionTranscript, prices, scans); err != nil {
			return agentSpend{}, err
		}
	}
	tree := scanAgentTree(sessionTranscript, prices, scans)
	if tree.err != nil {
		if !errors.Is(tree.err, fs.ErrNotExist) {
			total.partial = true
		}
		return total, nil
	}
	for _, id := range tree.ids {
		spend, err := readTranscriptSpend(transcript.SubagentTranscript(tree.dir, id), prices, scans)
		spend.partial = spend.partial || err != nil
		total.add(spend)
	}
	return total, nil
}

// readTranscriptSpend prices a whole transcript, resumed from its "spend"
// checkpoint; one the budget left unfinished is a floor, partial.
func readTranscriptSpend(path string, prices *pricing.Prices, scans transcriptScans) (agentSpend, error) {
	scan := newSpendScan(prices)
	done, err := scans.fold(path, "spend", scan)
	if err != nil {
		_, wrapped := transcriptError(path, err)
		return scan.total(), wrapped
	}
	spend := scan.total()
	spend.partial, spend.behind = spend.partial || !done, !done
	return spend, nil
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
func (tree *agentTree) nestedSpend(id string) agentSpend {
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
		activity := tree.read(next)
		spend := activity.spend
		if activity.err != nil {
			tree.warnOnce(next, activity.err)
			spend.partial = true
		}
		total.add(spend)
	}
	return total
}

// spendSegment renders "Σ$1.24/3.1M/42K/12": USD, prompt and output tokens,
// then tool calls, for the agent and every agent below it (with a cost ledger,
// its rows' dollars and tokens: see agentSpend.fromLedger); advisor spend, part
// of that USD, follows as " adv $0.30", or " adv n/a" when the table could not
// price a consultation. The Σ marks a lifetime sum, so it never reads as part
// of the context gauge beside it. A floor (partial) gets a "+?" in the
// warning colour; with nothing priced at all the dollar part is "$?". An own
// transcript that could not be read is "Σ$?/?/?/?": nothing it says is a total.
func spendSegment(activity agentActivity) string {
	spend := activity.spend
	if activity.err != nil && !spend.fromLedger {
		return cMuted + "Σ" + reset + cWarn + "$?/?/?/?" + reset
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
	line := cMuted + "Σ" + reset + cCost + dollars + reset + cMuted + "/" + reset +
		cTokens + formatContextTokens(spend.in) + reset + cMuted + "/" + reset +
		cTokens + formatContextTokens(spend.out) + reset + cMuted + "/" + reset +
		tools
	switch {
	case spend.advisorUnpriced > 0:
		line += cMuted + " adv " + reset + cWarn + "n/a" + reset
	case spend.advisorUSD > 0:
		line += cMuted + " adv " + reset + cCost + "$" + strconv.FormatFloat(spend.advisorUSD, 'f', 2, 64) + reset
	}
	if spend.partial && (spend.priced > 0 || spend.behind) {
		line += cWarn + "+?" + reset
	}
	return line + spend.gateway.note()
}

// gatewayModel recognizes the gateway's model ids without coupling to its store.
func gatewayModel(model string) bool {
	id, ok := pricing.NormalizeID(model)
	return ok && strings.HasPrefix(id, "gpt-")
}
