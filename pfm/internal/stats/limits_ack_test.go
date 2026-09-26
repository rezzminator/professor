package stats

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/usagehook"
)

func TestDefaultAckUsesLeanSettingsAndInheritedEnvironment(t *testing.T) {
	root := t.TempDir()
	argvPath := filepath.Join(root, "argv")
	envPath := filepath.Join(root, "environment")
	t.Setenv("PFM_ACK_ARGV", argvPath)
	t.Setenv("PFM_ACK_ENV", envPath)
	binary := filepath.Join(root, "claude")
	body := "#!/bin/sh\nset -eu\nprintf '%s\\n' \"$@\" > \"$PFM_ACK_ARGV\"\nprintenv CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT > \"$PFM_ACK_ENV\" || true\n"
	if err := os.WriteFile(binary, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	account := LimitAccount{ID: 2, ClaudeBinary: binary, ConfigDir: filepath.Join(root, "config")}
	if err := defaultAck(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	argv, err := os.ReadFile(argvPath)
	if err != nil {
		t.Fatal(err)
	}
	want := "-p\n--model\nclaude-haiku-4-5\n--max-turns\n1\n--settings\n"
	if !strings.HasPrefix(string(argv), want) ||
		!strings.Contains(string(argv), `"CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT":"1"`) {
		t.Fatalf("ACK argv = %q", argv)
	}
	env, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(env) != 0 {
		t.Fatalf("ACK ambient simple-prompt env = %q", env)
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
