package opencodegen

import "testing"

func TestOpenCodeCommandTransformFlattensKnownNestedCommand(t *testing.T) {
	got := swapOpenCodeCommands("run /tools:review now", map[string]string{"tools:review": "/tools-review"})
	if got != "run /tools-review now" {
		t.Fatalf("command transform = %q", got)
	}
}
