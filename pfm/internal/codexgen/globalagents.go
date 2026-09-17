package codexgen

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// GlobalAgentsOptions selects the host HOME whose global Codex agents get
// (re)compiled and installed. The source directory is always
// {SourceRepo}/templates/global/agents; installs land at {ClaudeConfigDir}/
// agents for every configured Claude account (a symlink to the raw .md,
// Claude reads it directly) and {Home}/.codex/agents (a symlink to the
// compiled .toml, Codex reads it directly).
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
	Mode             Mode
}

// GlobalAgentCompiled is one desired TOML beside its source .md. Build writes
// changed bytes; check reports the same desired artifact without writing it.
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
	Actions   []GlobalAgentAction
	Problems  []string
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
// every {SourceRepo}/templates/global/agents/*.md into a sibling TOML,
// validates every compiled TOML parses, then SYMLINKS every configured
// Claude agents registry (ClaudeConfigDirs, {Home}/.claude by default) to
// the .md sources and {Home}/.codex/agents to the compiled .toml files —
// updates to the source repo propagate through the link, no reinstall
// required. A regular-file copy already at a desired target (the shape the
// old copy-based installer left behind) is replaced with the link; a
// symlink pointing outside the source repository is a conflict this never
// touches — see ClassifyGlobalLink/ApplyGlobalLink in globallink.go.
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
	agentsDir := filepath.Join(sourceRepo, "templates", "global", "agents")

	sources, err := globSorted(filepath.Join(agentsDir, "*.md"))
	if err != nil {
		return GlobalAgentsResult{}, fmt.Errorf("glob %s: %w", agentsDir, err)
	}
	if len(sources) == 0 {
		return GlobalAgentsResult{}, fmt.Errorf("no agent .md files in %s", agentsDir)
	}

	// Phase 1: compile and validate every TOML twin before touching disk —
	// a failed compile or an unparseable TOML aborts before any install, the
	// same fail-loud order the host script used.
	type compiledAgent struct {
		mdSource    string
		tomlOutput  string
		tomlContent []byte
	}
	compiledAgents := make([]compiledAgent, 0, len(sources))
	for _, src := range sources {
		out, content, err := renderGlobalAgentTOML(src)
		if err != nil {
			return GlobalAgentsResult{}, err
		}
		if parseErr := validateTOML(content); parseErr != nil {
			return GlobalAgentsResult{}, fmt.Errorf("%s: does not parse: %w", out, parseErr)
		}
		if _, err := os.Stat(src); err != nil {
			return GlobalAgentsResult{}, fmt.Errorf("inspect %s: %w", src, err)
		}
		compiledAgents = append(
			compiledAgents,
			compiledAgent{mdSource: src, tomlOutput: out, tomlContent: []byte(content)},
		)
	}

	// One Claude agents/ registry per configured account; an installer that
	// never names its accounts keeps the single {Home}/.claude registry.
	claudeConfigDirs := options.ClaudeConfigDirs
	if len(claudeConfigDirs) == 0 {
		claudeConfigDirs = []string{filepath.Join(home, ".claude")}
	}

	type desiredLink struct {
		target string
		source string
	}
	result := GlobalAgentsResult{}
	links := make([]desiredLink, 0, len(compiledAgents)*2)
	for _, agent := range compiledAgents {
		result.Compiled = append(
			result.Compiled,
			GlobalAgentCompiled{Path: agent.tomlOutput, Size: int64(len(agent.tomlContent))},
		)
		same, err := sameGlobalAgentFile(agent.tomlOutput, agent.tomlContent)
		if err != nil {
			return GlobalAgentsResult{}, err
		}
		if !same {
			result.Actions = append(result.Actions, GlobalAgentAction{Kind: actionWrite, Path: agent.tomlOutput})
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
		links = append(
			links,
			desiredLink{
				target: filepath.Join(home, ".codex", "agents", filepath.Base(agent.tomlOutput)),
				source: agent.tomlOutput,
			},
		)
	}

	// Phase 2: classify every desired registry symlink against what is
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
		same, err := sameGlobalAgentFile(agent.tomlOutput, agent.tomlContent)
		if err != nil {
			return GlobalAgentsResult{}, err
		}
		if same {
			continue
		}
		if err := writeGlobalAgentFile(agent.tomlOutput, agent.tomlContent); err != nil {
			return GlobalAgentsResult{}, err
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

func renderGlobalAgentTOML(mdPath string) (string, string, error) {
	raw, err := os.ReadFile(mdPath)
	if err != nil {
		return "", "", fmt.Errorf("read %s: %w", mdPath, err)
	}
	fields, body, err := parseFrontmatter(string(raw))
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

	content := "name = \"" + globalAgentEscape(name) + "\"\n" +
		"description = \"" + globalAgentEscape(description) + "\"\n" +
		"developer_instructions = \"\"\"\n" + globalAgentEscapeMultiline(body) + "\n\"\"\"\n"

	out := filepath.Join(filepath.Dir(mdPath), name+".toml")
	return out, content, nil
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
