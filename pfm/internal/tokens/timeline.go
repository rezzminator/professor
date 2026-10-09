package tokens

import (
	"path/filepath"
	"strings"
)

// timelineTokens is a token count as a timeline row shows it, one decimal.
func timelineTokens(value float64) string {
	switch {
	case value >= 1e6:
		return fixed(value/1e6, 1) + "M"
	case value >= 1000:
		return fixed(value/1000, 1) + "K"
	}
	return num(round(value))
}

func timelineCash(usd *float64) string {
	if usd == nil {
		return notPriced
	}
	return "$" + fixed(*usd, 4)
}

func seconds(ms float64) string { return fixed(ms/1000, 1) + "s" }

// runTimeline prints one row per model call of each --timeline transcript,
// priced by the same replay; an unreadable file is a failure, never a skip.
func (a *auditor) runTimeline() (int, error) {
	failed := 0
	for i, given := range a.opts.timeline {
		// the gaps line speaks for THIS file only
		a.scan = newScanState()
		if i != 0 {
			a.print("")
		}
		file, err := filepath.Abs(given)
		if err != nil {
			failed++
			a.print("UNREADABLE — " + given + ": " + err.Error())
			continue
		}
		_, files, err := a.ownership(file)
		if err != nil {
			return 1, err
		}
		r, err := a.audit(file, a.callOrigins(files))
		if err != nil {
			failed++
			a.print("UNREADABLE — " + given + ": " + describe(err))
			continue
		}
		if r == nil {
			a.print("NO CALLS — " + given)
			a.print(a.gapsLine())
			if len(a.scan.readErrors) != 0 {
				failed++
			}
			continue
		}
		a.printTimeline(r, given)
		a.print(a.gapsLine())
		if len(a.scan.readErrors) != 0 {
			failed++
		}
	}
	if failed != 0 {
		return 1, nil
	}
	return 0, nil
}

func (a *auditor) printTimeline(r *run, given string) {
	who := r.agentType
	if r.kind == kindMain {
		who = kindMain
	} else if who == "" {
		who = "agent (type unknown: no .meta.json)"
	}
	models := r.models.ordered()
	for i, model := range models {
		models[i] = modelStem(model)
	}
	usd := notPriced
	if !r.unpriced {
		usd = "$" + fixed(r.usd, 4)
	}
	a.print("TIMELINE " + who + " · " + strings.Join(models, "+") + " · effort " +
		strings.Join(r.efforts.ordered(), "/") + " · " + num(r.calls) + " calls · wall " +
		num(round((r.recT1-r.recT0)/1000)) + "s · peak ctx " + timelineTokens(r.ctxPeak) + " · out " + num(r.tok.out) +
		" tok · tool errors " + num(r.errs) + " · results >20KB " + num(r.big) + " · " + usd + " · " + given)
	for _, row := range r.timeline {
		var tools []string
		for _, slot := range row.tools {
			result := "(no result)"
			if slot.hasResult {
				result = num(slot.chars) + "ch"
				if slot.err {
					result += " ERR"
				}
				result += " " + seconds(slot.dur)
			}
			tools = append(tools, slot.name+": "+slot.target+" "+result)
		}
		gap := "-"
		if row.gap != nil {
			gap = seconds(*row.gap)
		}
		toolText := strings.Join(tools, " | ")
		if toolText == "" {
			toolText = "(no tools)"
		}
		a.print("  #" + num(row.n) + " " + utf16Slice(isoTime(row.ts), 11, 19) + " after " + gap + " · ctx " +
			timelineTokens(row.ctx) + " · out " + num(row.out) + " · " + timelineCash(row.usd) + " · " + toolText)
	}
}
