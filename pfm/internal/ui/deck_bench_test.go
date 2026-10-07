package ui

import (
	"fmt"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/rezzminator/professor/pfm/internal/compose"
)

// benchFleet is deckFleet grown to a real machine's size: the cost of a frame
// is what the picker spends every tick of the sky and the clock.
func benchFleet(width, height, copies int) Snapshot {
	snapshot := deckFleet(width, height)
	base := snapshot.Rows
	rows := make([]compose.Row, 0, len(base)*copies)
	for generation := range copies {
		for index := range base {
			row := base[index]
			if row.ID != "" {
				row.ID = fmt.Sprintf("%s-%02d", row.ID, generation)
			}
			if row.Name != "" && row.Kind != compose.ProfessorUpdate {
				row.Name = fmt.Sprintf("%s %02d", row.Name, generation)
			}
			rows = append(rows, row)
		}
	}
	snapshot.Rows = rows
	return snapshot
}

// BenchmarkDeckView is one frame of the deck: the cost the ambient tick pays.
func BenchmarkDeckView(b *testing.B) {
	for _, size := range []struct{ width, height int }{{120, 40}, {200, 60}} {
		b.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(b *testing.B) {
			model := NewModel(benchFleet(size.width, size.height, 12))
			model.nowNS = fixtureNowNS
			b.ReportAllocs()
			b.ResetTimer()
			for index := range b.N {
				model.nowNS = fixtureNowNS + int64(index)*workFrameNS
				_ = model.View().Content
			}
		})
	}
}

// BenchmarkDeckViewWhileSweeping is a frame while the masthead's light crosses.
func BenchmarkDeckViewWhileSweeping(b *testing.B) {
	model := NewModel(benchFleet(160, 50, 12))
	model.nowNS = fixtureNowNS
	model.activity = NewActivityClock(time.Unix(0, fixtureNowNS-int64(400*time.Millisecond)))
	b.ReportAllocs()
	b.ResetTimer()
	for index := range b.N {
		model.nowNS = fixtureNowNS + int64(index%30)*int64(sweepTickInterval)
		_ = model.View().Content
	}
}

// BenchmarkDeckKey is the cost of one cursor key: the model update plus its frame.
func BenchmarkDeckKey(b *testing.B) {
	model := NewModel(benchFleet(160, 50, 12))
	model.nowNS = fixtureNowNS
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		updated, _ := model.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		model = updated.(Model)
		_ = model.View().Content
	}
}
