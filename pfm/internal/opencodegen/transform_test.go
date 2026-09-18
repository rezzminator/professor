package opencodegen

import "testing"

func TestOpenCodeCommandTransformFlattensKnownNestedCommand(t *testing.T) {
	got := swapOpenCodeCommands("run /wave:review now", map[string]string{"wave:review": "/wave-review"})
	if got != "run /wave-review now" {
		t.Fatalf("command transform = %q", got)
	}
}
