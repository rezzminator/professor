package installer

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWireClaudeRegistryDefaultsSetsEveryValue(t *testing.T) {
	for _, row := range []struct {
		name, registry, want string
		apply, written       bool
	}{
		{
			name:     "an absent key is added",
			registry: "{\n  \"numStartups\": 12,\n  \"tipsHistory\": {\"a<b\": 1}\n}\n",
			want:     "{\n  \"numStartups\": 12,\n  \"tipsHistory\": {\"a<b\": 1},\n  \"leftArrowOpensAgents\": false\n}\n",
			apply:    true, written: true,
		},
		{
			name:     "an enabled key is replaced",
			registry: "{\n  \"leftArrowOpensAgents\": true,\n  \"numStartups\": 12\n}\n",
			want:     "{\n  \"numStartups\": 12,\n  \"leftArrowOpensAgents\": false\n}\n",
			apply:    true, written: true,
		},
		{
			name:     "a registry already holding the value is not written",
			registry: "{\n  \"leftArrowOpensAgents\":  false\n}\n",
			want:     "{\n  \"leftArrowOpensAgents\":  false\n}\n",
			apply:    true, written: false,
		},
		{
			name:     "a dry run only reports",
			registry: "{\n  \"numStartups\": 12\n}\n",
			want:     "{\n  \"numStartups\": 12\n}\n",
			apply:    false, written: true,
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			account := fullscreenAccount(t, "", row.registry)
			installer, output := fullscreenEngine(t, row.apply, account)
			if err := installer.wireClaudeRegistryDefaults(); err != nil {
				t.Fatalf("wireClaudeRegistryDefaults: %v\noutput:\n%s", err, output)
			}
			if got := readRegistry(t, account); got != row.want {
				t.Errorf("registry:\n%s\nwant:\n%s", got, row.want)
			}
			change := "set Claude registry defaults [leftArrowOpensAgents=false"
			if got := strings.Contains(output.String(), change); got != row.written {
				t.Errorf("output reports a change=%v, want %v:\n%s", got, row.written, output)
			}
			if !row.written && !strings.Contains(output.String(), "claude registry defaults set in ") {
				t.Errorf("output lacks the already-set line:\n%s", output)
			}
		})
	}
}

func TestWireClaudeRegistryDefaultsLeavesAnAbsentRegistryForLater(t *testing.T) {
	account := fullscreenAccount(t, "", "")
	installer, output := fullscreenEngine(t, true, account)
	if err := installer.wireClaudeRegistryDefaults(); err != nil {
		t.Fatalf("wireClaudeRegistryDefaults: %v\noutput:\n%s", err, output)
	}
	path := filepath.Join(account.ConfigDir, ".claude.json")
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%s created or unreadable: %v", path, err)
	}
	if want := "claude registry defaults: no registry yet in " + path; !strings.Contains(output.String(), want) {
		t.Errorf("output lacks %q:\n%s", want, output)
	}
}

func TestWireClaudeRegistryDefaultsReportsAnUnparsableRegistry(t *testing.T) {
	account := fullscreenAccount(t, "", "{not json")
	installer, output := fullscreenEngine(t, true, account)
	err := installer.wireClaudeRegistryDefaults()
	if err == nil || !strings.Contains(err.Error(), "registry unparsable, state unknown") {
		t.Fatalf("err=%v, want the unparsable-registry failure\noutput:\n%s", err, output)
	}
	if got := readRegistry(t, account); got != "{not json" {
		t.Errorf("registry rewritten to %q", got)
	}
}
