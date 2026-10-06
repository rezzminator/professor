package update

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/professor"
)

func writeRetiredFixture(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for relative, content := range files {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// TestScanRetiredNamesReportsEachHitByPathAndLine: a retired name in an
// unpinned script and an unpinned JSON file is reported as path:line with its
// successor; a pinned file, pfm's baseline, the engine mirrors, .git,
// .worktrees, node_modules and a binary file are never scanned, while the
// hand-written .codex/config.toml is.
func TestScanRetiredNamesReportsEachHitByPathAndLine(t *testing.T) {
	root := t.TempDir()
	writeRetiredFixture(t, root, map[string]string{
		"scripts/legacy.sh":            "#!/bin/sh\nclaude -p '/wave:refine the spec'\n# gitter Phase: JC-COMMIT\n",
		".claude/codex-build.json":     "{\n  \"skills\": [],\n  \"tools\": [\"mcp__chat__chat_inject\"]\n}\n",
		".codex/config.toml":           "[mcp]\nallow = \"mcp__harvester__readPage\"\n",
		"CLAUDE.md":                    "run /jc first\n",
		".professor/baseline.json":     "{\"files\": {\".claude/commands/pfm.md\": {}}}\n",
		"AGENTS.md":                    "/wave\n",
		"sub/AGENTS.md":                "/wave\n",
		".codex/agents/gitter.toml":    "JC-COMMIT\n",
		".opencode/agent/gitter.md":    "JC-COMMIT\n",
		".git/HEAD":                    "/wave\n",
		".worktrees/flight/scripts.sh": "/wave\n",
		"node_modules/pkg/index.js":    "\"/wave\"\n",
		"bin/tool":                     "\x00\x01/wave\n",
	})

	scan := ScanRetiredNames(root, map[string]bool{"CLAUDE.md": true})

	want := []professor.RetiredNameHit{
		{
			Path:      ".claude/codex-build.json",
			Line:      3,
			Name:      "mcp__chat__",
			Kind:      "tool",
			Successor: "mcp__professor__chat_*",
		},
		{
			Path: ".codex/config.toml", Line: 2, Name: "mcp__harvester__", Kind: "tool",
			Successor: "mcp__professor__harvester_*",
		},
		{Path: "scripts/legacy.sh", Line: 2, Name: "/wave", Kind: "command", Successor: "/flights:spec"},
		{Path: "scripts/legacy.sh", Line: 3, Name: "JC-COMMIT", Kind: "phase", Successor: "COMMIT"},
	}
	if !reflect.DeepEqual(scan.Hits, want) {
		t.Fatalf("hits =\n%+v\nwant\n%+v", scan.Hits, want)
	}
	if len(scan.Failures) != 0 {
		t.Fatalf("failures = %+v, want none", scan.Failures)
	}
}

// TestRetiredNamesNeverHitALiveNameSharingAPrefix pins the boundaries: a live
// command, agent, phase or tool that shares a retired name's prefix, and an
// ordinary path or URL containing one, is no hit; each retired form is.
func TestRetiredNamesNeverHitALiveNameSharingAPrefix(t *testing.T) {
	for _, line := range []string{
		"/pfm:release prepare --from ../live",
		"/pfm:workbench new",
		"run /pcm then /pcm:update",
		"the /pfm CLI guide",
		"see ~/.claude/commands/pfm.md and $HOME/.claude/commands/pfm.md",
		".claude/commands/pcm.md",
		"/quality:md-forlint check",
		"mcp__professor__chat_inject and mcp__professor__harvester_read",
		"spawn super-rr",
		"import x from 'src/wave.js'; /waveform",
		"/jcx and /pfm:refresher and /qa:lively",
		"JC-COMMITTED is not a phase; DOCS-COMMIT and COMMIT are live",
		"xmono-architect mono-architects",
		"the /chat route",
		"https://example.com/wave and http://localhost:3000/jc",
		"/wave-something",
	} {
		if hits := retiredNameHits("f", line); len(hits) != 0 {
			t.Errorf("line %q hit %+v, want none", line, hits)
		}
	}
	for line, name := range map[string]string{
		"/pfm:refresh is gone":             "/pfm:refresh",
		"run /wave":                        "/wave",
		"`/wave:refine`":                   "/wave",
		"(/jc:wave)":                       "/jc",
		"cat .claude/commands/pfm.md":      ".claude/commands/pfm.md",
		"\"mcp__chat__chat_ls\"":           "mcp__chat__",
		"/chat:save now":                   "/chat:",
		"agent: rr-super":                  "rr-super",
		".claude/agents/mono-architect.md": "mono-architect",
		"use /quality:forlint.":            "/quality:forlint",
		"Phase: JC-COMMIT.":                "JC-COMMIT",
		"/documenter:archive":              "/documenter",
		"skills/wave-builder/SKILL.md":     "wave-builder",
		"/qa:live":                         "/qa:live",
		"x .claude/commands/pfm/references/audit-scopes.md": ".claude/commands/pfm/references/audit-scopes.md",
	} {
		hits := retiredNameHits("f", line)
		if len(hits) != 1 || hits[0].Name != name {
			t.Errorf("line %q hits %+v, want exactly %q", line, hits, name)
		}
	}
}

// TestScanRetiredNamesReportsAnUnreadableFileAsAFailure: a file the scan
// cannot read, and a root that does not exist, are failures naming the path
// and the error — never an empty, clean scan.
func TestScanRetiredNamesReportsAnUnreadableFileAsAFailure(t *testing.T) {
	root := t.TempDir()
	writeRetiredFixture(t, root, map[string]string{
		"scripts/locked.sh": "/wave\n",
		"scripts/open.sh":   "/jc\n",
	})
	readFile := func(path string) ([]byte, error) {
		if strings.HasSuffix(path, "locked.sh") {
			return nil, errors.New("open " + path + ": permission denied")
		}
		return os.ReadFile(path)
	}

	scan := scanRetiredNamesWith(root, nil, readFile)

	if len(scan.Failures) != 1 || scan.Failures[0].Path != "scripts/locked.sh" ||
		!strings.Contains(scan.Failures[0].Error, "permission denied") {
		t.Fatalf("failures = %+v, want scripts/locked.sh permission denied", scan.Failures)
	}
	if len(scan.Hits) != 1 || scan.Hits[0].Path != "scripts/open.sh" {
		t.Fatalf("hits = %+v, want the readable file's /jc", scan.Hits)
	}

	missing := ScanRetiredNames(filepath.Join(root, "absent"), nil)
	if len(missing.Failures) != 1 || len(missing.Hits) != 0 {
		t.Fatalf("missing root scan = %+v, want one failure", missing)
	}
}

// projectReportNoScan runs `pfm doctor --project-updates` without the
// retired-name scan: project_test.go's status fixtures pin the template
// statuses alone.
func projectReportNoScan(root, home string, jsonOutput bool, stdout *bytes.Buffer) int {
	return professor.RunProjectUpdates(root, home, jsonOutput, stdout, nil)
}
