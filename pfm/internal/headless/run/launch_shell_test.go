package run

import (
	"path/filepath"
	"strings"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestClaudeEnvironmentAddsLaunchShell(t *testing.T) {
	dir := t.TempDir()
	bash := filepath.Join(dir, "bash")
	if err := testjail.WriteExecutable(bash, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake bash: %v", err)
	}
	t.Setenv("PATH", dir)
	for _, test := range []struct {
		name, inherited, want string
	}{
		{"unset", "", bash},
		{"refused", "CLAUDE_CODE_SHELL=relative-bash", bash},
		{"kept", "CLAUDE_CODE_SHELL=" + bash, bash},
	} {
		t.Run(test.name, func(t *testing.T) {
			environment := []string{"PATH=" + dir}
			if test.inherited != "" {
				environment = append(environment, test.inherited)
			}
			var got []string
			setEnvironment(environment, pfmengine.Claude, "", true, &got)
			count := 0
			for _, entry := range got {
				if strings.HasPrefix(entry, "CLAUDE_CODE_SHELL=") {
					count++
				}
			}
			if value := lastEnvironmentValue(got, "CLAUDE_CODE_SHELL"); value != test.want || count != 1 {
				t.Fatalf("CLAUDE_CODE_SHELL = %q (%d entries) in %q, want one %q", value, count, got, test.want)
			}
		})
	}
	var codex []string
	setEnvironment([]string{"PATH=" + dir}, pfmengine.Codex, "", true, &codex)
	if value := lastEnvironmentValue(codex, "CLAUDE_CODE_SHELL"); value != "" {
		t.Fatalf("Codex environment gained CLAUDE_CODE_SHELL=%q", value)
	}
}
