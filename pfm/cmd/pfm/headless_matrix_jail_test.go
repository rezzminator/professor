//go:build jail

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// TestUnknownChatIsRc4WithAMachineShape is STM's hard rule at the CLI edge:
// never empty output with rc 0.
func TestUnknownChatIsRc4WithAMachineShape(t *testing.T) {
	jail := newRunJail(t)
	defer jail.killSockets(t)

	for _, testCase := range []struct {
		name string
		args []string
	}{
		{"status", []string{"chat", "status", "ghost"}},
		{"last", []string{"chat", "last", "ghost"}},
		{"read", []string{"chat", "read", "ghost"}},
		{"stream", []string{"chat", "stream", "ghost"}},
		{"call", []string{"chat", "call", "ghost", "hello"}},
		{"watch", []string{"chat", "watch", "ghost"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(testCase.args, &stdout, &stderr)
			if code != codeUnknownChat {
				t.Fatalf("exit = %d, want %d (stderr=%q)", code, codeUnknownChat, stderr.String())
			}
			if !strings.Contains(stderr.String(), "no chat named") {
				t.Fatalf("stderr = %q", stderr.String())
			}
		})
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"chat", "status", "ghost", "--json"}, &stdout, &stderr); code != codeUnknownChat {
		t.Fatalf("json exit = %d", code)
	}
	var status map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		t.Fatalf("status --json is not JSON: %q", stdout.String())
	}
	if status["state"] != "not-found" {
		t.Fatalf("status = %v", status)
	}
}

// TestScanFailureIsRc2NeverUnknownChat is the other half of that rule: when
// the fleet scan cannot look (its index database cannot open), a read verb
// exits 2 with the failure — never rc 4 and a "not-found" row that would tell
// the caller the chat does not exist.
func TestScanFailureIsRc2NeverUnknownChat(t *testing.T) {
	jail := newRunJail(t)
	defer jail.killSockets(t)
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvCacheDB, filepath.Join(blocker, "index.db"))
	for _, args := range [][]string{{"chat", "status", "ghost", "--json"}, {"chat", "last", "ghost"}} {
		var stdout, stderr bytes.Buffer
		code := run(args, &stdout, &stderr)
		if code != 2 || strings.Contains(stderr.String(), "no chat named") ||
			strings.Contains(stdout.String(), "not-found") {
			t.Fatalf(
				"%v with a broken index = %d stdout=%q stderr=%q; want rc 2 naming the failure",
				args,
				code,
				stdout.String(),
				stderr.String(),
			)
		}
	}
}
