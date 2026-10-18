package deps

import (
	"context"
	"testing"
)

func TestBeginCommitAsksTheGateOnItsContext(t *testing.T) {
	ended := 0
	cases := []struct {
		name    string
		ctx     context.Context
		wantOK  bool
		wantEnd int
	}{
		{name: "no gate always opens", ctx: context.Background(), wantOK: true, wantEnd: 0},
		{
			name: "an open gate opens and its end runs once",
			ctx: WithCommitGate(context.Background(), func() (func(), bool) {
				return func() { ended++ }, true
			}),
			wantOK:  true,
			wantEnd: 1,
		},
		{
			name: "a closed gate refuses",
			ctx: WithCommitGate(context.Background(), func() (func(), bool) {
				return nil, false
			}),
			wantOK:  false,
			wantEnd: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ended = 0
			end, ok := BeginCommit(tc.ctx)
			if ok != tc.wantOK {
				t.Fatalf("BeginCommit ok = %v, want %v", ok, tc.wantOK)
			}
			if ok {
				end()
			}
			if ended != tc.wantEnd {
				t.Fatalf("gate end ran %d times, want %d", ended, tc.wantEnd)
			}
		})
	}
}
