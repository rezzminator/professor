package testjail

import "strings"

// HerdrPaneInfo returns the required Herdr 0.9.5 PaneInfo identity fields.
// Optional fields belong to the test exercising them, not this common fixture.
func HerdrPaneInfo(pane string) map[string]any {
	workspace, _, _ := strings.Cut(pane, ":")
	return map[string]any{
		"type": "pane_info",
		"pane": map[string]any{
			"pane_id": pane, "workspace_id": workspace,
			"terminal_id": "fixture-" + pane, "tab_id": workspace + ":t1", "focused": true,
		},
	}
}
