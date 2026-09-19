package codexgen

import (
	"path/filepath"
	"testing"
)

// TestMCPFenceProblemsAcceptsAWellFormedFence pins the non-broken case: a
// single complete BEGIN...END pair (current or legacy spelling) raises
// nothing.
func TestMCPFenceProblemsAcceptsAWellFormedFence(t *testing.T) {
	for _, begin := range []string{mcpBegin, legacyMCPBegin} {
		current := "model = \"x\"\n\n" + begin + "\n[mcp_servers.a]\ncommand = \"a\"\n" + mcpEnd + "\n"
		if got := mcpFenceProblems(current, "/repo/.codex/config.toml"); len(got) != 0 {
			t.Fatalf("mcpFenceProblems(%q spelling) = %#v, want none", begin, got)
		}
	}
}

// TestMCPFenceProblemsNoFenceIsClean pins the common case: no marker at all.
func TestMCPFenceProblemsNoFenceIsClean(t *testing.T) {
	if got := mcpFenceProblems("model = \"x\"\n", "/repo/.codex/config.toml"); len(got) != 0 {
		t.Fatalf("mcpFenceProblems() = %#v, want none for a file with no fence", got)
	}
}

// TestMCPFenceProblemsFlagsAnUnpairedBegin pins L3-F11: a BEGIN with no
// matching END — a truncated write, a merge conflict — must be a named
// Problem, not silently kept as "hand" content with a second fence appended
// beside it.
func TestMCPFenceProblemsFlagsAnUnpairedBegin(t *testing.T) {
	path := "/repo/.codex/config.toml"
	current := "model = \"x\"\n\n" + mcpBegin + "\n[mcp_servers.a]\ncommand = \"a\"\n"
	got := mcpFenceProblems(current, path)
	if len(got) != 1 || !containsFinding(got, "no matching END") || !containsFinding(got, path) {
		t.Fatalf("mcpFenceProblems() = %#v, want exactly one 'no matching END' problem naming %s", got, path)
	}
}

// TestMCPFenceProblemsFlagsALoneEnd pins the other unpaired shape: an END
// with no preceding BEGIN.
func TestMCPFenceProblemsFlagsALoneEnd(t *testing.T) {
	path := "/repo/.codex/config.toml"
	current := "model = \"x\"\n\n[mcp_servers.a]\ncommand = \"a\"\n" + mcpEnd + "\n"
	got := mcpFenceProblems(current, path)
	if len(got) != 1 || !containsFinding(got, "no matching BEGIN") || !containsFinding(got, path) {
		t.Fatalf("mcpFenceProblems() = %#v, want exactly one 'no matching BEGIN' problem naming %s", got, path)
	}
}

// TestMCPFenceProblemsFlagsDuplicatedFences pins the third shape: two
// complete fences, each individually well-formed.
func TestMCPFenceProblemsFlagsDuplicatedFences(t *testing.T) {
	path := "/repo/.codex/config.toml"
	current := "model = \"x\"\n\n" + mcpBegin + "\n[mcp_servers.a]\n" + mcpEnd + "\n\n" +
		mcpBegin + "\n[mcp_servers.b]\n" + mcpEnd + "\n"
	got := mcpFenceProblems(current, path)
	if len(got) != 1 || !containsFinding(got, "2 mcp_servers fences") {
		t.Fatalf("mcpFenceProblems() = %#v, want exactly one 'fences found' problem", got)
	}
}

// TestCompileMCPMalformedFenceIsAProblemInBothModesAndBuildNeverAppendsASecondFence
// is the end-to-end pin: run through the same Run() entry both check and
// build use, over a repo whose .codex/config.toml carries an unpaired BEGIN.
// Both modes must report the CONFLICT; build must leave the file exactly as
// it found it — never appending a second fence beside the broken one.
func TestCompileMCPMalformedFenceIsAProblemInBothModesAndBuildNeverAppendsASecondFence(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	writeTestFile(t, filepath.Join(root, "CLAUDE.md"), "# Fixture\n")
	configPath := filepath.Join(root, ".codex", "config.toml")
	broken := "model = \"x\"\n\n" + mcpBegin + "\n[mcp_servers.stale]\ncommand = \"stale\"\n"
	writeTestFile(t, configPath, broken)
	writeTestFile(
		t,
		filepath.Join(root, ".mcp.json"),
		`{"mcpServers":{"fresh":{"command":"pfm","args":["mcp"]}}}`,
	)

	check, err := Run(Options{Root: root, Home: home, Mode: ModeCheck})
	if err != nil {
		t.Fatal(err)
	}
	if check.OK || !containsFinding(check.Problems, "no matching END") {
		t.Fatalf("check result = %#v, want a 'no matching END' Problem", check)
	}

	build, err := Run(Options{Root: root, Home: home, Mode: ModeBuild})
	if err != nil {
		t.Fatal(err)
	}
	if build.OK || !containsFinding(build.Problems, "no matching END") {
		t.Fatalf("build result = %#v, want a 'no matching END' Problem", build)
	}
	got := string(mustReadTestFile(t, configPath))
	if got != broken {
		t.Fatalf("build rewrote the malformed config.toml:\nwant=%q\ngot=%q", broken, got)
	}
	if containsFinding([]string{got}, mcpEnd) {
		t.Fatalf("build appended a closing fence over the malformed one: %q", got)
	}
}
