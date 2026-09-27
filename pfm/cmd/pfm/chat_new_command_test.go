package main

import (
	"bytes"
	"strings"
	"testing"
)

// A seat named positionally keeps every flag written after its name: the
// flag parser must not stop at the name and silently birth a default chat.
func TestChatNewReadsFlagsAfterAPositionalName(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"chat", "new", "seat-x", "--timeout", "-1"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf(
			"chat new seat-x --timeout -1 = %d, want 2 (usage: the negative timeout after the name was read); stderr=%q",
			code,
			stderr.String(),
		)
	}
	if !strings.Contains(stderr.String(), "usage: pfm chat new") {
		t.Fatalf("stderr = %q, want the chat new usage line", stderr.String())
	}
}
