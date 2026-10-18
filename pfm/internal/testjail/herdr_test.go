package testjail

import "testing"

func TestHerdrPaneInfoIsolatesPerRequestOverrides(t *testing.T) {
	first := HerdrPaneInfo("w2:p3")
	first["pane"].(map[string]any)["terminal_id"] = "moved-terminal"
	second := HerdrPaneInfo("w2:p3")
	if second["pane"].(map[string]any)["terminal_id"] == "moved-terminal" {
		t.Fatal("one request's terminal override leaked into another fixture")
	}
}
