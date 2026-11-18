package statusline

import "testing"

func TestSpendTextKeepsItsFactsInEveryForm(t *testing.T) {
	whole := spendText{
		usd: 4.68, usdKnown: true, in: 6_500_000, out: 31_000, countsKnown: true, tools: 46, toolsKnown: true,
	}
	flagged := spendText{
		usd: 8, usdKnown: true, floor: true, in: 1_000_000, out: 5_100, countsKnown: true, tools: 1, toolsKnown: true,
		marker: "pre-ledger", gateway: gatewaySpend{present: true},
	}
	unread := spendText{usd: 3.42, usdKnown: true, in: 420_000, countsKnown: true}
	for _, tc := range []struct {
		name  string
		spend spendText
		form  spendForm
		want  string
	}{
		{"words", whole, spendWords, "Σ $4.68 · 6.5M in · 31.0K out · 46 tools"},
		{"slashes", whole, spendSlash, "Σ $4.68/6.5M/31.0K/46"},
		{"dollars alone", whole, spendUSD, "Σ $4.68"},
		{
			"a floor, one tool, a marker and the gateway's note in words", flagged, spendWords,
			"Σ $8.00+? · 1.0M in · 5.1K out · 1 tool pre-ledger API-eq",
		},
		{"the same in slashes", flagged, spendSlash, "Σ $8.00+?/1.0M/5.1K/1 pre-ledger API-eq"},
		{"the same as dollars alone", flagged, spendUSD, "Σ $8.00+? pre-ledger API-eq"},
		{"nothing known in words", spendText{}, spendWords, "Σ $? · ? in · ? out · ? tools"},
		{"nothing known in slashes", spendText{}, spendSlash, "Σ $?/?/?/?"},
		{"tool calls unread", unread, spendWords, "Σ $3.42 · 420.0K in · 0 out · ? tools"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripANSICodes(tc.spend.text(tc.form)); got != tc.want {
				t.Errorf("spend = %q, want %q", got, tc.want)
			}
		})
	}
	styled := "\x1b[38;5;245mΣ\x1b[0m \x1b[38;5;226m$8.00\x1b[0m\x1b[38;5;226m+?\x1b[0m \x1b[38;5;245m·\x1b[0m " +
		"\x1b[1;97m1.0M\x1b[0m \x1b[38;5;245min\x1b[0m \x1b[38;5;245m·\x1b[0m \x1b[1;97m5.1K\x1b[0m " +
		"\x1b[38;5;245mout\x1b[0m \x1b[38;5;245m·\x1b[0m \x1b[38;5;87m1\x1b[0m \x1b[38;5;245mtool\x1b[0m" +
		"\x1b[38;5;226m pre-ledger\x1b[0m\x1b[38;5;245m API-eq\x1b[0m"
	if got := flagged.text(spendWords); got != styled {
		t.Errorf("styled spend = %q, want %q", got, styled)
	}
}

func TestSpendDollarsTakeTheirToneFromTheirSize(t *testing.T) {
	for _, tc := range []struct {
		usd  float64
		want string
	}{
		{0, "\x1b[38;5;245mΣ\x1b[0m \x1b[97m$0.00\x1b[0m"},
		{1.99, "\x1b[38;5;245mΣ\x1b[0m \x1b[97m$1.99\x1b[0m"},
		{2, "\x1b[38;5;245mΣ\x1b[0m \x1b[38;5;226m$2.00\x1b[0m"},
		{9.99, "\x1b[38;5;245mΣ\x1b[0m \x1b[38;5;226m$9.99\x1b[0m"},
		{10, "\x1b[38;5;245mΣ\x1b[0m \x1b[1;38;5;203m$10.00\x1b[0m"},
		{1234.5, "\x1b[38;5;245mΣ\x1b[0m \x1b[1;38;5;203m$1234.50\x1b[0m"},
	} {
		if got := (spendText{usd: tc.usd, usdKnown: true}).text(spendUSD); got != tc.want {
			t.Errorf("$%v = %q, want %q", tc.usd, got, tc.want)
		}
	}
	// A sum nothing could price or count warns in the place of each figure.
	unknown := "\x1b[38;5;245mΣ\x1b[0m \x1b[38;5;226m$?\x1b[0m\x1b[38;5;245m/\x1b[0m\x1b[38;5;226m?\x1b[0m" +
		"\x1b[38;5;245m/\x1b[0m\x1b[38;5;226m?\x1b[0m\x1b[38;5;245m/\x1b[0m\x1b[38;5;226m?\x1b[0m"
	if got := (spendText{}).text(spendSlash); got != unknown {
		t.Errorf("unknown spend = %q, want %q", got, unknown)
	}
}
