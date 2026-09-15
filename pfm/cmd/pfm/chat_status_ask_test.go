package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestChatStatusEngineModelGuardCoversAskAndSummary pins
// headless_command.go's runHeadlessStatus guard: --engine/--model require
// --summary OR --ask (either verb satisfies it), and are still rejected
// with exit 2 naming both verbs when NEITHER is given.
func TestChatStatusEngineModelGuardCoversAskAndSummary(t *testing.T) {
	jail := newRunJail(t)
	t.Chdir(jail.root)
	const id = "b1111111-1111-4111-8111-111111111111"
	if err := os.MkdirAll(filepath.Dir(jail.transcript), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jail.transcript, []byte(
		`{"type":"user","message":{"role":"user","content":"status?"}}`+"\n"+
			`{"type":"assistant","message":{"role":"assistant","content":"steady"}}`+"\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jail.binDir, "claude"), []byte(
		"#!/bin/sh\nprintf 'engine ran\\n'\n",
	), 0o700); err != nil {
		t.Fatal(err)
	}

	const guardMessage = "pfm chat status: --engine and --model require --summary or --ask\n"
	tests := []struct {
		name        string
		args        []string
		wantGuarded bool
	}{
		{
			"ask alone with engine and model",
			[]string{"chat", "status", "--engine", "claude", "--model", "m", "--ask", id},
			false,
		},
		{
			"summary alone with engine and model",
			[]string{"chat", "status", "--engine", "claude", "--model", "m", "--summary", id},
			false,
		},
		{"neither verb with engine", []string{"chat", "status", "--engine", "claude", id}, true},
		{"neither verb with model", []string{"chat", "status", "--model", "m", id}, true},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(testCase.args, &stdout, &stderr)
			if testCase.wantGuarded {
				if code != 2 || stderr.String() != guardMessage {
					t.Fatalf("code=%d stderr=%q, want the guard rejection %q", code, stderr.String(), guardMessage)
				}
				return
			}
			if code == 2 && stderr.String() == guardMessage {
				t.Fatalf("code=%d stderr=%q, --engine/--model wrongly guarded", code, stderr.String())
			}
			if code != codeDeadChat {
				t.Fatalf(
					"code=%d stdout=%q stderr=%q, want the dead-chat verdict past the guard",
					code,
					stdout.String(),
					stderr.String(),
				)
			}
		})
	}
}
