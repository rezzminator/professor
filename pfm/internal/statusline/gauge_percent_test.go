package statusline

import "testing"

// A context share rounds to the nearest whole percent, and a share below a
// full window never rounds up to 100.
func TestGaugePercentRoundsToNearest(t *testing.T) {
	for share, want := range map[float64]int{
		0: 0, 0.4: 0, 6.91: 7, 42.9: 43, 50.5: 51, 99.4: 99, 99.96: 99, 100: 100, 120: 120,
	} {
		if got := gaugePercent(share); got != want {
			t.Errorf("gaugePercent(%v) = %d, want %d", share, got, want)
		}
	}
}
