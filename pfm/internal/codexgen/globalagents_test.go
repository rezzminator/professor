package codexgen

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// TestGlobalAgentsCompilesInstallsAndAppliesSpawnAgentSubstitution covers the
// two-file happy path: both .md sources compile to a sibling .toml, both get
// installed into {home}/.claude/agents (raw source) and {home}/.codex/agents
// (compiled TOML), and the one Codex-specific body substitution — "Codex has
// no Agent tool" — fires exactly where the host script's docstring says it
// does.
func TestGlobalAgentsCompilesInstallsAndAppliesSpawnAgentSubstitution(t *testing.T) {
	home := t.TempDir()
	writeTestFile(t, filepath.Join(home, ".professor", "templates", "global", "agents", "alpha.md"),
		"---\nname: alpha\ndescription: Alpha role for testing.\ntools: Read\nmodel: sonnet\n---\n\n"+
			"Delegate to children are Explore+haiku (never\nyour own type) for search fan-out.\n")
	writeTestFile(
		t,
		filepath.Join(home, ".professor", "templates", "global", "agents", "beta.md"),
		"---\nname: beta\ndescription: Beta role for testing.\ntools: Read\nmodel: haiku\n---\n\nBeta body, unrelated.\n",
	)

	result, err := RunGlobalAgents(GlobalAgentsOptions{Home: home})
	if err != nil {
		t.Fatalf("RunGlobalAgents: %v", err)
	}
	if len(result.Compiled) != 2 {
		t.Fatalf("compiled = %#v, want 2 entries", result.Compiled)
	}
	if len(result.Installed) != 2 {
		t.Fatalf("installed = %#v, want 2 Claude link entries", result.Installed)
	}
	for _, installed := range result.Installed {
		if installed.State != GlobalLinkMissing {
			t.Fatalf("installed %s classified %s before install ran, want missing", installed.Path, installed.State)
		}
	}
	if len(result.Roles) != 2 {
		t.Fatalf("roles = %#v, want 2 Codex role files", result.Roles)
	}
	for _, role := range result.Roles {
		if role.State != GlobalRoleMissing {
			t.Fatalf("role %s classified %s before install ran, want missing", role.Path, role.State)
		}
	}
	if len(result.Problems) != 0 {
		t.Fatalf("problems = %#v, want none for a fresh install", result.Problems)
	}

	alphaTOML := string(
		mustReadTestFile(t, filepath.Join(filepath.Join(home, ".codex", "agents"), "alpha.toml")),
	)
	if strings.Contains(alphaTOML, "children are Explore+haiku") {
		t.Fatalf("alpha.toml: substitution did not fire:\n%s", alphaTOML)
	}
	if !strings.Contains(alphaTOML, "spawned via spawn_agent as the `explorer` role") {
		t.Fatalf("alpha.toml: substitution target text missing:\n%s", alphaTOML)
	}

	for _, expect := range []struct{ target, source string }{
		{filepath.Join(home, ".claude", "agents", "alpha.md"), filepath.Join(home, ".professor", "templates", "global", "agents", "alpha.md")},
		{filepath.Join(home, ".claude", "agents", "beta.md"), filepath.Join(home, ".professor", "templates", "global", "agents", "beta.md")},
	} {
		assertGlobalSymlink(t, expect.target, expect.source)
	}
	for _, name := range []string{"alpha.toml", "beta.toml"} {
		assertGlobalRoleFile(t, filepath.Join(home, ".codex", "agents", name))
	}

	// No .toml is ever written beside the .md source inside the clone —
	// the whole point of this move.
	for _, name := range []string{"alpha.toml", "beta.toml"} {
		inClone := filepath.Join(home, ".professor", "templates", "global", "agents", name)
		if _, err := os.Lstat(inClone); !os.IsNotExist(err) {
			t.Fatalf("expected no .toml written inside the clone at %s, lstat err=%v", inClone, err)
		}
	}

	// The .claude install is the raw source, untouched by the Codex-only
	// substitution — Claude does have an Agent tool.
	claudeAlpha := string(mustReadTestFile(t, filepath.Join(home, ".claude", "agents", "alpha.md")))
	if !strings.Contains(claudeAlpha, "children are Explore+haiku (never\nyour own type)") {
		t.Fatalf(".claude/agents/alpha.md: raw source was mutated:\n%s", claudeAlpha)
	}
}

// assertGlobalSymlink fails the test unless target is a symlink resolving
// exactly to source — the shape every global registry install now promises
// in place of the old copy.
func assertGlobalSymlink(t *testing.T, target, source string) {
	t.Helper()
	info, err := os.Lstat(target)
	if err != nil {
		t.Fatalf("expected install at %s: %v", target, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s is not a symlink (mode=%v)", target, info.Mode())
	}
	resolved, err := os.Readlink(target)
	if err != nil {
		t.Fatalf("readlink %s: %v", target, err)
	}
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(filepath.Dir(target), resolved)
	}
	if filepath.Clean(resolved) != filepath.Clean(source) {
		t.Fatalf("%s -> %s, want -> %s", target, resolved, source)
	}
}

// TestGlobalAgentsAdversarialFixtureEmitsValidTOMLWithLiteralQuotesAndDelimiterCollision
// is the exact failure mode the host script's docstring warns about: a
// description containing a raw `"` (real examples: the trigger phrases
// "walker fast", "map it now") and a body containing a literal `"""` that
// collides with the multi-line basic-string delimiter. An emitter that drops
// either escape rule ships a TOML Codex cannot parse at startup — this test
// asserts both the exact escaped bytes AND that the result independently
// parses as TOML (via BurntSushi/toml, not our own escaping logic).
func TestGlobalAgentsAdversarialFixtureEmitsValidTOMLWithLiteralQuotesAndDelimiterCollision(t *testing.T) {
	home := t.TempDir()
	writeTestFile(
		t,
		filepath.Join(home, ".professor", "templates", "global", "agents", "quirky.md"),
		"---\nname: quirky\ndescription: Uses \"walker fast\" and \"map it now\" verbatim.\ntools: Read\nmodel: opus # pinned\neffort: high\n---\n\n"+
			"Body has a literal triple quote \"\"\" and a backslash \\ standalone.\n",
	)

	result, err := RunGlobalAgents(GlobalAgentsOptions{Home: home})
	if err != nil {
		t.Fatalf("RunGlobalAgents: %v", err)
	}
	if len(result.Compiled) != 1 {
		t.Fatalf("compiled = %#v, want 1 entry", result.Compiled)
	}

	got := string(
		mustReadTestFile(t, filepath.Join(filepath.Join(home, ".codex", "agents"), "quirky.toml")),
	)
	head := globalRoleMarkerPrefix + "templates/global/agents/quirky.md" +
		"; do not edit — edit the source, then re-run: pfm codex build\n" +
		"name = \"quirky\"\n" +
		"description = \"Uses \\\"walker fast\\\" and \\\"map it now\\\" verbatim.\"\n" +
		"model = \"gpt-5.6-sol\"\n" +
		"model_reasoning_effort = \"high\"\n" +
		"developer_instructions = \"\"\"\n"
	// The role's own body, escaped byte for byte, is the whole value.
	body := "Body has a literal triple quote \\\"\\\"\\\" and a backslash \\\\ standalone.\n" +
		"\"\"\"\n"
	if got != head+body {
		t.Fatalf("quirky.toml =\n%q\nwant %q ... %q", got, head, body)
	}
	if err := validateTOML(got); err != nil {
		t.Fatalf(
			"emitted TOML does not parse (the exact startup failure this escaping exists to prevent): %v\n%s",
			err,
			got,
		)
	}
}

func TestGlobalAgentsUnquotesYAMLQuotedDescription(t *testing.T) {
	home := t.TempDir()
	writeTestFile(
		t,
		filepath.Join(home, ".professor", "templates", "global", "agents", "quoted.md"),
		"---\nname: quoted\ndescription: 'a: b, \"c\"'\n---\n\nBody.\n",
	)

	if _, err := RunGlobalAgents(GlobalAgentsOptions{Home: home}); err != nil {
		t.Fatalf("RunGlobalAgents: %v", err)
	}
	got := string(mustReadTestFile(
		t,
		filepath.Join(filepath.Join(home, ".codex", "agents"), "quoted.toml"),
	))
	head := globalRoleMarkerPrefix + "templates/global/agents/quoted.md" +
		"; do not edit — edit the source, then re-run: pfm codex build\n" +
		"name = \"quoted\"\n" +
		"description = \"a: b, \\\"c\\\"\"\n" +
		"developer_instructions = \"\"\"\n"
	if !strings.HasPrefix(got, head) || !strings.HasSuffix(got, "\nBody.\n\"\"\"\n") {
		t.Fatalf("quoted.toml =\n%q\nwant %q ... %q", got, head, "Body.")
	}
}

// TestGlobalAgentSourcesCompileDeterministicallyToValidTOML replaces the old
// tracked-twin comparison: wave 9b retired the tracked `.toml` twins from
// git (root CLAUDE.md — "nothing generated is written into the clone"), so
// there is no on-disk artifact left to diff against. What remains true and
// worth pinning for every real `templates/global/agents/*.md` source: the
// compiler succeeds, is deterministic (two renders byte-equal), the output
// independently parses as TOML (via the same BurntSushi/toml parser
// validateTOML already depends on — no new dependency), carries the
// source frontmatter's name/description, and no `.toml` is ever written
// beside the `.md` source (the clone stays clean).
func TestGlobalAgentSourcesCompileDeterministicallyToValidTOML(t *testing.T) {
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate codexgen test source")
	}
	agentsDir := filepath.Join(filepath.Dir(testFile), "..", "..", "..", "templates", "global", "agents")
	sources, err := filepath.Glob(filepath.Join(agentsDir, "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) == 0 {
		t.Fatalf("no global agent sources in %s", agentsDir)
	}
	for _, source := range sources {
		t.Run(strings.TrimSuffix(filepath.Base(source), ".md"), func(t *testing.T) {
			raw, err := os.ReadFile(source)
			if err != nil {
				t.Fatal(err)
			}
			fields, _, err := parseFrontmatter(string(raw))
			if err != nil {
				t.Fatalf("parse frontmatter of %s: %v", source, err)
			}
			wantName := strings.TrimSpace(fields["name"])
			// The description reaches the compiled role verbatim but for the
			// one rewrite a Codex lead needs: Claude's /code-review names a
			// command no Codex seat has (review.go).
			wantDescription := rewriteCodeReview(strings.TrimSpace(fields["description"]), nil)

			_, first, err := renderGlobalAgentTOML(source, string(raw), t.TempDir())
			if err != nil {
				t.Fatalf("renderGlobalAgentTOML: %v", err)
			}
			_, second, err := renderGlobalAgentTOML(source, string(raw), t.TempDir())
			if err != nil {
				t.Fatalf("renderGlobalAgentTOML (second render): %v", err)
			}
			if first != second {
				t.Fatalf("renderGlobalAgentTOML is not deterministic:\nfirst:\n%q\nsecond:\n%q", first, second)
			}

			var document struct {
				Name        string `toml:"name"`
				Description string `toml:"description"`
			}
			if _, err := toml.Decode(first, &document); err != nil {
				t.Fatalf("rendered TOML does not parse: %v\n%s", err, first)
			}
			if document.Name != wantName {
				t.Fatalf("rendered TOML name = %q, want %q (frontmatter)", document.Name, wantName)
			}
			if document.Description != wantDescription {
				t.Fatalf("rendered TOML description = %q, want %q (frontmatter)", document.Description, wantDescription)
			}

			besideSource := strings.TrimSuffix(source, ".md") + ".toml"
			if _, err := os.Lstat(besideSource); !os.IsNotExist(err) {
				t.Fatalf("expected no .toml written beside the source at %s, lstat err=%v", besideSource, err)
			}
		})
	}
}

// TestGlobalAgentEscapeMirrorsBuildCodexMJS pins the basic-string escape to
// build-codex.mjs:151 exactly: backslash doubled, then a raw quote escaped.
// Order matters — escaping the quote first would double-escape the
// backslashes it just inserted.
func TestGlobalAgentEscapeMirrorsBuildCodexMJS(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"raw quote pair", `Uses "walker fast" and "map it now".`, `Uses \"walker fast\" and \"map it now\".`},
		{"backslash before quote", `back\slash "then" quote`, `back\\slash \"then\" quote`},
		{"plain text unchanged", `no special characters here`, `no special characters here`},
	}
	for _, c := range cases {
		if got := globalAgentEscape(c.in); got != c.want {
			t.Errorf("%s: globalAgentEscape(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// TestGlobalAgentEscapeMultilineNeutralisesTripleQuoteCollision pins the
// multi-line basic-string escape to build-codex.mjs:153 exactly: backslash
// doubled, then a literal `"""` neutralised so it can never be mistaken for
// the string's own closing delimiter.
func TestGlobalAgentEscapeMultilineNeutralisesTripleQuoteCollision(t *testing.T) {
	in := "line one\nhas a literal \"\"\" inside\nand a backslash \\ alone"
	want := "line one\nhas a literal \\\"\\\"\\\" inside\nand a backslash \\\\ alone"
	if got := globalAgentEscapeMultiline(in); got != want {
		t.Fatalf("globalAgentEscapeMultiline(%q) = %q, want %q", in, got, want)
	}
}

func TestGlobalAgentsMissingFrontmatterFieldIsAHardError(t *testing.T) {
	home := t.TempDir()
	writeTestFile(t, filepath.Join(home, ".professor", "templates", "global", "agents", "broken.md"),
		"---\nname: broken\n---\n\nno description field.\n")

	_, err := RunGlobalAgents(GlobalAgentsOptions{Home: home})
	if err == nil || !strings.Contains(err.Error(), "needs both name: and description") {
		t.Fatalf("RunGlobalAgents: got %v, want a frontmatter error", err)
	}
}

func TestGlobalAgentsNoSourcesIsAHardError(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".professor", "templates", "global", "agents"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := RunGlobalAgents(GlobalAgentsOptions{Home: home})
	if err == nil || !strings.Contains(err.Error(), "no agent .md files in") {
		t.Fatalf("RunGlobalAgents: got %v, want a no-sources error", err)
	}
}

// TestGlobalAgentsCheckReportsMissingBeforeInstall is the RED-then-GREEN pin
// on check mode's classification: nothing has been installed yet, so every
// desired target must classify as missing — never fabricated as "installed"
// just because check mode looked.
func TestGlobalAgentsCheckReportsMissingBeforeInstall(t *testing.T) {
	home := t.TempDir()
	writeTestFile(t, filepath.Join(home, ".professor", "templates", "global", "agents", "alpha.md"),
		"---\nname: alpha\ndescription: Alpha role for testing.\n---\n\nbody\n")
	result, err := RunGlobalAgents(GlobalAgentsOptions{Home: home, Mode: ModeCheck})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Installed) != 1 || len(result.Roles) != 1 {
		t.Fatalf("installed=%#v roles=%#v, want one of each desired target", result.Installed, result.Roles)
	}
	if result.Installed[0].State != GlobalLinkMissing {
		t.Fatalf(
			"check mode classified an absent link as %s, not missing: %#v",
			result.Installed[0].State,
			result.Installed[0],
		)
	}
	if result.Roles[0].State != GlobalRoleMissing {
		t.Fatalf("check mode classified an absent role as %s, not missing: %#v", result.Roles[0].State, result.Roles[0])
	}
	for _, path := range []string{
		filepath.Join(home, ".claude", "agents", "alpha.md"),
		filepath.Join(home, ".codex", "agents", "alpha.toml"),
	} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("check mode wrote %s: %v", path, err)
		}
	}
}

// TestGlobalAgentsCodexRosterNilDefaultsEmptyPlansNoRole pins the roster
// contract the installer relies on: a nil CodexHomes (`pfm codex agents`)
// defaults to {Home}/.codex, while a non-nil empty CodexHomes (an install
// with no Codex account) means no Codex home — the Claude link is still
// planned and built, and no role is planned or written.
func TestGlobalAgentsCodexRosterNilDefaultsEmptyPlansNoRole(t *testing.T) {
	for _, tc := range []struct {
		name      string
		roster    []string
		wantRoles int
	}{
		{name: "nil roster", roster: nil, wantRoles: 1},
		{name: "empty roster", roster: []string{}, wantRoles: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			source := filepath.Join(home, ".professor", "templates", "global", "agents", "alpha.md")
			writeTestFile(t, source, "---\nname: alpha\ndescription: Alpha role for testing.\n---\n\nbody\n")
			options := GlobalAgentsOptions{Home: home, CodexHomes: tc.roster, Mode: ModeCheck}
			plan, err := RunGlobalAgents(options)
			if err != nil {
				t.Fatalf("RunGlobalAgents check: %v", err)
			}
			if len(plan.Roles) != tc.wantRoles {
				t.Fatalf("planned roles=%#v, want %d", plan.Roles, tc.wantRoles)
			}
			if len(plan.Installed) != 1 {
				t.Fatalf("planned Claude links=%#v, want one", plan.Installed)
			}
			options.Mode = ModeBuild
			if _, err := RunGlobalAgents(options); err != nil {
				t.Fatalf("RunGlobalAgents build: %v", err)
			}
			assertGlobalSymlink(t, filepath.Join(home, ".claude", "agents", "alpha.md"), source)
			role := filepath.Join(home, ".codex", "agents", "alpha.toml")
			if tc.wantRoles == 0 {
				if _, err := os.Lstat(filepath.Join(home, ".codex")); !os.IsNotExist(err) {
					t.Fatalf("empty roster wrote under ~/.codex: %v", err)
				}
				return
			}
			assertGlobalRoleFile(t, role)
		})
	}
}

// TestGlobalAgentsInstallLinksClaudeAndWritesTheCodexRole pins the two shapes
// apart: Claude's registry entry must be a symlink resolving to the
// source-repo original, and Codex's must be the regular role file its loader
// can actually open.
func TestGlobalAgentsInstallLinksClaudeAndWritesTheCodexRole(t *testing.T) {
	home := t.TempDir()
	writeTestFile(t, filepath.Join(home, ".professor", "templates", "global", "agents", "alpha.md"),
		"---\nname: alpha\ndescription: Alpha role for testing.\n---\n\nbody\n")

	if _, err := RunGlobalAgents(GlobalAgentsOptions{Home: home}); err != nil {
		t.Fatalf("RunGlobalAgents: %v", err)
	}

	assertGlobalSymlink(t,
		filepath.Join(home, ".claude", "agents", "alpha.md"),
		filepath.Join(home, ".professor", "templates", "global", "agents", "alpha.md"))
	assertGlobalRoleFile(t, filepath.Join(home, ".codex", "agents", "alpha.toml"))
}

// TestGlobalAgentsInstallReplacesALegacyCopyWithASymlink covers the exact
// migration case this rewrite exists for: a regular-file copy the old
// copy-based installer left behind at the desired path is ours (its basename
// IS the roster entry) and gets replaced with the link, not backed up as a
// stranger's file.
func TestGlobalAgentsInstallReplacesALegacyCopyWithASymlink(t *testing.T) {
	home := t.TempDir()
	writeTestFile(t, filepath.Join(home, ".professor", "templates", "global", "agents", "alpha.md"),
		"---\nname: alpha\ndescription: Alpha role for testing.\n---\n\nbody\n")
	writeTestFile(t, filepath.Join(home, ".claude", "agents", "alpha.md"), "stale copy from the old installer\n")

	result, err := RunGlobalAgents(GlobalAgentsOptions{Home: home})
	if err != nil {
		t.Fatalf("RunGlobalAgents: %v", err)
	}
	if len(result.Problems) != 0 {
		t.Fatalf("a legacy copy at our own desired path was reported as a conflict: %#v", result.Problems)
	}
	assertGlobalSymlink(t,
		filepath.Join(home, ".claude", "agents", "alpha.md"),
		filepath.Join(home, ".professor", "templates", "global", "agents", "alpha.md"))
}

// TestGlobalAgentsInstallRepointsAStaleInRepoSymlink covers a symlink that
// already points somewhere INSIDE the source repository, just not at the
// current desired source (a rename, a re-rostered agent) — still ours,
// repointed rather than reported as a conflict.
func TestGlobalAgentsInstallRepointsAStaleInRepoSymlink(t *testing.T) {
	home := t.TempDir()
	writeTestFile(t, filepath.Join(home, ".professor", "templates", "global", "agents", "alpha.md"),
		"---\nname: alpha\ndescription: Alpha role for testing.\n---\n\nbody\n")
	staleSource := filepath.Join(home, ".professor", "templates", "global", "retired-alpha.md")
	writeTestFile(t, staleSource, "a since-renamed agent source\n")
	claudeDest := filepath.Join(home, ".claude", "agents")
	if err := os.MkdirAll(claudeDest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(staleSource, filepath.Join(claudeDest, "alpha.md")); err != nil {
		t.Fatal(err)
	}

	result, err := RunGlobalAgents(GlobalAgentsOptions{Home: home})
	if err != nil {
		t.Fatalf("RunGlobalAgents: %v", err)
	}
	if len(result.Problems) != 0 {
		t.Fatalf("a stale in-repo symlink was reported as a conflict: %#v", result.Problems)
	}
	assertGlobalSymlink(t,
		filepath.Join(home, ".claude", "agents", "alpha.md"),
		filepath.Join(home, ".professor", "templates", "global", "agents", "alpha.md"))
}

// TestGlobalAgentsRepointsALinkStillTargetingTheOldInCloneTOML is the
// migration case this wave exists for: an existing ~/.codex/agents/alpha.toml
// symlink still points at the retired in-clone
// {SourceRepo}/templates/global/agents/alpha.toml. pfm wrote that link, so it
// is ours to migrate: the run replaces it with the real role file instead of
// reporting a conflict.
func TestGlobalAgentsMigratesALinkStillTargetingTheOldInCloneTOML(t *testing.T) {
	home := t.TempDir()
	writeTestFile(t, filepath.Join(home, ".professor", "templates", "global", "agents", "alpha.md"),
		"---\nname: alpha\ndescription: Alpha role for testing.\n---\n\nbody\n")
	oldInCloneTOML := filepath.Join(home, ".professor", "templates", "global", "agents", "alpha.toml")
	writeTestFile(t, oldInCloneTOML, "name = \"alpha\"\ndescription = \"stale tracked twin\"\n")
	codexDest := filepath.Join(home, ".codex", "agents")
	if err := os.MkdirAll(codexDest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(oldInCloneTOML, filepath.Join(codexDest, "alpha.toml")); err != nil {
		t.Fatal(err)
	}

	result, err := RunGlobalAgents(GlobalAgentsOptions{Home: home})
	if err != nil {
		t.Fatalf("RunGlobalAgents: %v", err)
	}
	if len(result.Problems) != 0 {
		t.Fatalf("a link still targeting the old in-clone twin was reported as a conflict: %#v", result.Problems)
	}
	assertGlobalRoleFile(t, filepath.Join(home, ".codex", "agents", "alpha.toml"))
}

// assertGlobalRoleFile fails the test unless target is a regular file carrying
// pfm's generated marker — the shape Codex loads and the proof pfm owns it.
func assertGlobalRoleFile(t *testing.T, target string) {
	t.Helper()
	info, err := os.Lstat(target)
	if err != nil {
		t.Fatalf("expected a role file at %s: %v", target, err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("%s mode=%v — Codex refuses anything but a regular file", target, info.Mode())
	}
	if !GeneratedGlobalRole(mustReadTestFile(t, target)) {
		t.Fatalf("%s carries no generated marker", target)
	}
}

// TestGlobalAgentsInstallLeavesAForeignSymlinkAlone is the conflict-law pin:
// a symlink pointing OUTSIDE the source repository entirely — an operator's
// own file, nothing this installer ever wrote — is never overwritten, never
// deleted, and is reported by exact CONFLICT wording rather than silently
// skipped.
func TestGlobalAgentsInstallLeavesAForeignSymlinkAlone(t *testing.T) {
	home := t.TempDir()
	writeTestFile(t, filepath.Join(home, ".professor", "templates", "global", "agents", "alpha.md"),
		"---\nname: alpha\ndescription: Alpha role for testing.\n---\n\nbody\n")

	claudeDest := filepath.Join(home, ".claude", "agents")
	if err := os.MkdirAll(claudeDest, 0o755); err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(home, "elsewhere.md")
	writeTestFile(t, elsewhere, "an operator's own file, unrelated to the source repo\n")
	foreignLink := filepath.Join(claudeDest, "alpha.md")
	if err := os.Symlink(elsewhere, foreignLink); err != nil {
		t.Fatal(err)
	}

	result, err := RunGlobalAgents(GlobalAgentsOptions{Home: home})
	if err != nil {
		t.Fatalf("RunGlobalAgents: %v", err)
	}
	want := "CONFLICT " + foreignLink + ": not ours (points to " + elsewhere + ")"
	found := false
	for _, problem := range result.Problems {
		if problem == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("problems = %#v, want to contain %q", result.Problems, want)
	}

	resolved, err := os.Readlink(foreignLink)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != elsewhere {
		t.Fatalf("foreign symlink was rewritten: now -> %s, want -> %s", resolved, elsewhere)
	}
}

// TestGlobalAgentsLinkIntoEveryConfiguredClaudeConfigDir pins the fanout the
// retire side already had: a host with two configured Claude accounts must
// receive one agent link per account registry, not one link into the primary
// while every other account silently has no global agents at all. Check mode
// plans a link for BOTH dirs, build creates both, and a second check reports
// nothing left to do.
func TestGlobalAgentsLinkIntoEveryConfiguredClaudeConfigDir(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(home, ".professor", "templates", "global", "agents", "alpha.md")
	writeTestFile(t, source, "---\nname: alpha\ndescription: Alpha role for testing.\n---\n\nbody\n")
	first := filepath.Join(home, ".claude")
	second := filepath.Join(home, ".claude2")
	options := GlobalAgentsOptions{Home: home, ClaudeConfigDirs: []string{first, second}}

	options.Mode = ModeCheck
	plan, err := RunGlobalAgents(options)
	if err != nil {
		t.Fatalf("RunGlobalAgents check: %v", err)
	}
	for _, want := range []string{
		filepath.Join(first, "agents", "alpha.md"),
		filepath.Join(second, "agents", "alpha.md"),
	} {
		if !hasGlobalAgentLinkAction(plan.Actions, want, source) {
			t.Fatalf("check mode planned no link for %s: %#v", want, plan.Actions)
		}
	}

	options.Mode = ModeBuild
	if _, err := RunGlobalAgents(options); err != nil {
		t.Fatalf("RunGlobalAgents build: %v", err)
	}
	assertGlobalSymlink(t, filepath.Join(first, "agents", "alpha.md"), source)
	assertGlobalSymlink(t, filepath.Join(second, "agents", "alpha.md"), source)

	options.Mode = ModeCheck
	settled, err := RunGlobalAgents(options)
	if err != nil {
		t.Fatalf("RunGlobalAgents recheck: %v", err)
	}
	if len(settled.Actions) != 0 || len(settled.Problems) != 0 {
		t.Fatalf("a settled install still reports work: actions=%#v problems=%#v", settled.Actions, settled.Problems)
	}
}

// TestGlobalAgentsTolerateAnAliasedAccountRegistry is the real host shape:
// the second account's agents/ registry is itself a directory symlink into
// the first account's registry. Both desired targets resolve to the same
// physical file, so build must converge with no error and a recheck must
// report zero actions and zero problems — never a CONFLICT, and never an
// "file exists" failure from linking the same physical path twice.
func TestGlobalAgentsTolerateAnAliasedAccountRegistry(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(home, ".professor", "templates", "global", "agents", "alpha.md")
	writeTestFile(t, source, "---\nname: alpha\ndescription: Alpha role for testing.\n---\n\nbody\n")
	first := filepath.Join(home, ".claude")
	second := filepath.Join(home, ".claude2")
	if err := os.MkdirAll(filepath.Join(first, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(second, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(first, "agents"), filepath.Join(second, "agents")); err != nil {
		t.Fatal(err)
	}
	options := GlobalAgentsOptions{Home: home, ClaudeConfigDirs: []string{first, second}, Mode: ModeBuild}

	if _, err := RunGlobalAgents(options); err != nil {
		t.Fatalf("RunGlobalAgents build over an aliased registry: %v", err)
	}
	assertGlobalSymlink(t, filepath.Join(second, "agents", "alpha.md"), source)

	options.Mode = ModeCheck
	settled, err := RunGlobalAgents(options)
	if err != nil {
		t.Fatalf("RunGlobalAgents recheck: %v", err)
	}
	if len(settled.Actions) != 0 || len(settled.Problems) != 0 {
		t.Fatalf("an aliased registry is not settled: actions=%#v problems=%#v", settled.Actions, settled.Problems)
	}
}

// hasGlobalAgentLinkAction reports whether the plan carries the exact link
// action for one desired target — an action list that merely has the right
// LENGTH would pass while pointing everywhere but the account that is missing
// its agents.
func hasGlobalAgentLinkAction(actions []GlobalAgentAction, path, target string) bool {
	for _, action := range actions {
		if action.Kind == "link" && action.Path == path && action.Target == target {
			return true
		}
	}
	return false
}

// TestGlobalAgentsInstallsCodexRoleAsRegularFile is the regression for the
// defect that made every machine-global role unspawnable on Codex ≥0.154:
// the role loader reads a role file through read_sensitive_file_to_string
// (codex-rs/exec-server/src/regular_file.rs), which opens with O_NOFOLLOW and
// rejects a symlink outright, and codex-rs/core/src/agent/role.rs renders that
// rejection as the single vague line "agent type is currently not available".
// A symlink at {codex home}/agents/<name>.toml is therefore not an installed
// role at all — it is a role Codex refuses to load.
func TestGlobalAgentsInstallsCodexRoleAsRegularFile(t *testing.T) {
	home := t.TempDir()
	writeTestFile(t, filepath.Join(home, ".professor", "templates", "global", "agents", "alpha.md"),
		"---\nname: alpha\ndescription: Alpha role for testing.\n---\n\nAlpha body.\n")

	if _, err := RunGlobalAgents(GlobalAgentsOptions{Home: home}); err != nil {
		t.Fatalf("RunGlobalAgents: %v", err)
	}

	role := filepath.Join(home, ".codex", "agents", "alpha.toml")
	info, err := os.Lstat(role)
	if err != nil {
		t.Fatalf("expected an installed role at %s: %v", role, err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("%s mode=%v — Codex rejects anything but a regular file", role, info.Mode())
	}
	assertTestFileContains(t, role, globalRoleMarkerPrefix, `name = "alpha"`)
}

// TestGlobalAgentsMigratesOwnedCodexRoleLinkToRegularFile covers the upgrade
// path every host that ever ran the symlinking installer is on: the link is
// pfm's own, so it is replaced by the real file rather than reported as a
// conflict.
func TestGlobalAgentsMigratesOwnedCodexRoleLinkToRegularFile(t *testing.T) {
	home := t.TempDir()
	writeTestFile(t, filepath.Join(home, ".professor", "templates", "global", "agents", "alpha.md"),
		"---\nname: alpha\ndescription: Alpha role for testing.\n---\n\nAlpha body.\n")
	legacy := filepath.Join(paths.LegacyGeneratedCodexAgentsDir(home), "alpha.toml")
	writeTestFile(t, legacy, "name = \"alpha\"\n")
	role := filepath.Join(home, ".codex", "agents", "alpha.toml")
	if err := os.MkdirAll(filepath.Dir(role), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(legacy, role); err != nil {
		t.Fatal(err)
	}

	result, err := RunGlobalAgents(GlobalAgentsOptions{Home: home})
	if err != nil {
		t.Fatalf("RunGlobalAgents: %v", err)
	}
	info, err := os.Lstat(role)
	if err != nil {
		t.Fatalf("expected a migrated role at %s: %v", role, err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("%s mode=%v — the pfm-owned link was not migrated to a regular file", role, info.Mode())
	}
	if len(result.Problems) != 0 {
		t.Fatalf("problems = %#v, want none: a pfm-owned link is ours to migrate", result.Problems)
	}
}

// TestGlobalAgentsRefusesForeignCodexRoleFile is the other half of ownership:
// a regular file of the same name that pfm did not write carries no generated
// marker, so it is reported and left byte-for-byte alone. Overwriting it
// would destroy an operator's own role.
func TestGlobalAgentsRefusesForeignCodexRoleFile(t *testing.T) {
	home := t.TempDir()
	writeTestFile(t, filepath.Join(home, ".professor", "templates", "global", "agents", "alpha.md"),
		"---\nname: alpha\ndescription: Alpha role for testing.\n---\n\nAlpha body.\n")
	role := filepath.Join(home, ".codex", "agents", "alpha.toml")
	const foreign = "name = \"alpha\"\ndescription = \"hand-written by the operator\"\n"
	writeTestFile(t, role, foreign)

	result, err := RunGlobalAgents(GlobalAgentsOptions{Home: home})
	if err != nil {
		t.Fatalf("RunGlobalAgents: %v", err)
	}
	if got := string(mustReadTestFile(t, role)); got != foreign {
		t.Fatalf("%s was overwritten:\n%s", role, got)
	}
	if len(result.Problems) == 0 {
		t.Fatalf("a foreign role file was silently accepted: problems = %#v", result.Problems)
	}
}
