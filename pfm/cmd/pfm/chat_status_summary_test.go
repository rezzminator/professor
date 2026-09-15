package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChatStatusSummaryIsOptInCachedAndStructured(t *testing.T) {
	jail := newRunJail(t)
	t.Chdir(jail.root)
	const id = "b1111111-1111-4111-8111-111111111111"
	if err := os.MkdirAll(filepath.Dir(jail.transcript), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jail.transcript, []byte(
		`{"type":"user","message":{"role":"user","content":"what changed?"}}`+"\n"+
			`{"type":"assistant","message":{"role":"assistant","content":"the cache changed"}}`+"\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	counter := filepath.Join(jail.root, "ask-calls")
	if err := os.WriteFile(filepath.Join(jail.binDir, "claude"), []byte(
		"#!/bin/sh\nprintf x >> \"$ASK_COUNTER\"\nprintf 'cache now keys the last exchange\\n'\n",
	), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ASK_COUNTER", counter)

	var stdout, stderr bytes.Buffer
	baseCode := run([]string{"chat", "status", id}, &stdout, &stderr)
	if baseCode != codeDeadChat {
		t.Fatalf("base code=%d stdout=%q stderr=%q", baseCode, stdout.String(), stderr.String())
	}
	baseline := stdout.String()
	baselineStable := statusLineWithoutIdle(baseline)
	if strings.Contains(baseline, "summary") {
		t.Fatalf("no-flag output grew a summary: %q", baseline)
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"chat", "status", "--summary", id}, &stdout, &stderr); code != codeDeadChat {
		t.Fatalf("summary code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if statusLineWithoutIdle(stdout.String()) != baselineStable ||
		!strings.Contains(stdout.String(), "summary: cache now keys the last exchange\n") {
		t.Fatalf("summary stdout=%q baseline=%q", stdout.String(), baseline)
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"chat", "status", id, "--summary"}, &stdout, &stderr); code != codeDeadChat {
		t.Fatalf("cached code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "summary(cached): cache now keys the last exchange\n") {
		t.Fatalf("cached stdout=%q", stdout.String())
	}
	if calls, err := os.ReadFile(counter); err != nil || string(calls) != "x" {
		t.Fatalf("ask calls=%q err=%v", calls, err)
	}

	stdout.Reset()
	stderr.Reset()
	if code := run(
		[]string{"chat", "status", id},
		&stdout,
		&stderr,
	); code != codeDeadChat ||
		statusLineWithoutIdle(stdout.String()) != baselineStable {
		t.Fatalf("post-cache no-flag code=%d stdout=%q want=%q", code, stdout.String(), baseline)
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"chat", "status", id, "--summary", "--json"}, &stdout, &stderr); code != codeDeadChat {
		t.Fatalf("json code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var status map[string]any
	if err := json.Unmarshal(
		stdout.Bytes(),
		&status,
	); err != nil || status["summary"] != "cache now keys the last exchange" ||
		status["summary_cached"] != true {
		t.Fatalf("json status=%v err=%v", status, err)
	}
}

func statusLineWithoutIdle(output string) string {
	line, _, _ := strings.Cut(output, "\n")
	fields := strings.Split(line, "\t")
	if len(fields) > 2 && strings.HasPrefix(fields[2], "idle=") {
		fields[2] = "idle=<dynamic>"
	}
	return strings.Join(fields, "\t")
}
