package stats

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/usagehook"
)

func TestDefaultAckUsesLeanSettingsAndInheritedEnvironment(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "claude")
	if err := testjail.WriteExecutableNeverRun(binary, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &deps.FakeRunner{}
	runner.ScriptStartOutput([]string{binary}, deps.StartOutput{})
	previous := ackRunner
	ackRunner = runner
	t.Cleanup(func() { ackRunner = previous })
	account := LimitAccount{ID: 2, ClaudeBinary: binary, ConfigDir: filepath.Join(root, "config")}
	if err := os.MkdirAll(account.ConfigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := defaultAck(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	starts := runner.Starts()
	if len(starts) != 1 {
		t.Fatalf("ACK starts = %v, want exactly 1", starts)
	}
	argv := strings.Join(starts[0].Argv[1:], "\n") + "\n"
	if argv != `-p
--model
claude-haiku-4-5
--max-turns
1
--settings
{"env":{"ANTHROPIC_SMALL_FAST_MODEL":"claude-haiku-5-5","CLAUDE_CODE_AUTO_COMPACT_WINDOW":"100000","CLAUDE_CODE_ENABLE_FUNCTION_HOOKS":"1","CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT":"1"},"outputStyle":"default"}
` {
		t.Fatalf("ACK argv = %q", argv)
	}
	for _, entry := range starts[0].Opts.Env {
		if strings.HasPrefix(entry, "CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT=") {
			t.Fatalf("ACK ambient simple-prompt env = %q", entry)
		}
	}
}

func TestLimitsSamplerACKFallbackIsAtMostOncePerAccount(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	var fetches, acks int
	sampler := NewLimitsSampler([]LimitAccount{{ID: 7, Engine: pfmengine.Claude, ConfigDir: "config"}})
	sampler.Now = func() time.Time { return now }
	sampler.Fetch = func(context.Context, LimitAccount) (usagehook.Usage, error) {
		fetches++
		return usagehook.Usage{}, fmt.Errorf("401 unauthorized")
	}
	sampler.Ack = func(context.Context, LimitAccount) error {
		acks++
		return fmt.Errorf("ACK refresh failed")
	}
	_, warnings := sampler.Sample(context.Background())
	if acks != 1 || fetches != 1 || len(warnings) != 0 {
		t.Fatalf("first sample fetches=%d acks=%d warnings=%v", fetches, acks, warnings)
	}
	now = now.Add(defaultLimitsTTL + time.Minute)
	_, warnings = sampler.Sample(context.Background())
	if acks != 1 || fetches != 2 || len(warnings) != 0 {
		t.Fatalf("expired sample fetches=%d acks=%d warnings=%v", fetches, acks, warnings)
	}
}

func TestLocalCredentialFileErrorsDoNotTriggerLiveAckRefresh(t *testing.T) {
	for _, message := range []string{
		"stat usage credentials: permission denied",
		"read usage credentials: input/output error",
		"decode usage credentials: invalid character",
	} {
		if needsCredentialRefresh(errors.New(message)) {
			t.Fatalf("local I/O error routed to live credential refresh: %q", message)
		}
	}
}
