package ui

import (
	"math/rand"
	"os"
	"runtime"
	"sort"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/rezzminator/professor/pfm/internal/compose"
)

func TestUIStress(t *testing.T) {
	strict := os.Getenv("PFM_STRESS_STRICT") == "1"
	snapshot := largeSnapshot(5_000)
	started := time.Now()
	model := NewModel(snapshot)
	modelReady := time.Since(started)
	started = time.Now()
	_ = model.View()
	viewReady := time.Since(started)
	firstPaint := modelReady + viewReady
	firstPaintLimit := 2 * time.Second
	if strict {
		firstPaintLimit = 100 * time.Millisecond
	}
	if firstPaint >= firstPaintLimit {
		t.Fatalf(
			"first paint=%s (model=%s view=%s), want <%s (strict=%t)",
			firstPaint,
			modelReady,
			viewReady,
			firstPaintLimit,
			strict,
		)
	}

	const frames = 1_000
	frameTimes := make([]time.Duration, frames)
	for frame := range frames {
		started = time.Now()
		view := model.View()
		if view.Content == "" {
			t.Fatal("empty stress frame")
		}
		frameTimes[frame] = time.Since(started)
	}
	sort.Slice(frameTimes, func(left, right int) bool {
		return frameTimes[left] < frameTimes[right]
	})
	medianFrame := frameTimes[len(frameTimes)/2]
	frameLimit := 2 * time.Second
	if strict {
		frameLimit = 16 * time.Millisecond
	}
	if medianFrame >= frameLimit {
		t.Fatalf(
			"median 5k-row frame=%s, want <%s (strict=%t)",
			medianFrame,
			frameLimit,
			strict,
		)
	}
	t.Logf(
		"STRESS rows=5000 frames=%d first_paint=%s median_frame=%s viewport_rows=%d strict=%t",
		frames,
		firstPaint,
		medianFrame,
		model.pageRows(),
		strict,
	)

	var maxFuzzy time.Duration
	fuzzyLimit := 2 * time.Second
	if strict {
		fuzzyLimit = 10 * time.Millisecond
	}
	for _, runeValue := range "needle" {
		started = time.Now()
		model, _ = applyKey(t, model, printableKey(runeValue))
		elapsed := time.Since(started)
		if elapsed > maxFuzzy {
			maxFuzzy = elapsed
		}
		if elapsed >= fuzzyLimit {
			t.Fatalf(
				"fuzzy query %q over 5k=%s, want <%s (strict=%t)",
				model.Query(),
				elapsed,
				fuzzyLimit,
				strict,
			)
		}
	}
	t.Logf(
		"STRESS fuzzy_rows=5000 keystrokes=6 max_keystroke=%s matches=%d strict=%t",
		maxFuzzy,
		model.FilteredRowCount(),
		strict,
	)

	stressRandomKeys(t)
	stressRefreshStorm(t)
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
		controlKey('e'),
		controlKey('s'),
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
