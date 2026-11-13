package tokens

import (
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
		want string
	}{
		{
			name: "an untitled main run shows its session's first 8 characters",
			r: &run{
				kind: "main", sid: "0123456789ab", unpriced: true,
				topRepeat: &repeatedCommand{cmd: "go test ./...", n: 3},
			},
			want: "        n/a  MAIN 01234567                                ?                0 calls · ctx   0→  0K ·    0m wall/   0m in tools · rw  $0.00 · errs   0 · reread   0 · poll  $0.00 · ttl — · 3× \"go test ./...\"",
		},
		{
			name: "a sub-agent without a type is an agent",
			r:    &run{kind: "agent", title: "fix the parser", calls: 7},
			want: "      $0.00  agent: fix the parser                        ?                7 calls · ctx   0→  0K ·    0m wall/   0m in tools · rw  $0.00 · errs   0 · reread   0 · poll  $0.00 · ttl —",
		},
		{
			name: "a typed sub-agent shows its type",
			r:    &run{kind: "agent", agentType: "flights:lander", title: "land"},
			want: "      $0.00  flights:lander: land                         ?                0 calls · ctx   0→  0K ·    0m wall/   0m in tools · rw  $0.00 · errs   0 · reread   0 · poll  $0.00 · ttl —",
		},
	}
	for _, c := range cases {
		if got := a.runLine(c.r); got != c.want {
			t.Errorf("%s: runLine = %q, want %q", c.name, got, c.want)
		}
	}
}
