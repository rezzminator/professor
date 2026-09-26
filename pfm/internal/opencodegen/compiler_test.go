package opencodegen

import "testing"

func TestResolveOpenCodePathUsesWorkingDirectoryWhenUnset(t *testing.T) {
	if _, err := resolveOpenCodePath("", "repository root"); err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
}
