package tokens

import "testing"

func TestFlightUnits(t *testing.T) {
	cases := []struct{ got, want string }{
		{flightTokens(999), "999"},
		{flightTokens(1500), "2K"},
		{flightTokens(2.5e6), "2.5M"},
		{flightCash(&run{unpriced: true, usd: 4}), "n/a"},
		{flightCash(&run{usd: 1.234}), "$1.23"},
		{flightMinutes(&run{t1: 180e3}), "?"},
		{flightMinutes(&run{t0: 60e3, t1: 180e3}), "2m"},
		{flightGrowth(&run{}), "?"},
		{flightGrowth(&run{calls: 2, ctxFirst: 1000, ctxPeak: 5000}), "2K"},
		{flightGrowth(&run{calls: 2, ctxFirst: 5000, ctxPeak: 1000}), "0"},
		{
			ledgerType(flightRow{led: ledgerRow{agentType: "flights-lander"}, run: &run{agentType: "x"}}),
			"flights-lander",
		},
		{ledgerType(flightRow{run: &run{agentType: "tracer"}}), "tracer"},
		{ledgerType(flightRow{run: &run{}}), "?"},
	}
	for i, c := range cases {
		if c.got != c.want {
			t.Errorf("case %d = %q, want %q", i, c.got, c.want)
		}
	}
}

func TestFoldRowsCountsOverCapAndTheLongContextPrice(t *testing.T) {
	rows := []flightRow{
		{
			led: ledgerRow{agentType: "flights-foreman"},
			run: &run{
				calls:   300,
				usd:     2,
				usdLC:   3,
				ctxPeak: 900,
				tok:     tokenCounts{in: 1, cw5: 2, cw1: 3, cr: 4, out: 5},
			},
		},
		{run: &run{agentType: "tracer", calls: 160, usd: 1, unpriced: true, ctxPeak: 400, pollN: 2, resets: 1}},
		{run: &run{agentType: "tracer", calls: 10, usd: 0.5, advisor: advisorSpend{usd: 0.25, calls: 1}}},
	}
	f := foldRows(rows)
	want := flightFold{
		n: 3, calls: 470, usd: 3.5, unpriced: 1, in: 6, cr: 4, out: 5, poll: 2, compact: 1, over: 2,
		peak: 900, lc: 4.5, advisor: advisorSpend{usd: 0.25, calls: 1},
	}
	if f != want {
		t.Errorf("foldRows = %+v, want %+v", f, want)
	}
	if got := stringify(
		f.json(),
		0,
	); got != `{"n":3,"calls":470,"usd":3.5,"unpriced":1,"in":6,"cr":4,"out":5,"fail":0,`+
		`"poll":2,"reread":0,"contract":0,"compact":1,"over":2,"peak":900,"lc":4.5}` {
		t.Errorf("json = %s", got)
	}
}
