package tokens

import (
	"math"
	"strings"
)

// countContext counts one non-call event inside the window: the tool, Bash and
// injection tallies the report reads.
func (a *auditor) countContext(r *run, e event, project string, cmdCount *object, lastToolTS *float64) {
	if e.tool != "" && !math.IsNaN(e.ts) {
		*lastToolTS = e.ts
	}
	if e.ts >= a.since && e.tool != "" {
		tool := r.tools.childWith(e.tool, "n", "chars", "err")
		tool.bump("n", 1)
		tool.bump("chars", e.chars)
		if e.err {
			tool.bump("err", 1)
			r.errs++
		}
		if e.bash != noBash {
			bash := r.bash.childWith(categories[e.bash], "n", "chars", "err", "waitMs")
			bash.bump("n", 1)
			bash.bump("chars", e.chars)
			bash.bump("waitMs", e.dur)
			if e.err {
				bash.bump("err", 1)
			}
			cmdCount.bump(e.target, 1)
		}
		if e.cat == catReadAgain {
			r.rereadN++
			r.rereadChars += e.chars
			key := project + " · " + strings.Replace(e.target, project+"/", "", 1)
			a.global.rereads.bump2(key, "n", 1)
			a.global.rereads.bump2(key, "chars", e.chars)
		}
		// the project contract the agent re-opens: by Read, or by a shell read of it
		if contract.MatchString(e.target) && (e.tool == toolRead || e.bash == catBashRead) {
			r.contractReads++
		}
		if e.tool == toolBash && e.err {
			r.failedCmds++
		}
		r.toolWaitMs += e.dur
	}
	if e.ts >= a.since && e.att != "" {
		a.global.attach.bump2(e.att, "n", 1)
		a.global.attach.bump2(e.att, "chars", e.chars)
		r.attach.bump2(e.att, "n", 1)
		r.attach.bump2(e.att, "chars", e.chars)
	}
}
