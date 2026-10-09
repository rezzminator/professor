package tokens

import (
	"context"
	"errors"
	"sort"
	"strings"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/index"
	"github.com/rezzminator/professor/pfm/internal/pricing"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// OpenCode sessions. COUNTING RULE: every assistant message carries its own
// token record (index.OpenCodeTokens: uncached input, output, reasoning
// outside output, cache read and cache write), so a session is the plain sum
// of its messages, each priced at its own model from the table. OpenCode's
// own recorded cost is summed beside the estimate as OC USD and never stands
// in for it: under a subscription it is $0, under an API key it is the
// provider's rate, neither of which is the list price the other engines show.

var engineOpenCode = pfmengine.MustLookup(pfmengine.OpenCode).LongName

// The providers whose model ids the price table reads with their prefix.
const (
	providerAnthropic = "anthropic"
	providerOpenAI    = "openai"
)

// openCodeUsage is a session's summed token counts.
type openCodeUsage struct{ in, read, write, out, reasoning int64 }

// input is every input token: uncached, cache read and cache write.
func (u openCodeUsage) input() int64 { return u.in + u.read + u.write }

// output is every output token, reasoning included.
func (u openCodeUsage) output() int64 { return u.out + u.reasoning }

// openCodeRow is one session of the --opencode table.
type openCodeRow struct {
	id, model, label string
	at               float64
	usage            openCodeUsage
	cost             *float64
	recorded         float64
}

// runOpenCode prints one bounded row per OpenCode session in the window.
func (a *auditor) runOpenCode(ctx context.Context) (int, error) {
	if len(a.openCodeRoot) == 0 {
		return 1, failure("no OpenCode root is configured; pass --opencode-root DIR")
	}
	var (
		sessions []index.OpenCodeSessionRows
		unread   []string
		read     int
	)
	for _, root := range a.openCodeRoot {
		found, err := index.ReadOpenCodeUsage(ctx, root, int64(a.since))
		switch {
		case errors.Is(err, index.ErrNoOpenCodeStore):
			unread = append(unread, index.OpenCodeStorePath(root)+" is missing")
		case err != nil:
			// a store we could not read is a failure to look, never an empty one
			unread = append(unread, index.OpenCodeStorePath(root)+": "+err.Error())
			a.scan.readErrors = append(a.scan.readErrors, index.OpenCodeStorePath(root)+": "+err.Error())
		default:
			read++
			sessions = append(sessions, found...)
		}
	}
	if read == 0 {
		return 1, failure("no OpenCode store could be read: %s", strings.Join(unread, "; "))
	}
	for _, missing := range unread {
		if strings.HasSuffix(missing, " is missing") {
			a.scan.notes = append(a.scan.notes, "OpenCode store "+missing)
		}
	}
	var rows []openCodeRow
	old, offProject := 0, 0
	for k := range sessions {
		session := &sessions[k]
		if float64(session.TimeUpdatedMS) < a.since {
			old++
			continue
		}
		if a.opts.project != "" && !strings.Contains(session.Directory, a.opts.project) {
			offProject++
			continue
		}
		if row, counted := a.openCodeSession(session); counted {
			rows = append(rows, row)
		}
	}
	a.scan.files = len(rows)
	if old != 0 {
		a.scan.notes = append(a.scan.notes, num(float64(old))+" sessions older than the window")
	}
	if offProject != 0 {
		a.scan.notes = append(a.scan.notes, num(float64(offProject))+" sessions outside --project "+a.opts.project)
	}
	sort.SliceStable(rows, func(left, right int) bool {
		if difference := orZero(rows[right].cost) - orZero(rows[left].cost); difference != 0 {
			return difference < 0
		}
		return rows[right].usage.input()+rows[right].usage.output() < rows[left].usage.input()+rows[left].usage.output()
	})
	a.printOpenCode(rows, len(sessions))
	if len(a.scan.readErrors) != 0 {
		return 1, nil
	}
	return 0, nil
}

// openCodeSession sums one session's assistant messages; counted is false
// for a session that billed no token in the window.
func (a *auditor) openCodeSession(session *index.OpenCodeSessionRows) (row openCodeRow, counted bool) {
	row = openCodeRow{id: session.ID, at: float64(session.TimeCreatedMS), label: kindMain}
	agent, priced, cost := "", true, 0.0
	for _, stored := range session.Messages {
		message, err := index.DecodeOpenCodeMessage(stored.Data)
		if err != nil {
			a.scan.badLines++
			continue
		}
		if message.Role != transcript.RoleAssistant {
			continue
		}
		if agent == "" {
			agent = message.Agent
		}
		if message.Tokens == nil {
			// the SDK writes tokens on every assistant message: one without is malformed
			a.scan.badLines++
			continue
		}
		if message.Cost != nil {
			row.recorded += *message.Cost
		}
		tokens := *message.Tokens
		if tokens.Input+tokens.Output+tokens.Reasoning+tokens.Cache.Read+tokens.Cache.Write == 0 {
			a.scan.syntheticCalls++
			continue
		}
		counted = true
		row.model = message.ModelID
		row.usage.in += tokens.Input
		row.usage.read += tokens.Cache.Read
		row.usage.write += tokens.Cache.Write
		row.usage.out += tokens.Output
		row.usage.reasoning += tokens.Reasoning
		if usd := a.openCodeCost(message.ProviderID, message.ModelID, tokens); usd == nil {
			priced = false
		} else {
			cost += *usd
		}
	}
	if session.ParentID != "" {
		if agent == "" {
			agent = "?"
		}
		row.label = "subagent (" + agent + ")"
	}
	if priced {
		row.cost = &cost
	}
	return row, counted
}

// openCodeCost prices one assistant message at its own model: a Claude row
// bills the cache write at the 5-minute rate (OpenCode records no TTL), a
// Codex row bills every input token with the cache read as its cached part;
// nil (rendered "n/a") when unpriced, never an invented $0.
func (a *auditor) openCodeCost(provider, model string, tokens index.OpenCodeTokens) *float64 {
	if row, found := openCodePriceRow(a.prices.table, provider, model); found {
		switch row.Engine {
		case pricing.EngineClaude:
			if tokens.Cache.Write != 0 {
				a.scan.tierUnknownCalls++
			}
			if usd, ok := row.ClaudeCost(pricing.ClaudeUsage{
				Input: tokens.Input, Output: tokens.Output + tokens.Reasoning, CacheRead: tokens.Cache.Read,
				Write5m: tokens.Cache.Write,
			}); ok {
				return &usd
			}
		case pricing.EngineCodex:
			input := tokens.Input + tokens.Cache.Read + tokens.Cache.Write
			if usd, ok := row.CodexCost(pricing.CodexUsage{
				Input: input, Cached: tokens.Cache.Read, Output: tokens.Output + tokens.Reasoning,
			}, input); ok {
				return &usd
			}
		}
	}
	a.scan.unpricedCalls++
	name := strings.Trim(provider+"/"+model, "/")
	if name == "" {
		name = noModel
	}
	a.scan.unpricedModels.bump(name, 1)
	return nil
}

// openCodePriceRow is the row that prices an OpenCode model: an anthropic or
// openai id as the table reads it with its provider prefix, any other
// provider's (a gateway or a local model) by its bare model id.
func openCodePriceRow(table pricing.Table, provider, model string) (pricing.Row, bool) {
	if provider == providerAnthropic || provider == providerOpenAI {
		return table.Resolve(provider + "/" + model)
	}
	return table.Resolve(model)
}

func (a *auditor) printOpenCode(rows []openCodeRow, sessions int) {
	var total openCodeUsage
	cost, recorded, unpriced := 0.0, 0.0, 0
	for k := range rows {
		row := &rows[k]
		total.in += row.usage.in
		total.read += row.usage.read
		total.write += row.usage.write
		total.out += row.usage.out
		total.reasoning += row.usage.reasoning
		recorded += row.recorded
		if row.cost == nil {
			unpriced++
		} else {
			cost += *row.cost
		}
	}
	count := func(value int64) string { return grouped(float64(value)) }
	cash := func(value *float64) string {
		if value == nil {
			return notPriced
		}
		return "$" + fixed(*value, 2)
	}
	a.print("TOKEN AUDIT · opencode · last " + num(a.hours) + "h · " + strings.Join(a.openCodeRoot, ", ") + " · " +
		num(float64(len(rows))) + " sessions of " + num(float64(sessions)))
	a.print(a.gapsLine())
	a.print("\n" + padEnd("STARTED", 17) + padEnd("SESSION", 14) + padEnd("MODEL", 13) + padEnd("LABEL", 22) +
		padStart("IN", 12) + padStart("CACHED", 12) + padStart("OUT", 11) + padStart("EST USD", 10) +
		padStart("OC USD", 10))
	rowList := firstN(rows, a.opts.top)
	for k := range rowList {
		row := &rowList[k]
		model := row.model
		if model == "" {
			model = "?"
		}
		a.print(padEnd(strings.Replace(utf16Slice(isoTime(row.at), 0, 16), "T", " ", 1), 17) +
			padEnd(utf16Tail(row.id, 12), 14) + padEnd(utf16Slice(model, 0, 12), 13) +
			padEnd(utf16Slice(row.label, 0, 21), 22) + padStart(count(row.usage.input()), 12) +
			padStart(count(row.usage.read), 12) + padStart(count(row.usage.output()), 11) +
			padStart(cash(row.cost), 10) + padStart(cash(&row.recorded), 10))
	}
	if len(rows) > a.opts.top {
		a.print("  … " + num(float64(len(rows)-a.opts.top)) + " further sessions folded into the total below")
	}
	a.print("\nTOTAL " + count(total.input()+total.output()) + " tok · uncached in " + count(total.in) +
		" · cache write " + count(total.write) + " · cached in " + count(total.read) + " · out " +
		count(total.output()) + " (incl. " + count(total.reasoning) + " reasoning) · " + cash(&cost))
	a.print("OC USD is OpenCode's own recorded cost, shown beside EST USD and never in its place: " + cash(&recorded) +
		" in all")
	if unpriced != 0 {
		a.print(num(float64(unpriced)) + ` session(s) ran an unpriced model and show "n/a" — their TOKENS are in ` +
			"the total, their DOLLARS are not.")
	}
}
