package tokens

// advisorSpend is what advisor models billed: the dollars and tokens (already
// in the run's totals, which they raise) and the consultations that billed
// them, priced or not. The advisor is another model than the executor's, so its
// spend is booked apart, never as an executor call. A consultation the table
// cannot price is ignorance: with one among them the dollars render "n/a".
type advisorSpend struct {
	usd, calls, unpriced float64
	tok                  tokenCounts
}

// book adds one consultation: its dollars, or none when priced is false.
func (s *advisorSpend) book(usd float64, priced bool, tok tokenCounts) {
	consultation := advisorSpend{usd: usd, calls: 1, tok: tok}
	if !priced {
		consultation.unpriced = 1
	}
	s.merge(consultation)
}

func (s *advisorSpend) merge(other advisorSpend) {
	s.calls += other.calls
	s.usd += other.usd
	s.unpriced += other.unpriced
	s.tok.add(other.tok.in, other.tok.out, other.tok.cw5, other.tok.cw1, other.tok.cr)
}

// cash is the consultations' dollars as the report prints them: "n/a" when
// the table could not price one of them, never a quiet "$0.00".
func (s advisorSpend) cash() string {
	if s.unpriced != 0 {
		return notPriced
	}
	return dollars(s.usd)
}

// usdJSON is the consultations' dollars for the JSON: null when the table could
// not price one of them.
func (s advisorSpend) usdJSON() any {
	if s.unpriced != 0 {
		return nil
	}
	return s.usd
}

func (s advisorSpend) json() *object {
	return newObject().set("usd", s.usdJSON()).set("calls", s.calls).set("unpriced", s.unpriced).
		set("tok", s.tok.json())
}

// bookAdvisors prices the call's advisor consultations, each at the advisor's
// own model and the context it was billed at, and books them to the run and the
// totals: dollars and tokens in the billing columns, the band, hour and window
// bin of the consultation, and the advisor tally the report shows apart. It
// returns the consultations booked. An advisor model the table cannot price is
// ignorance, as for any call: its tokens count, the run renders "n/a".
func (a *auditor) bookAdvisors(r *run, call *callUsage, ts float64, project string) advisorSpend {
	g := a.global
	var booked advisorSpend
	for _, advisor := range call.advisors {
		u := advisor.Usage
		cw5, cw1 := float64(u.Write5m), float64(u.Write1h)
		inp, out, cr := float64(u.Input), float64(u.Output), float64(u.CacheRead)
		tok := tokenCounts{in: inp, out: out, cw5: cw5, cw1: cw1, cr: cr}
		ctx := inp + cr + cw5 + cw1
		row, _, found := a.prices.rates(advisor.Model, ctx)
		parts, priced := row.ClaudeCostParts(u)
		r.tok.add(inp, out, cw5, cw1, cr)
		g.tok.add(inp, out, cw5, cw1, cr)
		if !found || !priced {
			name := advisor.Model
			if name == "" {
				name = noModel
			}
			a.scan.unpricedCalls++
			a.scan.unpricedModels.bump(name, 1)
			r.unpriced = true
			r.advisor.book(0, false, tok)
			g.advisor.book(0, false, tok)
			booked.book(0, false, tok)
			continue
		}
		if cw5+cw1 != 0 && !advisor.Split {
			a.scan.tierUnknownCalls++
		}
		usd := parts.Total()
		a.bookSpend(r, parts, ctx, out, ts, r.kind+" · "+modelStem(advisor.Model)+" · advisor", project)
		r.advisor.book(usd, true, tok)
		g.advisor.book(usd, true, tok)
		booked.book(usd, true, tok)
	}
	return booked
}
