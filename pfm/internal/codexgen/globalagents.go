package codexgen

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// GlobalAgentsOptions selects the host HOME whose global Codex agents get
// (re)compiled and installed. The source directory is always
// {SourceRepo}/templates/global/agents. Installs land at
// {ClaudeConfigDir}/agents for every configured Claude account (a symlink to
// the raw .md, Claude reads it directly) and at {CodexHome}/agents for every
// configured Codex home — there as a REGULAR FILE holding the compiled TOML,
// because Codex refuses to load a role through a symlink (globalrole.go).
type GlobalAgentsOptions struct {
	Home string
	// SourceRepo is the clone the symlink targets and the source-repo
	// conflict check are anchored on. Empty defaults to {Home}/.professor —
	// the documented clone location (INSTALL.md's $HOME/.professor) — so a
	// caller that never sets it (pfm codex agents has no --source-repo flag)
	// keeps today's default.
	SourceRepo string
	// ClaudeConfigDirs are the Claude config dirs whose agents/ registry
	// receives one link per global agent — every account the host has
	// configured, the same fanout the installer's retire paths already walk.
	// Nil or empty means {Home}/.claude alone: the legacy single-account
	// behavior every caller that never learned about accounts still gets.
	ClaudeConfigDirs []string
	// CodexHomes are the Codex homes whose agents/ registry receives one role
	// FILE per global agent — every account the host has configured. Nil or
	// empty means {Home}/.codex alone.
	CodexHomes []string
	Mode       Mode
}

// GlobalAgentCompiled is one desired role file or rendered variant source.
// Build writes changed bytes; check reports the same desired artifact without
// writing it.
type GlobalAgentCompiled struct {
	Path string
	Size int64
}

// GlobalAgentInstalled is one desired registry symlink and the shape check
// found there — the classification codexgen.GlobalLinkState names, so a
// missing, drifted, or foreign target can never certify itself as installed.
type GlobalAgentInstalled struct {
	Path   string
	Source string
	State  GlobalLinkState
	Found  string
}

// GlobalAgentsResult reports every desired artifact in the same two phases
// the host script performed. Actions narrows that roster to paths build would
// actually replace; Problems carries every conflict build refused to touch.
type GlobalAgentsResult struct {
	Compiled  []GlobalAgentCompiled
	Installed []GlobalAgentInstalled
	// Roles is one desired {CodexHome}/agents/<name>.toml per role per Codex
	// home, with the shape check found there before this run wrote anything.
	Roles    []GlobalRoleInstalled
	Actions  []GlobalAgentAction
	Problems []string
}

// GlobalRoleInstalled is one desired Codex role file and the shape check found
// at its path — the classification GlobalRoleState names, so a symlinked,
// drifted, or foreign role can never certify itself as installed. Found
// carries a symlink's resolved target and is empty otherwise.
type GlobalRoleInstalled struct {
	Path  string
	State GlobalRoleState
	Found string
}

// GlobalAgentAction is one exact path a build would replace. Check mode emits
// the same action list without touching disk, which lets the host installer
// present its complete plan before any earlier install mutation can land.
// Target is the symlink source for a "link" action; it is empty for the
// "write" actions the .toml compile step still performs.
type GlobalAgentAction struct {
	Kind   string
	Path   string
	Target string
}

// Codex has no Agent tool — the lead spawns children through spawn_agent
// instead. This is the one Codex-specific substitution the host script
// applies to every agent body; it is a literal, not a general transform.
const (
	globalAgentBodyOld = "children are Explore+haiku (never\nyour own type)"
	globalAgentBodyNew = "children are spawned via spawn_agent as the `explorer` role (never your own type)"
)

// RunGlobalAgents is the Go port of the retired host script
// ~/.professor/templates/global/agents/build-global-agents.py: it compiles
// every {SourceRepo}/templates/global/agents/*.md, validates every compiled
// TOML parses, then installs each agent twice.
//
// Claude gets a SYMLINK: every configured Claude agents registry
// (ClaudeConfigDirs, {Home}/.claude by default) points at the clone's .md, so
// an edit to the source propagates with no reinstall. A regular-file copy
// already at a desired target (the shape the old copy-based installer left
// behind) is replaced with the link; a symlink pointing outside the source
// repository is a conflict this never touches — see
// ClassifyGlobalLink/ApplyGlobalLink in globallink.go.
//
// Codex gets a REGULAR FILE: {CodexHome}/agents/<name>.toml holds the compiled
// bytes outright, because Codex's role loader opens a role with O_NOFOLLOW and
// rejects a symlink as "agent type is currently not available". A role file
// pfm wrote is recognised by its generated marker and rewritten when it
// drifts; a pre-migration pfm symlink is replaced by the real file; anything
// else of that name is a conflict this never touches — see globalrole.go.
//
// TOML escaping mirrors build-codex.mjs:151-153 exactly — see
// globalAgentEscape / globalAgentEscapeMultiline — because a raw `"` in an
// agent description (real examples: the trigger phrases "walker fast",
// "map it now") breaks the TOML parser at Codex startup and takes the whole
// role down with it.
//
// A failed compile or an unparseable TOML aborts before any install, the
// same fail-loud order the host script used: an emitter that ships an
// unparseable artifact has done nothing useful.
func RunGlobalAgents(options GlobalAgentsOptions) (GlobalAgentsResult, error) {
	home, err := resolveHome(options.Home)
	if err != nil {
		return GlobalAgentsResult{}, err
	}
	sourceRepo := strings.TrimSpace(options.SourceRepo)
	if sourceRepo == "" {
		sourceRepo = filepath.Join(home, ".professor")
	}
	sourceRepo, err = filepath.Abs(sourceRepo)
	if err != nil {
		return GlobalAgentsResult{}, fmt.Errorf(
			"resolve global agents source repository %q: %w",
			options.SourceRepo,
			err,
		)
	}
	sourceRepo = filepath.Clean(sourceRepo)

	// Phase 1: compile and validate every role's TOML before touching disk —
	// a failed compile or an unparseable TOML aborts before any install, the
	// same fail-loud order the host script used.
	compiledAgents, err := compileGlobalAgents(home, sourceRepo)
	if err != nil {
		return GlobalAgentsResult{}, err
	}

	// One Claude agents/ registry per configured account; an installer that
	// never names its accounts keeps the single {Home}/.claude registry.
	claudeConfigDirs := options.ClaudeConfigDirs
	if len(claudeConfigDirs) == 0 {
		claudeConfigDirs = []string{filepath.Join(home, ".claude")}
	}

	// One Codex agents/ registry per configured Codex home; a caller that
	// never names its accounts (a nil roster, `pfm codex agents`) keeps the
	// single {Home}/.codex registry, while a non-nil empty roster means no
	// Codex home at all: no role is planned or written.
	codexHomes := options.CodexHomes
	if codexHomes == nil {
		codexHomes = []string{filepath.Join(home, ".codex")}
	}
	// The registries a pre-migration role symlink is allowed to point into —
	// the retired generated store, and the even older in-clone twin. A link
	// resolving anywhere else was never written by pfm.
	ownedLinkDirs := []string{
		paths.LegacyGeneratedCodexAgentsDir(home),
		filepath.Join(sourceRepo, "templates", "global", "agents"),
	}

	type desiredLink struct {
		target string
		source string
	}
	type desiredRole struct {
		target  string
		content []byte
	}
	result := GlobalAgentsResult{}
	links := make([]desiredLink, 0, len(compiledAgents)*len(claudeConfigDirs))
	roles := make([]desiredRole, 0, len(compiledAgents)*len(codexHomes))
	for _, agent := range compiledAgents {
		for _, codexHome := range codexHomes {
			target := filepath.Join(codexHome, "agents", agent.name+".toml")
			roles = append(roles, desiredRole{target: target, content: agent.tomlContent})
			result.Compiled = append(
				result.Compiled,
				GlobalAgentCompiled{Path: target, Size: int64(len(agent.tomlContent))},
			)
		}
		if agent.mdContent != nil {
			result.Compiled = append(
				result.Compiled,
				GlobalAgentCompiled{Path: agent.mdSource, Size: int64(len(agent.mdContent))},
			)
			same, err := sameGlobalAgentFile(agent.mdSource, agent.mdContent)
			if err != nil {
				return GlobalAgentsResult{}, err
			}
			if !same {
				result.Actions = append(result.Actions, GlobalAgentAction{Kind: actionWrite, Path: agent.mdSource})
			}
		}
		for _, config := range claudeConfigDirs {
			links = append(
				links,
				desiredLink{
					target: filepath.Join(config, "agents", filepath.Base(agent.mdSource)),
					source: agent.mdSource,
				},
			)
		}
	}

	// Phase 2a: classify every desired role file against what is actually on
	// disk. A foreign file is a Problem and never an Action — it stays exactly
	// as the operator left it.
	for _, role := range roles {
		state, foundAt, err := ClassifyGlobalRole(role.target, role.content, ownedLinkDirs)
		if err != nil {
			return GlobalAgentsResult{}, fmt.Errorf("inspect global role %s: %w", role.target, err)
		}
		found := GlobalRoleInstalled{Path: role.target, State: state, Found: foundAt}
		result.Roles = append(result.Roles, found)
		switch state {
		case GlobalRoleForeign:
			result.Problems = append(result.Problems, found.Describe())
			continue
		case GlobalRoleCurrent:
			continue
		}
		result.Actions = append(result.Actions, GlobalAgentAction{Kind: actionWrite, Path: role.target})
	}

	// Phase 2b: classify every desired registry symlink against what is
	// actually on disk. A stat failure other than "not found" bubbles up as
	// a genuine error — an unreadable target is never reported as absent.
	for _, link := range links {
		state, found, err := ClassifyGlobalLink(link.target, link.source, sourceRepo, GlobalLinkFile)
		if err != nil {
			return GlobalAgentsResult{}, fmt.Errorf("inspect global agent artifact %s: %w", link.target, err)
		}
		result.Installed = append(
			result.Installed,
			GlobalAgentInstalled{Path: link.target, Source: link.source, State: state, Found: found},
		)
		switch state {
		case GlobalLinkConflict:
			result.Problems = append(result.Problems, DescribeGlobalLinkState(state, link.target, link.source, found))
			continue
		case GlobalLinkCorrect:
			continue
		}
		result.Actions = append(
			result.Actions,
			GlobalAgentAction{Kind: actionLink, Path: link.target, Target: link.source},
		)
	}

	if options.Mode == ModeCheck {
		return result, nil
	}

	for _, agent := range compiledAgents {
		if agent.mdContent == nil {
			continue
		}
		same, err := sameGlobalAgentFile(agent.mdSource, agent.mdContent)
		if err != nil {
			return GlobalAgentsResult{}, err
		}
		if same {
			continue
		}
		if err := writeGlobalAgentFile(agent.mdSource, agent.mdContent); err != nil {
			return GlobalAgentsResult{}, err
		}
	}
	for _, role := range roles {
		// Re-classify against disk for the same reason the link loop below
		// does: two configured Codex homes can alias one physical registry,
		// so a role an earlier iteration wrote already satisfies this target.
		state, _, err := ClassifyGlobalRole(role.target, role.content, ownedLinkDirs)
		if err != nil {
			return GlobalAgentsResult{}, fmt.Errorf("re-inspect global role %s: %w", role.target, err)
		}
		if err := ApplyGlobalRole(role.target, role.content, state); err != nil {
			return GlobalAgentsResult{}, fmt.Errorf("install global role %s: %w", role.target, err)
		}
	}
	for _, installed := range result.Installed {
		// Re-classify against disk: the planning phase described the
		// filesystem as it was BEFORE any of these links landed, and two
		// configured accounts can alias one physical registry (the
		// hand-made {Home}/.claude2/agents -> {Home}/.claude/agents shape),
		// so a link an earlier iteration wrote already satisfies this
		// target. Applying the stale "missing" there would fail with
		// "file exists" on a host that is in fact correctly installed.
		state, _, err := ClassifyGlobalLink(installed.Path, installed.Source, sourceRepo, GlobalLinkFile)
		if err != nil {
			return GlobalAgentsResult{}, fmt.Errorf("re-inspect global agent artifact %s: %w", installed.Path, err)
		}
		if err := ApplyGlobalLink(installed.Path, installed.Source, state); err != nil {
			return GlobalAgentsResult{}, fmt.Errorf("install global agent artifact %s: %w", installed.Path, err)
		}
	}

	return result, nil
}

// compiledAgent is one global agent rendered for both engines. mdContent is
// nil for an original agent (Claude links straight to the clone's .md) and
// carries the rendered bytes for a variant, whose mdSource is its file in the
// generated Claude directory: a variant is the Claude -> Claude step, and from
// there it takes the same link and the same role file as any original.
type compiledAgent struct {
	name        string
	mdSource    string
	mdContent   []byte
	tomlContent []byte
}

// compileGlobalAgents renders every machine-global agent — originals plus the
// variants variants.json declares — and validates that each one's TOML parses
// before any caller touches disk. An emitter that ships an unparseable
// artifact has done nothing useful, so this returns an error rather than a
// partial roster.
func compileGlobalAgents(home, sourceRepo string) ([]compiledAgent, error) {
	agentsDir := filepath.Join(sourceRepo, "templates", "global", "agents")
	sources, err := globSorted(filepath.Join(agentsDir, "*.md"))
	if err != nil {
		return nil, fmt.Errorf("glob %s: %w", agentsDir, err)
	}
	if len(sources) == 0 {
		return nil, fmt.Errorf("no agent .md files in %s", agentsDir)
	}
	type agentSource struct {
		path    string
		content []byte
	}
	agentSources := make([]agentSource, 0, len(sources))
	for _, src := range sources {
		agentSources = append(agentSources, agentSource{path: src})
	}
	variants, err := LoadGlobalAgentVariants(agentsDir, paths.GeneratedClaudeAgentsDir(home))
	if err != nil {
		return nil, err
	}
	for _, variant := range variants {
		agentSources = append(agentSources, agentSource{path: variant.Path, content: variant.Content})
	}
	compiled := make([]compiledAgent, 0, len(agentSources))
	for _, src := range agentSources {
		raw := src.content
		if raw == nil {
			raw, err = os.ReadFile(src.path)
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", src.path, err)
			}
		}
		name, content, err := renderGlobalAgentTOML(src.path, string(raw), agentsDir)
		if err != nil {
			return nil, err
		}
		if parseErr := validateTOML(content); parseErr != nil {
			return nil, fmt.Errorf("%s: does not parse: %w", src.path, parseErr)
		}
		compiled = append(compiled, compiledAgent{
			name:        name,
			mdSource:    src.path,
			mdContent:   src.content,
			tomlContent: []byte(content),
		})
	}
	return compiled, nil
}

// GlobalRole is one machine-global role as THIS binary compiles it: the name
// its registry file takes and the exact bytes that file must hold. The doctor
// reads it to compare disk against the compiler that owns it — a role file is
// correct only when it matches what a reinstall would write, never merely
// because something exists at the path.
type GlobalRole struct {
	Name    string
	Content []byte
}

// CompileGlobalRoles is the read-only half of RunGlobalAgents' compile phase,
// for callers that need the desired bytes without installing anything.
func CompileGlobalRoles(home, sourceRepo string) ([]GlobalRole, error) {
	resolvedHome, err := resolveHome(home)
	if err != nil {
		return nil, err
	}
	compiled, err := compileGlobalAgents(resolvedHome, filepath.Clean(sourceRepo))
	if err != nil {
		return nil, err
	}
	roles := make([]GlobalRole, 0, len(compiled))
	for _, agent := range compiled {
		roles = append(roles, GlobalRole{Name: agent.name, Content: agent.tomlContent})
	}
	return roles, nil
}

func sameGlobalAgentFile(path string, content []byte) (bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect global agent artifact %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return false, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read global agent artifact %s: %w", path, err)
	}
	return bytes.Equal(raw, content), nil
}

func writeGlobalAgentFile(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	info, err := os.Lstat(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("inspect global agent destination %s: %w", path, err)
	}
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove symlink %s: %w", path, err)
		}
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// codexReadOnly reports whether a role compiles to sandbox_mode = "read-only",
// at either tier: read-only unless the Claude tools line grants a write tool,
// the line is absent, or the role's base filename (without .md) is gitter.
func codexReadOnly(tools, baseName string) bool {
	if strings.TrimSpace(tools) == "" || baseName == "gitter" {
		return false
	}
	for _, tool := range strings.Split(tools, ",") {
		switch strings.TrimSpace(tool) {
		case "Write", "Edit", "MultiEdit", "NotebookEdit":
			return false
		}
	}
	return true
}

// renderGlobalAgentTOML compiles one agent's Markdown into the role name and
// the exact TOML bytes its registry file holds. raw is passed in rather than
// read here because a variant's Markdown exists only in memory until the build
// phase writes it; mdPath labels errors and, against agentsDir, names the
// source the generated marker credits.
func renderGlobalAgentTOML(mdPath, raw, agentsDir string) (string, string, error) {
	fields, body, err := parseFrontmatter(raw)
	if err != nil {
		return "", "", fmt.Errorf("parse %s: %w", mdPath, err)
	}
	if len(fields) == 0 {
		return "", "", fmt.Errorf("%s: no frontmatter", mdPath)
	}
	name := strings.TrimSpace(fields["name"])
	description := strings.TrimSpace(fields["description"])
	if name == "" || description == "" {
		return "", "", fmt.Errorf("%s: frontmatter needs both name: and description", mdPath)
	}
	body = strings.TrimSpace(body)

	body = strings.ReplaceAll(body, globalAgentBodyOld, globalAgentBodyNew)

	// A global role never passes through transformMarkdown — this compiler
	// applies the literal substitutions a global role needs and nothing else
	// — so the /code-review rewrite is applied here too, to the description a
	// lead routes on as much as to the body the role runs (review.go). The
	// tier map is the compiler's default: this path loads no project config.
	description = rewriteCodeReview(description, nil)
	body = rewriteCodeReview(body, nil)
	model := strings.TrimSpace(fields["model"])
	if mapped, ok := defaultConfig().ModelMap[model]; ok {
		model = mapped
	}
	effort := strings.TrimSpace(fields["effort"])

	content := globalRoleHeader(globalAgentMarkerSource(mdPath, agentsDir)) +
		"name = \"" + globalAgentEscape(name) + "\"\n" +
		"description = \"" + globalAgentEscape(description) + "\"\n"
	if model != "" {
		content += "model = \"" + globalAgentEscape(model) + "\"\n"
	}
	if effort != "" {
		content += "model_reasoning_effort = \"" + globalAgentEscape(effort) + "\"\n"
	}
	if codexReadOnly(fields["tools"], strings.TrimSuffix(filepath.Base(mdPath), ".md")) {
		content += "sandbox_mode = \"read-only\"\n"
	}
	content += "developer_instructions = \"\"\"\n" + globalAgentEscapeMultiline(body) + "\n\"\"\"\n"

	return name, content, nil
}

// globalAgentMarkerSource names what the generated marker credits: the clone
// path of an original agent, or variants.json for a variant, whose Markdown
// only ever existed in the pfm-owned generated directory. It is deliberately
// clone-relative — a host-absolute path in a marker says nothing an operator
// on another machine can act on.
func globalAgentMarkerSource(mdPath, agentsDir string) string {
	const relDir = "templates/global/agents/"
	if filepath.Dir(mdPath) == filepath.Clean(agentsDir) {
		return relDir + filepath.Base(mdPath)
	}
	return relDir + GlobalAgentVariantsFile
}

// globalAgentEscape is for a TOML basic string — build-codex.mjs:151.
// Only a backslash and a raw quote need neutralising; name/description are
// always a single frontmatter line, so no literal newline ever reaches it.
func globalAgentEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, `"`, `\"`)
}

// globalAgentEscapeMultiline is for a TOML multi-line basic string —
// build-codex.mjs:153. Only a backslash and a literal triple-quote need
// neutralising; TOML tolerates a raw newline inside """ ... """.
func globalAgentEscapeMultiline(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, `"""`, `\"\"\"`)
}

func globSorted(pattern string) ([]string, error) {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	return matches, nil
}
