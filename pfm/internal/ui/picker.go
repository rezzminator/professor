package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	tea "charm.land/bubbletea/v2"
)

func (picker BubblePicker) Pick(
	ctx context.Context,
	snapshot Snapshot,
) (outcome Outcome, returnErr error) {
	openTTY := picker.OpenTTY
	if openTTY == nil {
		openTTY = func() (ReadWriteCloser, error) {
			return os.OpenFile("/dev/tty", os.O_RDWR, 0)
		}
	}
	terminal, err := openTTY()
	if err != nil {
		return Outcome{}, fmt.Errorf("open picker /dev/tty: %w", err)
	}
	defer func() {
		if err := terminal.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close picker /dev/tty: %w", err))
		}
	}()

	samplingContext, cancelSamples := context.WithCancel(ctx)
	defer cancelSamples()
	snapshot.SamplingContext = samplingContext
	model := NewModel(snapshot)
	program := tea.NewProgram(
		model,
		tea.WithContext(ctx),
		tea.WithInput(terminal),
		tea.WithOutput(terminal),
		tea.WithColorProfile(interactiveColorProfile(terminal)),
		tea.WithWindowSize(model.width, model.height),
		// Bubble Tea's own renderer runs an internal flush ticker independent
		// of every application-level backoff above (skyCadence, the fleet
		// refresh), at a rate fixed when the Program starts, for its whole
		// life: 60fps by default, measured ~1.7% of a core idle against an
		// empty fleet. It is also the only path a frame reaches the screen by,
		// so it caps the masthead sweep (sweeptick.go ticks every 33ms): at
		// 10fps the light crossed in ~13 distinct frames, which is the
		// frame-by-frame look the sweep exists to remove. 30fps shows every
		// sweep tick and costs ~0.95% of a core idle against ~0.43% at 10fps
		// (160x50 deck demo, three alternating 20s windows) — inside the 2%
		// idle target (TESTPLAN); the way back is 10 here and a 100ms sweepTickInterval.
		tea.WithFPS(30),
	)
	done := make(chan struct{})
	var updates sync.WaitGroup
	if picker.Updates != nil {
		updates.Add(1)
		go func() {
			defer updates.Done()
			for {
				select {
				case refresh, ok := <-picker.Updates:
					if !ok {
						return
					}
					program.Send(RefreshMsg{Snapshot: refresh})
				case <-done:
					return
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	final, err := program.Run()
	close(done)
	updates.Wait()
	if err != nil {
		return Outcome{}, fmt.Errorf("run picker: %w", err)
	}
	result, ok := final.(Model)
	if !ok {
		return Outcome{}, fmt.Errorf("picker returned model %T", final)
	}
	return result.Result(), nil
}

var _ Picker = BubblePicker{}
