package codexgen

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestLoadConfigRejectsUnknownFieldsAndBadVersion(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, ".claude", "codex-build.json"), `{"version":1,"mystery":true}`)
	if _, err := loadConfig(root, CLIOverrides{}); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field: got %v", err)
	}

	writeTestFile(t, filepath.Join(root, ".claude", "codex-build.json"), `{"version":2}`)
	if _, err := loadConfig(root, CLIOverrides{}); err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("bad version: got %v", err)
	}
}

func TestGlobalCommandsCanBeDisabledWithoutTouchingGlobalOutputs(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	writeTestFile(t, filepath.Join(root, "CLAUDE.md"), "# Fixture\n")
	writeTestFile(t, filepath.Join(root, ".claude", "commands", "local.md"), "---\ndescription: local\n---\nlocal\n")
	writeTestFile(t, filepath.Join(home, ".claude", "commands", "global.md"), "---\ndescription: global\n---\nglobal\n")

	// The default remains compatible: global outputs are produced when the
	// project does not opt out.
	if result, err := Run(Options{Root: root, Home: home, Mode: ModeBuild}); err != nil || !result.OK {
		t.Fatalf("seed build: result=%#v err=%v", result, err)
	}
	globalPrompt := filepath.Join(home, ".codex", "prompts", "global.md")
	globalSkill := filepath.Join(home, ".codex", "skills", "global", "SKILL.md")
	beforePrompt := string(mustReadTestFile(t, globalPrompt))
	beforeSkill := string(mustReadTestFile(t, globalSkill))

	writeTestFile(t, filepath.Join(root, ".claude", "codex-build.json"), `{"version":1,"globalCommands":false}`)
	writeTestFile(
		t,
		filepath.Join(home, ".claude", "commands", "global.md"),
		"---\ndescription: changed\n---\nchanged\n",
	)
	result, err := Run(Options{Root: root, Home: home, Mode: ModeBuild})
	if err != nil || !result.OK {
		t.Fatalf("opt-out build: result=%#v err=%v", result, err)
	}
	if got := string(mustReadTestFile(t, globalPrompt)); got != beforePrompt {
		t.Fatalf("global prompt changed while disabled: %q", got)
	}
	if got := string(mustReadTestFile(t, globalSkill)); got != beforeSkill {
		t.Fatalf("global skill changed while disabled: %q", got)
	}
	if _, err := os.Stat(filepath.Join(root, ".codex", "skills", "local", "SKILL.md")); err != nil {
		t.Fatalf("repository output missing while globals disabled: %v", err)
	}
	check, err := Run(Options{Root: root, Home: home, Mode: ModeCheck})
	if err != nil || !check.OK {
		t.Fatalf("disabled global source should not make check stale: result=%#v err=%v", check, err)
	}

	// Disabling global reconciliation must not weaken the independent TOML
	// safety gate: malformed existing global artifacts remain visible.
	writeTestFile(t, filepath.Join(home, ".codex", "config.toml"), "broken = [\n")
	check, err = Run(Options{Root: root, Home: home, Mode: ModeCheck})
	if err != nil || check.OK || !containsFinding(check.Problems, "UNPARSEABLE") {
		t.Fatalf("malformed global TOML was not reported: result=%#v err=%v", check, err)
	}
}

func TestGlobalCommandsConfigDefaultsTrue(t *testing.T) {
	root := t.TempDir()
	cfg, err := loadConfig(root, CLIOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.GlobalCommands {
		t.Fatal("GlobalCommands default = false, want true")
	}
	writeTestFile(t, filepath.Join(root, ".claude", "codex-build.json"), `{"version":1,"globalCommands":false}`)
	cfg, err = loadConfig(root, CLIOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GlobalCommands {
		t.Fatal("GlobalCommands explicit false was ignored")
	}
}

func TestGlobalCommandsOnlyReconcilePreservesForeignFilesAndDeletesManagedOrphans(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(home, ".claude", "commands", "fixture.md")
	writeTestFile(t, source, "---\ndescription: fixture\n---\nUse /fixture.\n")
	build, err := RunGlobalCommands(GlobalCommandsOptions{Home: home, Mode: ModeBuild})
	if err != nil || !build.OK || build.Wrote != 2 {
		t.Fatalf("initial global build: result=%#v err=%v", build, err)
	}
	prompt := filepath.Join(home, ".codex", "prompts", "fixture.md")
	skill := filepath.Join(home, ".codex", "skills", "fixture", "SKILL.md")
	for _, path := range []string{prompt, skill} {
		if !generatedBytes(mustReadTestFile(t, path)) {
			t.Fatalf("generated output %s has no ownership marker", path)
		}
	}

	foreignPrompt := filepath.Join(home, ".codex", "prompts", "foreign.md")
	foreignSkill := filepath.Join(home, ".codex", "skills", "foreign", "SKILL.md")
	writeTestFile(t, foreignPrompt, "operator prompt\n")
	writeTestFile(t, foreignSkill, "operator skill\n")
	foreignLink := filepath.Join(home, ".codex", "skills", "foreign-link")
	if err := os.Symlink(filepath.Join(home, "operator-skill"), foreignLink); err != nil {
		t.Fatal(err)
	}
	managedOrphanPrompt := filepath.Join(home, ".codex", "prompts", "swap.md")
	managedOrphanSkill := filepath.Join(home, ".codex", "skills", "swap", "SKILL.md")
	writeTestFile(t, managedOrphanPrompt, generatedHeader("swap")+"\n")
	writeTestFile(t, managedOrphanSkill, generatedHeader("swap")+"\n")
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}

	check, err := RunGlobalCommands(GlobalCommandsOptions{Home: home, Mode: ModeCheck})
	if err != nil || check.OK || !containsFinding(check.Problems, "ORPHAN") {
		t.Fatalf("read-only global check: result=%#v err=%v", check, err)
	}
	for _, path := range []string{prompt, skill, managedOrphanPrompt, managedOrphanSkill, foreignPrompt, foreignSkill, foreignLink} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("check mutated %s: %v", path, err)
		}
	}

	build, err = RunGlobalCommands(GlobalCommandsOptions{Home: home, Mode: ModeBuild})
	if err != nil || !build.OK || build.Deleted != 4 {
		t.Fatalf("orphan cleanup: result=%#v err=%v", build, err)
	}
	for _, path := range []string{prompt, skill, managedOrphanPrompt, managedOrphanSkill} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("managed orphan remains at %s: %v", path, err)
		}
	}
	for _, path := range []string{foreignPrompt, foreignSkill, foreignLink} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("foreign file was removed at %s: %v", path, err)
		}
	}
}

// TestGlobalCommandUninstallDeletesTheOrphanedSkillDirectorySymlink is the
// regression for a dangling `pfm uninstall` leftover: compileGlobalCommands
// mirrors a skill-directory global command (a {home}/.claude/commands/<name>
// directory carrying its own SKILL.md, e.g. "tokens") into
// {home}/.codex/skills/<name> as a bare SYMLINK into that source directory —
// the one output shape this compiler writes that cannot carry the marker
// generatedBytes looks for. When the installer tears the command down
// (unwireGlobalRegistries in the installer package) and then reconciles the
// Codex mirror against an intentionally empty source, the old markerClaimable
// treated every symlink in the managed registry as foreign and left the now
// pointing-nowhere .codex/skills/<name> link behind — INSTALL.md § Uninstall
// promises every installer-owned link is removed. Ownership here is decided
// by the symlink's TARGET the same way global_unwire.go's ownedGlobalLink
// decides registry-link ownership: pointing inside {home}/.claude/commands
// names it as this compiler's, whatever else sits there afterward.
func TestGlobalCommandUninstallDeletesTheOrphanedSkillDirectorySymlink(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(home, ".claude", "commands", "tokens", "SKILL.md")
	writeTestFile(t, source, "---\ndescription: tokens\n---\nUse /tokens.\n")

	build, err := RunGlobalCommands(GlobalCommandsOptions{Home: home, Mode: ModeBuild})
	if err != nil || !build.OK {
		t.Fatalf("initial global build: result=%#v err=%v", build, err)
	}
	link := filepath.Join(home, ".codex", "skills", "tokens")
	info, err := os.Lstat(link)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected %s to be a symlink after build: info=%#v err=%v", link, info, err)
	}

	// Simulate the uninstall order: the source command directory is gone
	// (unwireGlobalRegistries already retired it) before the Codex mirror is
	// reconciled against an intentionally empty SourceHome, the exact shape
	// reconcileCodexCommands(nil) in installer.go uses for ModeUninstall.
	if err := os.RemoveAll(filepath.Join(home, ".claude", "commands", "tokens")); err != nil {
		t.Fatal(err)
	}
	emptySource := t.TempDir()
	uninstallBuild, err := RunGlobalCommands(
		GlobalCommandsOptions{Home: home, SourceHome: emptySource, Mode: ModeBuild},
	)
	if err != nil || !uninstallBuild.OK {
		t.Fatalf("uninstall reconciliation: result=%#v err=%v", uninstallBuild, err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("uninstall left the dangling Codex skill symlink %s behind: %v", link, err)
	}
}

func TestFullCheckAgreesWithInstallerGlobalReconciliation(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	writeTestFile(t, filepath.Join(root, "CLAUDE.md"), "# Fixture\n")
	writeTestFile(t, filepath.Join(root, ".claude", "commands", "dev.md"), "---\ndescription: dev\n---\nRun /dev.\n")
	writeTestFile(
		t,
		filepath.Join(home, ".claude", "commands", "chat", "inject.md"),
		"---\ndescription: inject\n---\nInject.\n",
	)
	writeTestFile(
		t,
		filepath.Join(home, ".claude", "commands", "chat", "new.md"),
		"---\ndescription: new\n---\nContinue with /chat:inject.\n",
	)

	initial, err := Build(Options{Root: root, Home: home})
	if err != nil || !initial.OK {
		t.Fatalf("initial full build: result=%#v err=%v", initial, err)
	}
	installed, err := RunGlobalCommands(GlobalCommandsOptions{Home: home, Mode: ModeBuild})
	if err != nil || !installed.OK {
		t.Fatalf("installer global reconciliation: result=%#v err=%v", installed, err)
	}
	check, err := Check(Options{Root: root, Home: home})
	if err != nil {
		t.Fatalf("full check: %v", err)
	}
	if !check.OK {
		t.Fatalf("full check disagrees with installer reconciliation: %#v", check.Problems)
	}
}

func TestMCPBackedChatCommandsRetireGlobalSkillsButKeepInterrogate(t *testing.T) {
	home := t.TempDir()
	for relative := range map[string]bool{
		"chat/inject.md":      true,
		"chat/interrogate.md": true,
		"reload.md":           true,
	} {
		writeTestFile(
			t,
			filepath.Join(home, ".claude", "commands", relative),
			"---\ndescription: fixture\n---\nfixture\n",
		)
	}
	managedInject := filepath.Join(home, ".codex", "skills", "chat-inject", "SKILL.md")
	writeTestFile(t, managedInject, generatedHeader("old-chat-inject")+"\n")
	result, err := RunGlobalCommands(GlobalCommandsOptions{Home: home, Mode: ModeBuild})
	if err != nil || !result.OK {
		t.Fatalf("global command reconciliation: result=%#v err=%v", result, err)
	}
	for _, path := range []string{
		filepath.Join(home, ".codex", "prompts", "chat-inject.md"),
		filepath.Join(home, ".codex", "prompts", "chat-interrogate.md"),
		filepath.Join(home, ".codex", "skills", "chat-interrogate", "SKILL.md"),
		filepath.Join(home, ".codex", "skills", "reload", "SKILL.md"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected global artifact %s: %v", path, err)
		}
	}
	if _, err := os.Stat(managedInject); !os.IsNotExist(err) {
		t.Fatalf("MCP-backed global chat skill remains: %v", err)
	}
}

// TestCommandSwapNeverRewritesASlashContinuedPath is the regression for a
// real corruption: swapCommands matched a command name by its PREFIX only,
// so a filesystem path that merely starts with a command name (/dev/tty)
// was rewritten right along with the actual command invocation (/dev build
// pfm). A slash command is followed by whitespace, end-of-line, or
// punctuation — never by another "/".
func TestCommandSwapNeverRewritesASlashContinuedPath(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	writeTestFile(t, filepath.Join(root, ".claude", "commands", "dev.md"), "---\ndescription: dev\n---\ndev\n")
	writeTestFile(
		t,
		filepath.Join(root, "CLAUDE.md"),
		"# Fixture\n\nTUI renders on /dev/tty. Use `/dev build pfm` for anything the pipeline reads.\n",
	)
	result, err := Run(Options{Root: root, Home: home, Mode: ModeBuild})
	if err != nil || !result.OK {
		t.Fatalf("build: result=%#v err=%v", result, err)
	}
	agents := string(mustReadTestFile(t, filepath.Join(root, "AGENTS.md")))
	if !strings.Contains(agents, "TUI renders on /dev/tty.") {
		t.Fatalf("a /-continued path was rewritten, corrupting it:\n%s", agents)
	}
	if !strings.Contains(agents, "Use `$dev build pfm` for anything the pipeline reads.") {
		t.Fatalf("the real /dev command invocation was not rewritten:\n%s", agents)
	}
}

func TestFrontmatterAndRosterTransform(t *testing.T) {
	raw := "---\ndescription: >-\n  A quoted \\\"description\\\"\n  over two lines.\nmodel: opus\nhooks:\n  PostToolUse:\n    - matcher: Edit\n---\nUse /tools:go, not /tools or /scripts/x. Read CLAUDE.md.\n"
	fm, body, err := parseFrontmatter(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := fm["description"], `A quoted \"description\" over two lines.`; got != want {
		t.Fatalf("description = %q, want %q", got, want)
	}
	got := transformMarkdown(body, TransformOptions{
		ModelMap:          map[string]string{"opus": "gpt-frontier"},
		Commands:          map[string]string{"tools:go": "tools-go"},
		ReplaceClaudeFile: true,
	})
	for _, want := range []string{"$tools-go", "/tools or", "/scripts/x", "AGENTS.md"} {
		if !strings.Contains(got, want) {
			t.Fatalf("transform missing %q in %q", want, got)
		}
	}
}

func TestFrontmatterUnquotesYAMLScalars(t *testing.T) {
	raw := "---\n" +
		"single: 'a: b, \"c\" and it''s fine'\n" +
		"double: \"line one\\n\\\"line two\\\" \\\\ end\"\n" +
		"plain: unchanged: value\n" +
		"---\nbody\n"
	fields, _, err := parseFrontmatter(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"single": `a: b, "c" and it's fine`,
		"double": "line one\n\"line two\" \\ end",
		"plain":  "unchanged: value",
	}
	for key, expected := range want {
		if got := fields[key]; got != expected {
			t.Errorf("%s = %q, want %q", key, got, expected)
		}
	}
}

func TestFrontmatterStripsPlainScalarCommentAndPreservesQuotedHash(t *testing.T) {
	raw := "---\nmodel: opus # pinned\nquoted: 'keep # this'\n---\nbody\n"
	fields, _, err := parseFrontmatter(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got := fields["model"]; got != "opus" {
		t.Fatalf("model = %q, want %q", got, "opus")
	}
	if got := fields["quoted"]; got != "keep # this" {
		t.Fatalf("quoted = %q, want %q", got, "keep # this")
	}
	if _, _, err := parseFrontmatter("---\nbad: \"invalid\\x\"\n---\nbody\n"); err == nil ||
		!strings.Contains(err.Error(), "frontmatter field bad") {
		t.Fatalf("malformed quoted scalar error = %v", err)
	}
}

func TestDanglingGlobalCommandIsHonestAndCheckWritesNothing(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	writeTestFile(t, filepath.Join(root, "CLAUDE.md"), "# Fixture\n")
	commandDir := filepath.Join(home, ".claude", "commands")
	if err := os.MkdirAll(commandDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing-command.md", filepath.Join(commandDir, "gone.md")); err != nil {
		t.Fatal(err)
	}

	build, err := Run(Options{Root: root, Home: home, Mode: ModeBuild})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !containsFinding(build.Warnings, "dangling") {
		t.Fatalf("build warnings = %#v", build.Warnings)
	}

	if err := os.Remove(filepath.Join(root, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	check, err := Run(Options{Root: root, Home: home, Mode: ModeCheck})
	if err != nil {
		t.Fatalf("check execution: %v", err)
	}
	if check.OK || !containsFinding(check.Problems, "dangling") || !containsFinding(check.Problems, "MISSING") {
		t.Fatalf("check = %#v", check)
	}
	if _, err := os.Stat(filepath.Join(root, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatalf("check wrote AGENTS.md: %v", err)
	}
}

func TestIncumbentUnionFixtureBuildThenReadOnlyCheck(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	writeTestFile(t, filepath.Join(root, ".claude", "codex-build.json"), `{
  "version": 1,
  "modelMap": {"sonnet":"gpt-fixture","opus":"gpt-5.6-sol"},
  "rootAdapter": "\n## Fixture adapter\n",
  "agentPreamble": "Role ${name} starts here.\n\n",
  "excludeDirs": ["references"],
  "excludeProjects": ["template"],
  "neverRegister": ["private"],
  "suffixMode": "strip-prefix",
  "suffixPrefix": "sample-"
}`)
	writeTestFile(t, filepath.Join(root, "CLAUDE.md"), "# Root\n\nUse /tools:go with sonnet. Read CLAUDE.md.\n")
	writeTestFile(t, filepath.Join(root, "sample-api", "CLAUDE.md"), "# API\n")
	writeTestFile(t, filepath.Join(root, "template", "CLAUDE.md"), "# Must stay excluded\n")
	writeTestFile(
		t,
		filepath.Join(root, ".claude", "commands", "tools", "go.md"),
		"---\ndescription: >-\n  Run sonnet cards\n  safely\n---\nBody keeps CLAUDE.md and /tools:go with sonnet.\n",
	)
	writeTestFile(t, filepath.Join(root, ".claude", "commands", "references", "killed.md"), "killed\n")
	writeTestFile(
		t,
		filepath.Join(root, ".claude", "agents", "reviewer.md"),
		"---\ndescription: >-\n  Review \\\"quoted\\\" output\nmodel: opus # pinned\neffort: high\ntools: Read, Grep\n---\nFollow /tools:go.\n",
	)
	writeTestFile(
		t,
		filepath.Join(root, ".claude", "agents", "unmapped.md"),
		"---\ndescription: unmapped model\nmodel: something-else\n---\nunmapped\n",
	)
	writeTestFile(
		t,
		filepath.Join(root, ".claude", "agents", "escaped.md"),
		"---\ndescription: escaped values\nmodel: 'model\\path\"quoted'\neffort: 'effort\\path\"quoted'\n---\nescaped\n",
	)
	writeTestFile(t, filepath.Join(root, ".claude", "agents", "private.md"), "---\ndescription: private\n---\nno\n")
	writeTestFile(
		t,
		filepath.Join(root, "sample-api", ".claude", "agents", "worker.md"),
		"---\ndescription: child\n---\nchild\n",
	)
	writeTestFile(t, filepath.Join(root, ".claude", "skills", "native", "SKILL.md"), "# Native\n")
	writeTestFile(t, filepath.Join(home, ".claude", "commands", "global.md"), "---\ndescription: global\n---\nglobal\n")
	writeTestFile(
		t,
		filepath.Join(home, ".claude", "commands", "side.md"),
		"---\ndescription: side\ndisable-model-invocation: true\n---\nside\n",
	)
	writeTestFile(t, filepath.Join(root, ".codex", "config.toml"), "model = \"fixture\"\n")
	writeTestFile(
		t,
		filepath.Join(root, ".mcp.json"),
		`{"mcpServers":{"fixture":{"command":"pfm","args":["mcp"],"future":true}}}`,
	)

	build, err := Run(Options{Root: root, Home: home, Mode: ModeBuild})
	if err != nil || !build.OK {
		t.Fatalf("build: result=%#v err=%v", build, err)
	}
	if !containsFinding(build.Warnings, "fields not mapped") {
		t.Fatalf("MCP warning missing: %#v", build.Warnings)
	}
	assertTestFileContains(
		t,
		filepath.Join(root, "AGENTS.md"),
		"Generated by pfm codex build",
		"$tools-go",
		"gpt-fixture",
		"AGENTS.md",
		"## Fixture adapter",
	)
	if _, err := os.Stat(filepath.Join(root, "template", "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatalf("excluded template AGENTS.md exists: %v", err)
	}
	assertTestFileContains(
		t,
		filepath.Join(root, ".codex", "agents", "reviewer.toml"),
		`description = "Review \\\"quoted\\\" output"`,
		`model = "gpt-5.6-sol"`,
		`model_reasoning_effort = "high"`,
		"Role reviewer starts here.",
		"$tools-go",
	)
	reviewer := string(mustReadTestFile(t, filepath.Join(root, ".codex", "agents", "reviewer.toml")))
	ordered := []string{
		`name = "reviewer"`,
		`description = "Review \\\"quoted\\\" output"`,
		`model = "gpt-5.6-sol"`,
		`model_reasoning_effort = "high"`,
		`developer_instructions = """`,
	}
	last := -1
	for _, needle := range ordered {
		index := strings.Index(reviewer, needle)
		if index <= last {
			t.Fatalf("reviewer.toml key %q at %d after prior key at %d:\n%s", needle, index, last, reviewer)
		}
		last = index
	}
	assertTestFileContains(t, filepath.Join(root, ".codex", "agents", "unmapped.toml"), `model = "something-else"`)
	assertTestFileContains(
		t,
		filepath.Join(root, ".codex", "agents", "escaped.toml"),
		`model = "model\\path\"quoted"`,
		`model_reasoning_effort = "effort\\path\"quoted"`,
	)
	worker := string(mustReadTestFile(t, filepath.Join(root, ".codex", "agents", "worker-api.toml")))
	if !strings.Contains(worker, `name = "worker_api"`) || strings.Contains(worker, "\nmodel =") ||
		strings.Contains(worker, "\nmodel_reasoning_effort =") {
		t.Fatalf("worker-api.toml must omit absent model and effort keys:\n%s", worker)
	}
	if _, err := os.Stat(filepath.Join(root, ".codex", "agents", "private.toml")); !os.IsNotExist(err) {
		t.Fatalf("never-register agent exists: %v", err)
	}
	commandSkill := filepath.Join(root, ".codex", "skills", "tools-go", "SKILL.md")
	assertTestFileContains(
		t,
		commandSkill,
		"name: tools-go",
		"Run sonnet cards",
		"safely",
		"Body keeps CLAUDE.md",
		"$tools-go",
		"gpt-fixture",
	)
	if strings.Contains(string(mustReadTestFile(t, commandSkill)), "Run gpt-fixture cards") {
		t.Fatal("command frontmatter model alias was rewritten")
	}
	if _, err := os.Stat(
		filepath.Join(root, ".codex", "skills", "references-killed", "SKILL.md"),
	); !os.IsNotExist(
		err,
	) {
		t.Fatalf("excluded command exists: %v", err)
	}
	if target, err := os.Readlink(filepath.Join(root, ".codex", "skills", "native")); err != nil || target == "" {
		t.Fatalf("native skill link: target=%q err=%v", target, err)
	}
	assertTestFileContains(t, filepath.Join(home, ".codex", "prompts", "side.md"), "name: side")
	if _, err := os.Stat(filepath.Join(home, ".codex", "skills", "side", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("non-model-invocable global skill exists: %v", err)
	}
	assertTestFileContains(
		t,
		filepath.Join(root, ".codex", "config.toml"),
		`model = "fixture"`,
		"generated by pfm codex build",
		"[mcp_servers.fixture]",
	)

	before := snapshotTestTree(t, root, home)
	check, err := Run(Options{Root: root, Home: home, Mode: ModeCheck})
	if err != nil || !check.OK {
		t.Fatalf("check: result=%#v err=%v", check, err)
	}
	if after := snapshotTestTree(t, root, home); after != before {
		t.Fatalf("check wrote files:\nbefore=%s\nafter=%s", before, after)
	}
}

func TestCheckRejectsInvalidKeeperTOML(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	writeTestFile(t, filepath.Join(root, "CLAUDE.md"), "# Fixture\n")
	writeTestFile(t, filepath.Join(root, ".codex", "config.toml"), "broken = [\n")
	result, err := Run(Options{Root: root, Home: home, Mode: ModeCheck})
	if err != nil {
		t.Fatal(err)
	}
	if result.OK || !containsFinding(result.Problems, "UNPARSEABLE") {
		t.Fatalf("invalid TOML check = %#v", result)
	}
}

func TestReconcileFindingsAreNamedAndNonDestructive(t *testing.T) {
	t.Run("stale and orphan check findings", func(t *testing.T) {
		root := t.TempDir()
		home := t.TempDir()
		writeTestFile(t, filepath.Join(root, "CLAUDE.md"), "# Fixture\n")

		if result, err := Run(Options{Root: root, Home: home, Mode: ModeBuild}); err != nil || !result.OK {
			t.Fatalf("seed build: result=%#v err=%v", result, err)
		}
		writeTestFile(t, filepath.Join(root, "AGENTS.md"), "stale generated content\n")
		writeTestFile(
			t,
			filepath.Join(root, ".codex", "agents", "orphan.toml"),
			generatedMarker+"\nname = \"orphan\"\n",
		)
		before := snapshotTestTree(t, root, home)

		result, err := Run(Options{Root: root, Home: home, Mode: ModeCheck})
		if err != nil {
			t.Fatalf("check: %v", err)
		}
		if result.OK || !containsFinding(result.Problems, "STALE") || !containsFinding(result.Problems, "ORPHAN") {
			t.Fatalf("check findings = %#v", result)
		}
		if after := snapshotTestTree(t, root, home); after != before {
			t.Fatalf("check changed files:\nbefore=%s\nafter=%s", before, after)
		}
	})

	t.Run("build conflict is named and preserved", func(t *testing.T) {
		root := t.TempDir()
		home := t.TempDir()
		writeTestFile(t, filepath.Join(root, "CLAUDE.md"), "# Fixture\n")
		writeTestFile(
			t,
			filepath.Join(root, ".claude", "agents", "reviewer.md"),
			"---\ndescription: reviewer\n---\nReview.\n",
		)
		if result, err := Run(Options{Root: root, Home: home, Mode: ModeBuild}); err != nil || !result.OK {
			t.Fatalf("seed build: result=%#v err=%v", result, err)
		}

		conflictPath := filepath.Join(root, ".codex", "agents", "reviewer.toml")
		writeTestFile(t, conflictPath, "hand-written keeper\n")
		before := snapshotTestTree(t, root, home)
		result, err := Run(Options{Root: root, Home: home, Mode: ModeBuild})
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		if result.OK || !containsFinding(result.Problems, "CONFLICT") {
			t.Fatalf("build findings = %#v", result)
		}
		if after := snapshotTestTree(t, root, home); after != before {
			t.Fatalf("conflict build changed files:\nbefore=%s\nafter=%s", before, after)
		}
		if got := string(mustReadTestFile(t, conflictPath)); got != "hand-written keeper\n" {
			t.Fatalf("conflict file changed: %q", got)
		}
	})
}

func containsFinding(items []string, needle string) bool {
	for _, item := range items {
		if strings.Contains(strings.ToLower(item), strings.ToLower(needle)) {
			return true
		}
	}
	return false
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustReadTestFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func assertTestFileContains(t *testing.T, path string, needles ...string) {
	t.Helper()
	body := string(mustReadTestFile(t, path))
	for _, needle := range needles {
		if !strings.Contains(body, needle) {
			t.Fatalf("%s missing %q:\n%s", path, needle, body)
		}
	}
}

func snapshotTestTree(t *testing.T, roots ...string) string {
	t.Helper()
	rows := []string{}
	for _, root := range roots {
		err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if info.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				target, err := os.Readlink(path)
				if err != nil {
					return err
				}
				rows = append(rows, root+"/"+rel+" -> "+target)
				return nil
			}
			rows = append(rows, root+"/"+rel+" = "+string(mustReadTestFile(t, path)))
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	sort.Strings(rows)
	return strings.Join(rows, "\n")
}
