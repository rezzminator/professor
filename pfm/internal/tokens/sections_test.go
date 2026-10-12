package tokens

import (
	"strings"
	"testing"
)

func TestRunUSDNeverRendersIgnoranceAsZero(t *testing.T) {
	if got := runUSD(&run{unpriced: true, usd: 3}); got != "n/a" {
		t.Errorf("unpriced runUSD = %q, want n/a", got)
	}
	if got, want := runUSD(&run{usd: 3}), dollars(3); got != want {
		t.Errorf("priced runUSD = %q, want %q", got, want)
	}
}

func TestRunLineNamesTheRunAndItsTopRepeat(t *testing.T) {
	a, _, _ := testAuditor(t)
	cases := []struct {
		name string
		r    *run
		want []string
	}{
		{
			name: "an untitled main run shows its session's first 8 characters",
			r: &run{
				kind: "main", sid: "0123456789ab", unpriced: true,
				topRepeat: &repeatedCommand{cmd: "go test ./...", n: 3},
			},
			want: []string{"      n/a  MAIN 01234567", ` · 3× "go test ./..."`},
		},
		{
			name: "a sub-agent without a type is an agent",
			r:    &run{kind: "agent", title: "fix the parser", calls: 7},
			want: []string{"  agent: fix the parser", "     7 calls · ctx"},
		},
		{
			name: "a typed sub-agent shows its type",
			r:    &run{kind: "agent", agentType: "flights-lander", title: "land"},
			want: []string{"flights-lander: land"},
		},
	}
	for _, c := range cases {
		line := a.runLine(c.r)
		for _, part := range c.want {
			if !strings.Contains(line, part) {
				t.Errorf("%s: runLine = %q, want it to hold %q", c.name, line, part)
			}
		}
	}
}
