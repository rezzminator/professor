package ui

import (
	"math/rand"
	"runtime"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/rezzminator/professor/pfm/internal/compose"
)

func TestUIStress(t *testing.T) {
	snapshot := largeSnapshot(5_000)
	model := NewModel(snapshot)
	if view := model.View(); view.Content == "" {
		t.Fatal("empty first paint")
	}

	const frames = 1_000
	for range frames {
		view := model.View()
		if view.Content == "" {
			t.Fatal("empty stress frame")
		}
	}
	t.Logf(
		"STRESS rows=5000 frames=%d viewport_rows=%d",
		frames,
		model.pageRows(),
	)

	for _, runeValue := range "needle" {
		model, _ = applyKey(t, model, printableKey(runeValue))
	}
	t.Logf(
		"STRESS fuzzy_rows=5000 keystrokes=6 matches=%d",
		model.FilteredRowCount(),
	)

	stressRandomKeys(t)
	stressRefreshStorm(t)
}

// BenchmarkUIStressFirstPaint is the model build plus first frame over 5,000
// rows, the number TestUIStress asserts the rows of.
func BenchmarkUIStressFirstPaint(b *testing.B) {
	snapshot := largeSnapshot(5_000)
	for b.Loop() {
		_ = NewModel(snapshot).View()
	}
}

// BenchmarkUIStressFrame is one frame over 5,000 rows.
func BenchmarkUIStressFrame(b *testing.B) {
	model := NewModel(largeSnapshot(5_000))
	for b.Loop() {
		_ = model.View()
	}
}

// BenchmarkUIStressFuzzyQuery types "needle" one keystroke at a time over
// 5,000 rows.
func BenchmarkUIStressFuzzyQuery(b *testing.B) {
	base := NewModel(largeSnapshot(5_000))
	for b.Loop() {
		model := base
		for _, runeValue := range "needle" {
			updated, _ := model.Update(printableKey(runeValue))
			model = updated.(Model)
		}
	}
}

func stressRandomKeys(t *testing.T) {
	t.Helper()
	large := NewModel(largeSnapshot(5_000))
	rows := large.VisibleRows()
	seen := make(map[string]struct{}, len(rows))
	for index := range rows {
		row := &rows[index]
		key := compose.RowKey(*row)
		if _, duplicate := seen[key]; duplicate {
			t.Fatalf("natural order duplicated %q", key)
		}
		seen[key] = struct{}{}
	}
	if len(seen) != 5_000 {
		t.Fatalf("natural order covers %d rows", len(seen))
	}

	model := NewModel(largeSnapshot(128))
	random := rand.New(rand.NewSource(8))
	keys := []tea.KeyPressMsg{
		specialKey(tea.KeyUp),
		specialKey(tea.KeyDown),
		specialKey(tea.KeyPgUp),
		specialKey(tea.KeyPgDown),
		specialKey(tea.KeyBackspace),
		controlKey('r'),
		controlKey('x'),
		specialKey(tea.KeyLeft),
		specialKey(tea.KeyRight),
		printableKey('a'),
		printableKey('界'),
	}
	const messages = 10_000
	for index := 0; index < messages; index++ {
		var command tea.Cmd
		model, command = applyKey(
			t,
			model,
			keys[random.Intn(len(keys))],
		)
		_ = command
		if !model.HasVisibleSelection() || !model.ValidUTF8Query() {
			t.Fatalf(
				"invariant failed at %d: cursor=%d rows=%d query=%q",
				index,
				model.Cursor(),
				model.FilteredRowCount(),
				model.Query(),
			)
		}
	}
	t.Logf(
		"STRESS key_messages=%d panics=0 cursor_invariant=true natural_order_bijection=true",
		messages,
	)
}

func stressRefreshStorm(t *testing.T) {
	t.Helper()
	snapshot := largeSnapshot(1_000)
	snapshot.InitialCursorID = snapshot.Rows[500].ID
	model := NewModel(snapshot)
	var follow string
	before := runtime.NumGoroutine()
	const refreshes = 100
	for refresh := 0; refresh < refreshes; refresh++ {
		model, _ = applyKey(t, model, specialKey(tea.KeyDown))
		follow = model.SelectedKey()
		rotated := snapshot
		offset := refresh % len(snapshot.Rows)
		rotated.Rows = append(
			append(
				[]compose.Row(nil),
				snapshot.Rows[offset:]...,
			),
			snapshot.Rows[:offset]...,
		)
		updated, command := model.Update(RefreshMsg{Snapshot: rotated})
		if command != nil {
			t.Fatalf("refresh %d returned command", refresh)
		}
		model = updated.(Model)
		if model.SelectedKey() != follow || !model.HasVisibleSelection() {
			t.Fatalf(
				"refresh %d selected=%q want=%q",
				refresh,
				model.SelectedKey(),
				follow,
			)
		}
	}
	runtime.GC()
	after := runtime.NumGoroutine()
	if after > before {
		t.Fatalf("refresh storm goroutines before=%d after=%d", before, after)
	}
	t.Logf(
		"STRESS refreshes=%d cursor_follow=true goroutines_before=%d after=%d",
		refreshes,
		before,
		after,
	)
}
