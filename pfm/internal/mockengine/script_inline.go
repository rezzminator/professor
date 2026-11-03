package mockengine

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// An inline directive may end with pfm's same-line delivery footer.
const inlineMarker = "mock-engine: "

func parseInlineSteps(prompt string) ([]Step, error) {
	_, directive, found := strings.Cut(prompt, inlineMarker)
	if !found {
		return nil, nil
	}
	directive, _, _ = strings.Cut(directive, "\n")
	directive = strings.TrimSpace(directive)
	decoder := json.NewDecoder(strings.NewReader(directive))
	decoder.DisallowUnknownFields()
	steps := []Step{}
	switch {
	case strings.HasPrefix(directive, "{"):
		var step Step
		if err := decoder.Decode(&step); err != nil {
			return nil, fmt.Errorf("decode step: %w", err)
		}
		steps = append(steps, step)
	case strings.HasPrefix(directive, "["):
		if err := decoder.Decode(&steps); err != nil {
			return nil, fmt.Errorf("decode steps: %w", err)
		}
	default:
		return nil, errors.New("expected a step object or array")
	}
	if !strings.HasPrefix(strings.TrimLeft(directive[decoder.InputOffset():], " "), "— ") {
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			if err != nil {
				return nil, fmt.Errorf("decode trailing input: %w", err)
			}
			return nil, errors.New("unexpected JSON after inline steps")
		}
	}
	for index := range steps {
		if err := steps[index].validate(); err != nil {
			return nil, fmt.Errorf("step %d: %w", index, err)
		}
	}
	return steps, nil
}

func (running *script) forPrompt(prompt string) *script {
	steps, err := parseInlineSteps(prompt)
	if err != nil {
		steps = []Step{
			{Type: StepCrash, ExitCode: ExitUnpinned, Reply: "mock-engine: inline steps refused — " + err.Error()},
		}
	} else if steps == nil {
		return running
	}
	local := *running
	local.Steps, local.position, local.cursor = steps, 0, ""
	return &local
}
