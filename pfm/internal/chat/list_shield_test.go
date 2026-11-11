package chat

import (
	"context"
	"fmt"
	"testing"
	"time"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// A listing's rows all prove their native panes through the one shield the
// listing holds: herdr-shield runs once per socket however many rows there
// are, even when every run outlasts a listing's freshness.
func TestInspectRowsListsHerdrShieldOncePerSocket(t *testing.T) {
	const socket = "/run/fixture/herdr/herdr.sock"
	runs := testjail.StageHerdrShield(t, testjail.HerdrShieldListing(5, 2, 10, 3, "w1:p1", socket), 0)
	shield := gather.HerdrShield(gather.NewProcFS(t.TempDir()))
	fake := clock.NewFake(time.Date(2026, 11, 8, 2, 0, 0, 0, time.UTC))
	shield.Clock = fake
	rows := make([]compose.Row, 4)
	var shared []*herdr.Shield
	statuses, errs := InspectRows(
		context.Background(), rows, fake.Now(), shield,
		func(ctx context.Context, _ headless.Chat, _ time.Time, proofs pfmcall.HerdrProofs) (headless.Status, error) {
			shared = append(shared, proofs.Shield)
			fake.Advance(3 * time.Second) // a slow inspection: a listing is stale before the next row
			holders, err := proofs.Shield.Running(ctx, socket)
			if err == nil && len(holders) != 1 {
				err = fmt.Errorf("listed %d holders; want 1", len(holders))
			}
			return headless.Status{State: headless.StateIdle}, err
		},
	)
	if len(statuses) != len(rows) || len(errs) != len(rows) || len(shared) != len(rows) {
		t.Fatalf("statuses=%d errs=%d inspected=%d; want %d each", len(statuses), len(errs), len(shared), len(rows))
	}
	for i := range rows {
		if errs[i] != nil || statuses[i].State != headless.StateIdle || shared[i] != shield {
			t.Fatalf("row %d: status=%+v err=%v shield shared=%v; want idle through the listing's shield",
				i, statuses[i], errs[i], shared[i] == shield)
		}
	}
	if got := runs(); got != 1 {
		t.Fatalf("herdr-shield list ran %d times for %d rows on one socket; want 1", got, len(rows))
	}
}
