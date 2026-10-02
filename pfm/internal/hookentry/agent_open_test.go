package hookentry

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestAgentOpenCacheFlagDefaultsToConfigAndAllowsOverride(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "claude")
	argvPath := filepath.Join(root, "argv")
	if err := testjail.WriteExecutable(
		bin,
		[]byte(
			"#!/bin/sh\nif [ \"$1\" = agents ]; then printf '[]\\n'; exit 0; fi\nprintf '%s\\n' \"$@\" > \"$AGENT_OPEN_ARGV\"\n"+
				"printf '%s\\n' \"${CACHE_LIVE_CONTROL_MAIN_TTL-unset}\" > \"$AGENT_OPEN_ARGV.ttl\"\n",
		),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_OPEN_ARGV", argvPath)
	t.Setenv(paths.EnvHome, root)
	t.Setenv(paths.EnvStateDB, filepath.Join(root, "state", "pfm.db"))
	t.Setenv(paths.EnvSIDDir, filepath.Join(root, "sid"))
	t.Setenv(paths.EnvProcRoot, filepath.Join(root, "proc"))
	if err := os.MkdirAll(filepath.Join(root, "proc"), 0o700); err != nil {
		t.Fatal(err)
	}
	values, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	machine := config.Config{
		Claude:   config.ClaudePrefs{Binary: bin, Cache1H: true},
		Accounts: []config.Account{{ID: 1, ConfigDir: config.DefaultAccountDir(root, 1)}},
	}
	if err := os.MkdirAll(machine.Accounts[0].ConfigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	runtime := config.Runtime{Config: machine, Paths: values}
	for _, scenario := range []struct {
		name, id string
		flags    []string
		cache1H  bool
	}{
		{name: "config default", id: "aaaa1111-1111-4111-8111-111111111111", cache1H: true},
		{name: "five-minute override", id: "bbbb2222-2222-4222-8222-222222222222", flags: []string{"--cache", "5m"}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			arguments := append([]string{"--id", scenario.id, "--cwd", root}, scenario.flags...)
			var stderr bytes.Buffer
			if code := AgentOpen(arguments, &stderr, runtime); code != 0 {
				t.Fatalf("agent-open code=%d stderr=%q", code, stderr.String())
			}
			content, err := os.ReadFile(argvPath)
			if err != nil {
				t.Fatal(err)
			}
			argv := strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
			parsed, err := claudelaunch.Parse(append([]string{"claude"}, argv...))
			if err != nil {
				t.Fatal(err)
			}
			if parsed.Resume != scenario.id {
				t.Fatalf("resume=%q, want %q", parsed.Resume, scenario.id)
			}
			wantTTL := "5m"
			if scenario.cache1H {
				wantTTL = "1h"
			}
			ttl, err := os.ReadFile(argvPath + ".ttl")
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimSpace(string(ttl)); got != wantTTL {
				t.Fatalf("process CACHE_LIVE_CONTROL_MAIN_TTL=%q, want %s", got, wantTTL)
			}
			launches, err := fleetdb.OpenLaunches(context.Background(), values)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := launches.Close(); err != nil {
					t.Error(err)
				}
			}()
			record, err := launches.LaunchFor(context.Background(), scenario.id)
			if err != nil {
				t.Fatal(err)
			}
			if record.Account != 1 || record.Cache1H != scenario.cache1H {
				t.Fatalf("agent-open record=%+v", record)
			}
		})
	}
}
