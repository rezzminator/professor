package mockengine

import (
	"context"
	"errors"
	"fmt"
)

func (loop *paneLoop) attention(step Step) (approved bool, resultErr error) {
	engine, ok := loop.engine.(interface {
		requestAttention(Step) error
		answerAttention(Step, bool) error
	})
	if !ok {
		return false, fmt.Errorf("%s has no attention protocol", loop.proc.engine)
	}
	if err := engine.requestAttention(step); err != nil {
		return false, err
	}
	if decision, ok := loop.engine.(interface{ attentionDecision() (bool, bool) }); ok {
		if approved, decided := decision.attentionDecision(); decided {
			return approved, engine.answerAttention(step, approved)
		}
	}
	title := step.Message
	if title == "" {
		title = "Permission needed"
	}
	loop.screen.footer = ""
	loop.screen.say(title)
	options := step.Options
	if len(options) == 0 {
		options = []string{"Allow", "Deny"}
	}
	loop.screen.menu = &Step{Type: StepMenu, Name: title, Options: options, Selected: 1}
	loop.screen.selected = 1
	loop.paint()
	cancel, events, err := loop.startAttentionEvents(step.During)
	if err != nil {
		return false, err
	}
	stopEvents := func() error {
		cancel()
		if events == nil {
			return nil
		}
		err := <-events
		events = nil
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, stopEvents()) }()
	for {
		select {
		case <-loop.proc.ctx.Done():
			return false, loop.proc.ctx.Err()
		case err := <-events:
			events = nil
			if err != nil {
				return false, err
			}
		case event, ok := <-loop.keys:
			if !ok {
				return false, fmt.Errorf("%s approval input closed", loop.proc.engine)
			}
			var approved bool
			switch event.kind {
			case keyEscape:
				approved = false
			case keyEnter:
				approved = step.Type == StepQuestion || loop.screen.selected == 1
			case keyRune:
				if event.r >= '1' && int(event.r-'0') <= len(options) {
					loop.screen.selected = int(event.r - '0')
					loop.paint()
				}
				continue
			default:
				continue
			}
			if err := stopEvents(); err != nil {
				return false, err
			}
			loop.screen.menu = nil
			step.Selected = loop.screen.selected
			if err := engine.answerAttention(step, approved); err != nil {
				return false, err
			}
			return approved, nil
		}
	}
}
