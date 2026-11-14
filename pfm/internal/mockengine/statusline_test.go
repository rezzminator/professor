package mockengine

import (
	"testing"
	"time"
)

// TestStatusRunBound pins how long Claude Code lets a statusline run live: the refresh tick aborts it
// when that comes before 5 s, and 5 s does otherwise, a pane with no refreshInterval included.
func TestStatusRunBound(t *testing.T) {
	for _, row := range []struct {
		refresh, want time.Duration
	}{
		{0, 5 * time.Second},
		{2 * time.Second, 2 * time.Second},
		{5*time.Second - time.Millisecond, 5*time.Second - time.Millisecond},
		{5 * time.Second, 5 * time.Second},
		{5*time.Second + time.Millisecond, 5 * time.Second},
	} {
		if got := statusRunBound(row.refresh); got != row.want {
			t.Errorf("statusRunBound(%s) = %s, want %s", row.refresh, got, row.want)
		}
	}
}
