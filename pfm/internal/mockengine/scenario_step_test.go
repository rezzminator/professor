package mockengine

import "testing"

// TestStepValidateAndTerminal is the step table: each kind's required fields,
// and which kinds end the turn they run in.
func TestStepValidateAndTerminal(t *testing.T) {
	cases := []struct {
		step     Step
		err      string
		terminal bool
	}{
		{step: Step{Type: StepTurn}, terminal: true},
		{step: Step{Type: StepMenu, Options: []string{"yes"}}, terminal: true},
		{step: Step{Type: StepMenu}, err: "menu needs options", terminal: true},
		{step: Step{Type: StepCrash}, terminal: true},
		{step: Step{Type: StepExit}, terminal: true},
		{step: Step{Type: StepAPIError}, terminal: true},
		{step: Step{Type: StepAPIError, Reply: "API Error: 429", Error: "rate_limit"}, terminal: true},
		{step: Step{Type: StepHold}, err: "hold needs until_gone"},
		{step: Step{Type: StepHold, UntilGone: "/gate"}},
		{step: Step{Type: StepAITitle}, err: "ai_title needs name"},
		{step: Step{Type: StepAITitle, Name: "a title"}},
		{step: Step{Type: StepPermission}, err: "permission needs tool", terminal: true},
		{step: Step{Type: StepPermission, Tool: "Bash"}, terminal: true},
		{step: Step{Type: StepCompact}},
		{step: Step{Type: StepMCP}},
		{step: Step{Type: StepBackgroundAgent}},
		{step: Step{Type: "rest"}, err: `unknown step type "rest"`},
	}
	for _, tc := range cases {
		err := tc.step.validate()
		got := ""
		if err != nil {
			got = err.Error()
		}
		if got != tc.err || tc.step.terminal() != tc.terminal {
			t.Errorf("%+v: validate = %q, terminal = %t; want %q, %t", tc.step, got, tc.step.terminal(), tc.err,
				tc.terminal)
		}
	}
}
