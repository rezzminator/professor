package harvestmcp

import (
	"fmt"
	"slices"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/harvest"
)

// answerTokenBudget bounds one read answer's estimated tokens. Claude Code
// refuses an MCP tool result over its 25,000-token output limit and diverts it
// to a file the caller must read back; every refused read answer on record
// estimated at 25,000 tokens or more (harvest.EstimateTokens), so an answer
// held under this budget arrives inline, with headroom for the estimate.
const answerTokenBudget = 20000

// fitAnswerBudget returns the blocks of one read answer fitted so the whole
// answer, joined as readMany sends it, stays within answerTokenBudget; an
// answer already within it is left as rendered. The content of the items that
// read is cut first, its truncated: line naming the cut, the full text still
// at its path; an answer still over — a size-only view, a long ask answer, a
// long failure — has its longest blocks cut too (cutBlocks), each saying so.
// The estimate of a join is not the sum of its blocks' (harvest.EstimateTokens
// prices a whole text's symbol share), so each pass measures the joined answer
// and asks again with the overrun taken off, then the joined answer is cut
// whole as the last resort.
func fitAnswerBudget(blocks []string, jobs []readJob, results []harvest.Result, failed []bool, view ReadView) []string {
	over := answerOverrun(blocks)
	if over <= 0 {
		return blocks
	}
	rendered := blocks
	if !view.SizeOnly {
		for attempt, budget := 0, answerTokenBudget; attempt < fitAttempts && over > 0; attempt++ {
			capped, ok := capItemContent(rendered, jobs, results, failed, view, budget)
			if !ok {
				break
			}
			blocks, over = capped, answerOverrun(capped)
			budget -= max(over, 0)
		}
	}
	whole := blocks
	for attempt, budget := 0, answerTokenBudget; attempt < fitAttempts && over > 0; attempt++ {
		blocks = cutBlocks(whole, budget)
		over = answerOverrun(blocks)
		budget -= max(over, 0)
	}
	if over > 0 {
		return []string{cutWithin(strings.Join(blocks, "\n\n"), answerTokenBudget)}
	}
	return blocks
}

// fitAttempts bounds how often one pass of fitAnswerBudget asks again.
const fitAttempts = 4

// answerOverrun is how many estimated tokens blocks, joined as readMany sends
// them, run over answerTokenBudget; <= 0 when they fit.
func answerOverrun(blocks []string) int {
	return harvest.EstimateTokens(strings.Join(blocks, "\n\n")) - answerTokenBudget
}

// capItemContent re-renders rendered's content-bearing item blocks with their
// content shared out of budget estimated tokens (contentTokenCaps); ok is false
// when the blocks around the content alone spend the budget, so no content cut
// can fit the answer.
func capItemContent(
	rendered []string, jobs []readJob, results []harvest.Result, failed []bool, view ReadView, budget int,
) ([]string, bool) {
	needs := make([]int, len(jobs))
	overhead := 0
	for index, block := range rendered {
		blockTokens := harvest.EstimateTokens(block) + 1 // + the blank line joining it to the next
		if index >= len(jobs) || failed[index] {
			overhead += blockTokens
			continue
		}
		content, _ := viewContent(results[index], view)
		needs[index] = harvest.EstimateTokens(content)
		// A capped block also gains its truncated: line.
		overhead += max(blockTokens-needs[index], 0) + truncatedLineTokens
	}
	if budget-overhead <= 0 {
		return nil, false
	}
	capped := slices.Clone(rendered)
	for index, limit := range contentTokenCaps(needs, budget-overhead) {
		if limit > 0 {
			itemView := view
			itemView.TokenCap = limit
			capped[index], _ = RenderReadItem(index+1, len(jobs), jobs[index].source, results[index], itemView)
		}
	}
	return capped, true
}

// cutBlocks is blocks with the longest cut to their share of budget estimated
// tokens, smallest first (contentTokenCaps), each cut block ending on its cut
// line.
func cutBlocks(blocks []string, budget int) []string {
	needs := make([]int, len(blocks))
	for index, block := range blocks {
		needs[index] = harvest.EstimateTokens(block) + 1 + cutLineTokens
	}
	cut := slices.Clone(blocks)
	for index, limit := range contentTokenCaps(needs, budget) {
		if limit > 0 {
			cut[index] = cutWithin(blocks[index], limit)
		}
	}
	return cut
}

// cutWithin is text cut to a rune prefix that, with its cut line, estimates
// within limit tokens; the cut line alone when no prefix fits.
func cutWithin(text string, limit int) string {
	total := harvest.EstimateTokens(text)
	for room := limit - cutLineTokens; ; room -= cutLineTokens {
		prefix := strings.TrimRight(prefixWithinTokens(text, max(room, 0)), " \t\r\n")
		shown := harvest.EstimateTokens(prefix)
		cut := fmt.Sprintf("[cut: ~%d of ~%d estimated tokens shown, to keep the read answer within %d]",
			shown, total, answerTokenBudget)
		if prefix != "" {
			cut = prefix + "\n" + cut
		}
		if harvest.EstimateTokens(cut) <= limit || room <= 0 {
			return cut
		}
	}
}

// cutLineTokens is the most estimated tokens a cut line costs.
const cutLineTokens = 32

// truncatedLineTokens is the most estimated tokens a truncated: line costs.
const truncatedLineTokens = 32

// contentTokenCaps shares budget estimated tokens among items needing needs
// tokens each, smallest first: an item within its fair share keeps all of it
// (cap 0, no cap) and leaves the rest to the larger ones, each of which is
// capped at the share left to it, never below one token.
func contentTokenCaps(needs []int, budget int) []int {
	caps := make([]int, len(needs))
	order := make([]int, 0, len(needs))
	total := 0
	for index, need := range needs {
		if need > 0 {
			order = append(order, index)
			total += need
		}
	}
	if total <= budget {
		return caps
	}
	slices.SortStableFunc(order, func(a, b int) int { return needs[a] - needs[b] })
	left := max(budget, 0)
	for position, index := range order {
		share := left / (len(order) - position)
		if needs[index] <= share {
			left -= needs[index]
			continue
		}
		caps[index] = max(share, 1)
		left -= share
	}
	return caps
}
