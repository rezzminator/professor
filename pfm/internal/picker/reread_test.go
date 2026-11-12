package picker

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/fleet"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/ui"
)

// TestRereadFactsRefreshesHerdrBlockedMarks pins the parked picker's read of
// Herdr's blocked state: a question answered while the picker is parked drops
// the row's mark on the next re-read (and a new one raises it), one pane.list
// per socket, and a socket that cannot be read keeps the composed mark and
// names itself in the row-facts error.
func TestRereadFactsRefreshesHerdrBlockedMarks(t *testing.T) {
	row := func(socket, pane string, blocked bool) compose.Row {
		return compose.Row{Kind: compose.LiveClaude, Backend: "herdr", Socket: socket, PaneID: pane, Blocked: blocked}
	}
	output := compose.Output{Rows: []compose.Row{
		row("/run/a.sock", "w1:p1", true),
		row("/run/a.sock", "w1:p2", false),
		row("/run/b.sock", "w1:p1", true),
		{Kind: compose.LiveClaude, Socket: "tmux-sock", PaneID: "%1", Blocked: true},
	}}
	reads := map[string]int{}
	request := scanRequest{HerdrBlocked: func(_ context.Context, socket string) (map[string]bool, error) {
		reads[socket]++
		if socket == "/run/b.sock" {
			return nil, errors.New("connection refused")
		}
		return map[string]bool{"w1:p1": false, "w1:p2": true}, nil
	}}
	last := sentRefresh{output: output, snapshot: ui.Snapshot{Rows: output.Rows}}
	got := rereadFacts(context.Background(), fleet.Env{Paths: paths.Values{Home: t.TempDir()}}, request, last)
	marks := []bool{got.Rows[0].Blocked, got.Rows[1].Blocked, got.Rows[2].Blocked, got.Rows[3].Blocked}
	if want := []bool{false, true, true, true}; !reflect.DeepEqual(marks, want) {
		t.Errorf("blocked marks after the re-read = %v, want %v", marks, want)
	}
	if want := map[string]int{"/run/a.sock": 1, "/run/b.sock": 1}; !reflect.DeepEqual(reads, want) {
		t.Errorf("pane.list reads per socket = %v, want %v", reads, want)
	}
	if want := "herdr blocked marks /run/b.sock: connection refused"; got.FactsError != want {
		t.Errorf("row facts error = %q, want %q", got.FactsError, want)
	}
	if !rowFactsMoved(last.snapshot, got) {
		t.Error("a changed blocked mark must count as a change to publish")
	}
}
