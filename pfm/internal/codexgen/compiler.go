package codexgen

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Mode selects whether the reconciler may change the filesystem.
type Mode uint8

const (
	ModeBuild Mode = iota
	ModeCheck
	frontmatterFence = "---"
)

// Options is deliberately filesystem-only: the compiler has no process,
// network, or live-editor dependencies.
type Options struct {
	Root         string
	Home         string
	Mode         Mode
	CLIOverrides CLIOverrides
}

type Result struct {
	OK       bool
	Warnings []string
	Problems []string
	// Dangling records sources that vanished, as a TYPED condition rather than
	// a substring of rendered prose. Build reports these as warnings and still
	// writes; Check promotes them to Problems. Classifying by scanning warning
	// TEXT for "dangling" is a coincidence detector: rewording a message
	// silently disarms the gate, and an unrelated warning that happens to carry
	// the word arms it against nothing.
	Dangling  []string
	Wrote     int
	Unchanged int
	Deleted   int
	Actions   []Action
}

// danglingSource records a source that could not be resolved: a warning for
// every mode, plus the typed entry Check gates on.
func (result *Result) danglingSource(path string, err error) {
	message := fmt.Sprintf("dangling source %s: %v", path, err)
	result.Warnings = append(result.Warnings, message)
	result.Dangling = append(result.Dangling, message)
}

// Action is one filesystem mutation a build performs. Check returns the same
// list without writing, while Problems retains its existing drift diagnostics.
type Action struct {
	Kind   string
	Path   string
	Target string
}

// GlobalCommandsOptions selects the host HOME whose installed Claude
// commands are mirrored into the global Codex prompt and skill registries.
// Unlike Options, it deliberately has no repository root: host install and
// uninstall must not compile or reconcile project files as a side effect.
type GlobalCommandsOptions struct {
	Home       string
	SourceHome string
	Mode       Mode
}

const defaultAgentPreamble = "You are the ${name} role in this repository, running as a native Codex subagent.\nFirst action: read the repository root AGENTS.md in full. Follow its laws and the protocol below exactly; your mode and task come from the dispatch prompt.\n\n"

const defaultRootAdapter = `
## Codex adapter — reading this file in the Codex harness

This file is compiled from CLAUDE.md by pfm codex build; model aliases are already mapped to Codex models. Translate harness mechanics to the matching Codex command or role, and treat generated files as read-only: edit CLAUDE.md and rebuild.
`

// Run discovers the incumbent Claude source tree, transforms it into Codex
// artifacts, and reconciles only outputs carrying this compiler's marker.
// Build writes each file atomically; check computes the same plan and writes
// nothing.
func Run(options Options) (Result, error) {
	root, err := resolveRoot(options.Root)
	if err != nil {
		return Result{}, err
	}
	home, err := resolveHome(options.Home)
	if err != nil {
		return Result{}, err
	}
	cli := options.CLIOverrides
	cfg, err := loadConfig(root, cli)
	if err != nil {
		return Result{}, err
	}
	result := Result{Warnings: []string{}, Problems: []string{}, Dangling: []string{}}
	outputs := make([]generatedFile, 0)
	add := func(output generatedFile) { outputs = append(outputs, output) }
	warn := func(message string) { result.Warnings = append(result.Warnings, message) }
	problem := func(message string) { result.Problems = append(result.Problems, message) }

	projects := discoverProjects(root, cfg, &result)
	roster := discoverCommandRoster(root, cfg, &result)
	transform := TransformOptions{ModelMap: cfg.ModelMap, Commands: roster}
	agentsDocumentTransform := transform
	agentsDocumentTransform.ReplaceClaudeFile = true

	for _, project := range projects {
		src := filepath.Join(root, project, "CLAUDE.md")
		content, ok := compileSourceMarkdown(src, agentsDocumentTransform, problem)
		if !ok {
			continue
		}
		if project == "." {
			content += cfg.RootAdapter
		}
		rel, _ := filepath.Rel(root, src)
		add(
			generatedFile{
				Path:    filepath.Join(root, project, "AGENTS.md"),
				Content: generatedHeader(filepath.ToSlash(rel)) + "\n" + content,
			},
		)
	}

	compileAgents(root, projects, cfg, transform, add, problem, warn, &result)
	compileRepoCommands(root, cfg, transform, roster, add, problem, warn, &result)
	compileRepoSkills(root, add, problem, &result)
	if cfg.GlobalCommands {
		compileInstalledGlobalCommands(home, home, add, problem, warn, &result)
	}

	mcp, mcpErr := compileMCP(root)
	if mcpErr != nil {
		problem(mcpErr.Error())
	} else {
		if len(mcp.Problems) == 0 {
			if _, statErr := os.Stat(filepath.Join(root, ".mcp.json")); statErr == nil || mcp.Content != "" {
				add(
					generatedFile{
						Path:         filepath.Join(root, ".codex", "config.toml"),
						Content:      mcp.Content,
						ManagedFence: true,
					},
				)
			}
		}
		result.Warnings = append(result.Warnings, mcp.Notes...)
		result.Problems = append(result.Problems, mcp.Problems...)
	}
	if options.Mode == ModeCheck {
		for _, entry := range result.Dangling {
			result.Problems = append(
				result.Problems,
				entry+" — retire the stale link; `pfm install` prunes an orphaned global-command link automatically",
			)
		}
	}

	// The compiler never writes invalid generated TOML. Check additionally
	// validates every existing repository/global TOML artifact Codex can load.
	if options.Mode == ModeCheck {
		tomls, tomlWalkProblems := tomlPaths(root, home)
		result.Problems = append(result.Problems, tomlWalkProblems...)
		for _, path := range tomls {
			if data, readErr := os.ReadFile(path); readErr == nil {
				if parseErr := validateTOML(string(data)); parseErr != nil {
					problem(fmt.Sprintf("UNPARSEABLE %s: %v", path, parseErr))
				}
			}
		}
	}
	for _, output := range outputs {
		if strings.HasSuffix(output.Path, ".toml") {
			if parseErr := validateTOML(output.Content); parseErr != nil {
				problem(fmt.Sprintf("UNPARSEABLE generated %s: %v", output.Path, parseErr))
			}
		}
	}
	// A failed compile is a preflight failure. Build must not materialize a
	// partial mirror after an override/config/source error.
	if options.Mode == ModeBuild && len(result.Problems) != 0 {
		result.OK = false
		return result, nil
	}
	reconciled, reconcileErr := reconcile(root, home, outputs, options.Mode, cfg.GlobalCommands)
	if reconcileErr != nil {
		return Result{}, reconcileErr
	}
	result.Warnings = append(result.Warnings, reconciled.Warnings...)
	result.Problems = append(result.Problems, reconciled.Problems...)
	result.Wrote = reconciled.Wrote
	result.Unchanged = reconciled.Unchanged
	result.Deleted = reconciled.Deleted
	result.Actions = reconciled.Actions
	result.OK = len(result.Problems) == 0
	return result, nil
}

// RunGlobalCommands compiles and reconciles only $HOME/.claude/commands into
// $HOME/.codex/{prompts,skills}. Check is read-only; build replaces or deletes
// only artifacts carrying the compiler marker and preserves every unmarked
// conflict.
func RunGlobalCommands(options GlobalCommandsOptions) (Result, error) {
	home, err := resolveHome(options.Home)
	if err != nil {
		return Result{}, err
	}
	sourceHome := home
	if options.SourceHome != "" {
		sourceHome, err = resolveHome(options.SourceHome)
		if err != nil {
			return Result{}, err
		}
	}
	result := Result{Warnings: []string{}, Problems: []string{}, Dangling: []string{}}
	outputs := make([]generatedFile, 0)
	add := func(output generatedFile) { outputs = append(outputs, output) }
	warn := func(message string) { result.Warnings = append(result.Warnings, message) }
	problem := func(message string) { result.Problems = append(result.Problems, message) }
	compileInstalledGlobalCommands(sourceHome, home, add, problem, warn, &result)

	if options.Mode == ModeCheck {
		for _, entry := range result.Dangling {
			result.Problems = append(
				result.Problems,
				entry+" — retire the stale link; `pfm install` prunes an orphaned global-command link automatically",
			)
		}
	}
	if options.Mode == ModeBuild && len(result.Problems) != 0 {
		result.OK = false
		return result, nil
	}
	reconciled, err := reconcileManagedWithClaim(outputs, options.Mode, []string{
		filepath.Join(home, ".codex", "skills"),
		filepath.Join(home, ".codex", "prompts"),
	}, markerClaimable(home))
	if err != nil {
		return Result{}, err
	}
	result.Warnings = append(result.Warnings, reconciled.Warnings...)
	result.Problems = append(result.Problems, reconciled.Problems...)
	result.Wrote = reconciled.Wrote
	result.Unchanged = reconciled.Unchanged
	result.Deleted = reconciled.Deleted
	result.Actions = reconciled.Actions
	result.OK = len(result.Problems) == 0
	return result, nil
}

// Build and Check are explicit API entry points for callers that do not want
// to construct a Mode value.  Both retain the same deterministic plan.
func Build(options Options) (Result, error) {
	options.Mode = ModeBuild
	return Run(options)
}

func Check(options Options) (Result, error) {
	options.Mode = ModeCheck
	return Run(options)
}

func resolveRoot(root string) (string, error) {
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve repository root: %w", err)
		}
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve repository root: %w", err)
	}
	return filepath.Clean(root), nil
}

func resolveHome(home string) (string, error) {
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home: %w", err)
		}
	}
	home, err := filepath.Abs(home)
	if err != nil {
		return "", fmt.Errorf("resolve home: %w", err)
	}
	return filepath.Clean(home), nil
}

func generatedHeader(source string) string {
	return "<!-- " + generatedLine(source) + " -->"
}

func generatedLine(source string) string {
	return generatedMarker + " from " + source + "; do not edit — edit the source, then re-run: pfm codex build"
}

func discoverProjects(root string, cfg Config, result *Result) []string {
	projects := []string{"."}
	if cfg.Projects != nil {
		for _, project := range cfg.Projects {
			if project != "." && hasClaude(filepath.Join(root, project)) {
				projects = append(projects, project)
			}
		}
	} else if entries, err := os.ReadDir(root); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() || excluded(cfg.ExcludeProjects, entry.Name()) || entry.Name() == ".claude" ||
				entry.Name() == ".codex" {
				continue
			}
			if hasClaude(filepath.Join(root, entry.Name())) {
				projects = append(projects, entry.Name())
			}
		}
	} else {
		result.Problems = append(result.Problems, fmt.Sprintf("read repository root %s: %v", root, err))
	}
	sort.Strings(projects[1:])
	return projects
}

func hasClaude(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "CLAUDE.md"))
	return err == nil && info.Mode().IsRegular()
}

func excluded(exclusions []string, name string) bool {
	for _, excludedName := range exclusions {
		if filepath.ToSlash(excludedName) == name {
			return true
		}
	}
	return false
}

func discoverCommandRoster(root string, cfg Config, result *Result) map[string]string {
	return discoverCommandRosterIn(filepath.Join(root, ".claude", "commands"), cfg.ExcludeDirs, result)
}

func discoverCommandRosterIn(sourceRoot string, exclusions []string, result *Result) map[string]string {
	roster := map[string]string{}
	entries := discoverMarkdown(sourceRoot, exclusions, result)
	for _, entry := range entries {
		rel := entry.rel
		if !entry.skillDir {
			var err error
			rel, err = filepath.Rel(sourceRoot, entry.path)
			if err != nil {
				continue
			}
		}
		name := colonName(filepath.ToSlash(rel))
		roster[name] = "$" + flatName(filepath.ToSlash(rel))
	}
	return roster
}

type sourceEntry struct {
	path     string
	rel      string
	skillDir bool
}

func discoverMarkdown(dir string, excludes []string, result *Result) []sourceEntry {
	entries := []sourceEntry{}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return entries
	} else if err != nil {
		result.Problems = append(result.Problems, fmt.Sprintf("inspect %s: %v", dir, err))
		return entries
	}
	visited := map[string]bool{}
	var walk func(string, string)
	walk = func(current, relative string) {
		resolved, evalErr := filepath.EvalSymlinks(current)
		if evalErr != nil {
			result.danglingSource(current, evalErr)
			return
		}
		if visited[resolved] {
			result.Warnings = append(result.Warnings, "skip cyclic command directory "+current)
			return
		}
		visited[resolved] = true
		read, err := os.ReadDir(current)
		if err != nil {
			result.Problems = append(result.Problems, fmt.Sprintf("read %s: %v", current, err))
			return
		}
		for _, item := range read {
			name := item.Name()
			rel := name
			if relative != "" {
				rel = filepath.Join(relative, name)
			}
			if excludedSource(excludes, rel, name) {
				continue
			}
			path := filepath.Join(current, name)
			info, statErr := os.Stat(path)
			if item.Type()&os.ModeSymlink != 0 {
				if statErr != nil {
					result.danglingSource(path, statErr)
					continue
				}
			}
			if statErr != nil {
				result.Problems = append(result.Problems, fmt.Sprintf("stat %s: %v", path, statErr))
				continue
			}
			if info.IsDir() {
				walk(path, rel)
				continue
			}
			if name == "SKILL.md" {
				entries = append(entries, sourceEntry{path: filepath.Dir(path), rel: filepath.Dir(rel), skillDir: true})
				continue
			}
			if strings.HasSuffix(name, ".md") && name != "README.md" {
				entries = append(entries, sourceEntry{path: path, rel: rel})
			}
		}
	}
	walk(dir, "")
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })
	return entries
}

func excludedSource(exclusions []string, relative, name string) bool {
	relative = filepath.ToSlash(relative)
	for _, value := range exclusions {
		value = filepath.ToSlash(value)
		if value == name || value == relative || strings.HasPrefix(relative, value+"/") {
			return true
		}
	}
	return false
}

func compileSourceMarkdown(source string, options TransformOptions, problem func(string)) (string, bool) {
	raw, err := os.ReadFile(source)
	if err != nil {
		problem(fmt.Sprintf("read %s: %v", source, err))
		return "", false
	}
	return transformMarkdown(string(raw), options), true
}

func compileAgents(
	root string,
	projects []string,
	cfg Config,
	options TransformOptions,
	add func(generatedFile),
	problem, warn func(string),
	result *Result,
) {
	seen := map[string]bool{}
	for _, project := range projects {
		dir := filepath.Join(root, project, ".claude", "agents")
		for _, entry := range discoverMarkdown(dir, nil, result) {
			if entry.skillDir {
				continue
			}
			name := strings.TrimSuffix(filepath.Base(entry.path), ".md")
			if project != "." {
				suffix := project
				switch cfg.SuffixMode {
				case suffixModeNone:
					suffix = ""
				case suffixModeStripPrefix:
					suffix = strings.TrimPrefix(project, cfg.SuffixPrefix)
				}
				if suffix == "" && cfg.SuffixMode != suffixModeNone {
					problem(
						fmt.Sprintf("project %s has an empty agent suffix under %s policy", project, cfg.SuffixMode),
					)
					continue
				}
				if suffix != "" {
					name += "-" + suffix
				}
			}
			if contains(cfg.NeverRegister, strings.TrimSuffix(filepath.Base(entry.path), ".md")) ||
				contains(cfg.NeverRegister, name) {
				warn("skip agent " + entry.path + " — never-register policy")
				continue
			}
			if seen[name] {
				warn("skip agent " + entry.path + " — name already registered")
				continue
			}
			seen[name] = true
			raw, err := os.ReadFile(entry.path)
			if err != nil {
				problem(fmt.Sprintf("read %s: %v", entry.path, err))
				continue
			}
			rel, _ := filepath.Rel(root, entry.path)
			content := string(raw)
			fields, body, parseErr := parseFrontmatter(content)
			if parseErr != nil {
				problem(fmt.Sprintf("parse %s: %v", entry.path, parseErr))
				continue
			}
			modelAlias := fields["model"]
			model := modelAlias
			if mapped, ok := cfg.ModelMap[modelAlias]; ok {
				model = mapped
			}
			tomlName := strings.ReplaceAll(name, "-", "_")
			// Read-only unless the Claude tools grant a write tool, the line is absent, or it is a (suffixed) gitter.
			readOnly := strings.TrimSpace(fields["tools"]) != "" &&
				strings.TrimSuffix(filepath.Base(entry.path), ".md") != "gitter"
			for _, tool := range strings.Split(fields["tools"], ",") {
				tool = strings.TrimSpace(tool)
				if tool == "Write" || tool == "Edit" || tool == "MultiEdit" || tool == "NotebookEdit" {
					readOnly = false
				}
			}
			instructions := strings.ReplaceAll(cfg.AgentPreamble, "${name}", tomlName)
			instructions += transformMarkdown(strings.TrimSpace(body), options)
			toml := "# " + generatedLine(filepath.ToSlash(rel)) + "\n"
			if model != "" {
				toml += "# tier: " + tomlEscape(model)
				if modelAlias != "" && modelAlias != model {
					toml += " (Claude alias: " + tomlEscape(modelAlias) + ")"
				}
				toml += "\n"
			}
			description := transformMarkdown(fields["description"], options)
			toml += "name = " + tomlString(tomlName) + "\ndescription = " + tomlString(description) + "\n"
			if model != "" {
				toml += "model = " + tomlString(model) + "\n"
			}
			if effort := strings.TrimSpace(fields["effort"]); effort != "" {
				toml += "model_reasoning_effort = " + tomlString(effort) + "\n"
			}
			if readOnly {
				toml += "sandbox_mode = \"read-only\"\n"
			}
			toml += "developer_instructions = \"\"\"\n" + tomlMultiline(instructions) + "\"\"\"\n"
			add(generatedFile{Path: filepath.Join(root, ".codex", "agents", name+".toml"), Content: toml})
		}
	}
}

func compileRepoCommands(
	root string,
	cfg Config,
	options TransformOptions,
	_ map[string]string,
	add func(generatedFile),
	problem, warn func(string),
	result *Result,
) {
	sourceRoot := filepath.Join(root, ".claude", "commands")
	for _, entry := range discoverMarkdown(sourceRoot, cfg.ExcludeDirs, result) {
		if entry.skillDir {
			dst := filepath.Join(root, ".codex", "skills", flatName(filepath.ToSlash(entry.rel)))
			add(generatedFile{Path: dst, Link: relativeLink(dst, entry.path)})
			continue
		}
		compileCommandFile(
			root,
			sourceRoot,
			entry,
			".claude/commands",
			filepath.Join(root, ".codex", "skills"),
			options,
			add,
			problem,
			warn,
			result,
			false,
		)
	}
}

func compileGlobalCommands(
	_, sourceHome, outputHome string,
	_ Config,
	options TransformOptions,
	add func(generatedFile),
	problem, warn func(string),
	result *Result,
) {
	sourceRoot := filepath.Join(sourceHome, ".claude", "commands")
	for _, entry := range discoverMarkdown(sourceRoot, nil, result) {
		if entry.skillDir {
			dst := filepath.Join(outputHome, ".codex", "skills", flatName(filepath.ToSlash(entry.rel)))
			link := entry.path
			if sourceHome != outputHome {
				link = filepath.Join(outputHome, ".claude", "commands", entry.rel)
			}
			add(generatedFile{Path: dst, Link: link})
			continue
		}
		compileCommandFile(
			outputHome,
			sourceRoot,
			entry,
			"$HOME/.claude/commands",
			"",
			options,
			add,
			problem,
			warn,
			result,
			true,
		)
	}
}

// compileInstalledGlobalCommands is the single planner for installer-owned
// host commands. Full repository build/check and install reconciliation must
// use the same source roster and defaults or they can disagree about the
// bytes under $HOME/.codex immediately after a successful install.
func compileInstalledGlobalCommands(
	sourceHome, outputHome string,
	add func(generatedFile),
	problem, warn func(string),
	result *Result,
) {
	cfg := defaultConfig()
	sourceRoot := filepath.Join(sourceHome, ".claude", "commands")
	roster := discoverCommandRosterIn(sourceRoot, nil, result)
	transform := TransformOptions{ModelMap: cfg.ModelMap, Commands: roster}
	compileGlobalCommands(outputHome, sourceHome, outputHome, cfg, transform, add, problem, warn, result)
}

func compileCommandFile(
	outputBase, sourceRoot string,
	entry sourceEntry,
	label, outputRoot string,
	options TransformOptions,
	add func(generatedFile),
	problem, _ func(string),
	result *Result,
	global bool,
) {
	raw, err := os.ReadFile(entry.path)
	if err != nil {
		if os.IsNotExist(err) {
			result.danglingSource(entry.path, err)
		} else {
			problem(fmt.Sprintf("read %s: %v", entry.path, err))
		}
		return
	}
	rel, _ := filepath.Rel(sourceRoot, entry.path)
	rel = filepath.ToSlash(rel)
	relSource := filepath.ToSlash(filepath.Join(label, rel))
	content := string(raw)
	fields, body, parseErr := parseFrontmatter(content)
	if parseErr != nil {
		problem(fmt.Sprintf("parse %s: %v", entry.path, parseErr))
		return
	}
	flat := flatName(filepath.ToSlash(entry.rel))
	fm := frontmatterLines(content)
	_ = fields
	// The incumbent compilers only rewrite command references in command
	// frontmatter. Model names there are descriptive metadata, not executable
	// model selections; rewriting them changes user-facing skill descriptions.
	// A /code-review written there IS a command reference, and a Codex seat
	// reading the Claude spelling reviews the whole branch (review.go).
	generated := "---\n# " + generatedLine(
		relSource,
	) + "\nname: " + flat + "\n" + rewriteCodeReviewFrontmatter(
		swapCommands(strings.Join(fm, "\n"), options.Commands),
		options.ModelMap,
	) + "\n---\n" + transformMarkdown(
		body,
		options,
	)
	dst := filepath.Join(outputRoot, flat, "SKILL.md")
	if global {
		add(generatedFile{Path: filepath.Join(outputBase, ".codex", "prompts", flat+".md"), Content: generated})
		if fields["disable-model-invocation"] == "true" || mcpBackedGlobalChatSkill(flat) {
			return
		}
		dst = filepath.Join(outputBase, ".codex", "skills", flat, "SKILL.md")
	}
	add(generatedFile{Path: dst, Content: generated})
}

// MCP-backed chat operations do not need a second model-invocable global
// skill. Keep prompt cards for explicit operator invocation and keep
// chat-interrogate as the one workflow deliberately not exposed by MCP.
func mcpBackedGlobalChatSkill(name string) bool {
	return strings.HasPrefix(name, "chat-") && name != "chat-interrogate"
}

func frontmatterLines(text string) []string {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || lines[0] != frontmatterFence {
		return nil
	}
	for i := 1; i < len(lines); i++ {
		if lines[i] == frontmatterFence {
			out := make([]string, 0, i-1)
			for _, line := range lines[1:i] {
				if !strings.HasPrefix(line, "name:") {
					out = append(out, line)
				}
			}
			return out
		}
	}
	return nil
}

func compileRepoSkills(root string, add func(generatedFile), problem func(string), result *Result) {
	dir := filepath.Join(root, ".claude", "skills")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return
	} else if err != nil {
		problem(fmt.Sprintf("read %s: %v", dir, err))
		return
	}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		info, statErr := os.Stat(path)
		if statErr != nil {
			result.danglingSource(path, statErr)
			continue
		}
		if !info.IsDir() {
			continue
		}
		dst := filepath.Join(root, ".codex", "skills", entry.Name())
		add(generatedFile{Path: dst, Link: relativeLink(dst, path)})
	}
}

func relativeLink(dst, source string) string {
	rel, err := filepath.Rel(filepath.Dir(dst), source)
	if err != nil {
		return source
	}
	return rel
}

func tomlPaths(root, home string) ([]string, []string) {
	paths := []string{}
	problems := []string{}
	seen := map[string]bool{}
	for _, directory := range []string{filepath.Join(root, ".codex"), filepath.Join(home, ".codex")} {
		directory = filepath.Clean(directory)
		if seen[directory] {
			continue
		}
		seen[directory] = true
		err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return nil
			}
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".toml") {
				paths = append(paths, path)
			}
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			problems = append(problems, fmt.Sprintf("inspect TOML artifacts under %s: %v", directory, err))
		}
	}
	sort.Strings(paths)
	return paths, problems
}

func contains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func flatName(rel string) string {
	rel = strings.TrimSuffix(filepath.ToSlash(rel), ".md")
	return strings.ReplaceAll(rel, "/", "-")
}

func colonName(rel string) string {
	rel = strings.TrimSuffix(filepath.ToSlash(rel), ".md")
	return strings.ReplaceAll(rel, "/", ":")
}
