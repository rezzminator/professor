// Package agentrole is the one resolver for "make this seat BE a registered
// agent role from birth." It reads the constitution a --role seat is born
// having read — the Codex fleet prompt plus the compiled role's
// developer_instructions for a cx seat, the
// .claude/agents/<role>.md body for a cc seat — and returns it as plain text
// for the caller to fold into the launch prompt, or an error naming exactly
// what went wrong.
//
// Everything here runs before spawn.Run. A half-born seat with no
// constitution is the exact failure this package exists to prevent, so every
// failure path returns an error instead of an empty string: an unknown role,
// a directory that could not be read, a role file that could not be parsed,
// and an engine with no compiled artifact for an otherwise-registered role
// are four different errors, not four ways to silently produce "".
package agentrole

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/codexgen"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// RefreshSeatPrompt re-resolves the marker role, atomically rewrites its
// prompt file, and returns the engine-ready prompt channel.
func RefreshSeatPrompt(engine pfmengine.ID, sidDir, socket, pane, cwd, home string) (string, error) {
	role, _, path, found, err := ReadSeatPrompt(sidDir, socket, pane)
	if err != nil || !found {
		return "", err
	}
	constitution, _, err := Resolve(engine, role, cwd, home)
	if err != nil {
		return "", err
	}
	var stagedFleetPrompt string
	if engine == pfmengine.Claude {
		path := action.ProfessorPromptPath(home)
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return "", fmt.Errorf("agent role: read staged Claude prompt %s: %w", path, readErr)
		}
		stagedFleetPrompt = string(raw)
	}
	body, err := ComposeSeatPrompt(engine, role, constitution, stagedFleetPrompt)
	if err != nil {
		return "", err
	}
	if err := writeSeatPromptFile(sidDir, path, body); err != nil {
		return "", err
	}
	if engine == pfmengine.Claude {
		return path, nil
	}
	return constitution, nil
}

// artifactKind is which subdirectory and file extension carry a role's
// constitution for one engine.
type artifactKind struct {
	subdir string
	ext    string
}

// Artifact is exactly which file a role's constitution was read from.
type Artifact struct {
	// Path is the absolute path of the file Resolve actually read.
	Path string
}

func kindFor(engineID pfmengine.ID) (artifactKind, error) {
	switch engineID {
	case pfmengine.Claude:
		return artifactKind{subdir: filepath.Join(".claude", "agents"), ext: ".md"}, nil
	case pfmengine.Codex:
		return artifactKind{subdir: filepath.Join(".codex", "agents"), ext: ".toml"}, nil
	default:
		return artifactKind{}, fmt.Errorf("agent role: engine %q has no registered agent artifact ladder", engineID)
	}
}

// otherEngine is the counterpart engine a role's OTHER artifact would live
// under, used only to give a precise "it exists but was never compiled"
// error instead of a bare "unknown role".
func otherEngine(engineID pfmengine.ID) (pfmengine.ID, bool) {
	switch engineID {
	case pfmengine.Claude:
		return pfmengine.Codex, true
	case pfmengine.Codex:
		return pfmengine.Claude, true
	default:
		return "", false
	}
}

// Resolve returns the constitution text for role, on a seat born with
// engineID, launched from cwd, against host home, alongside the Artifact it
// was read from. cwd is the same directory value runRun already computes
// from --cwd (runDir's return, before any repo walk); home is
// runtime.Paths.Home, the fleet's resolved $HOME.
//
// Resolution never falls back across engines and never merges two partial
// artifacts: the first rung of the ladder that names an existing file wins,
// full stop.
func Resolve(engineID pfmengine.ID, role, cwd, home string) (string, Artifact, error) {
	if strings.TrimSpace(role) == "" {
		return "", Artifact{}, errors.New("agent role: role name is empty")
	}
	kind, err := kindFor(engineID)
	if err != nil {
		return "", Artifact{}, err
	}
	repo, err := repoRoot(cwd)
	if err != nil {
		return "", Artifact{}, err
	}

	// When cwd sits under $HOME with no repo of its own above it, the walk in
	// repoRoot lands on $HOME itself and both rungs name the SAME directory.
	// One directory searched once must report as one rung: an unknown-role
	// message listing the same path twice claims a breadth of search it never
	// had.
	ladder := []string{filepath.Join(repo, kind.subdir)}
	if global := filepath.Join(home, kind.subdir); global != ladder[0] {
		ladder = append(ladder, global)
	}

	var states []dirState
	for _, dir := range ladder {
		path := filepath.Join(dir, role+kind.ext)
		info, statErr := os.Stat(path)
		if statErr == nil && !info.IsDir() {
			text, err := readArtifact(engineID, path)
			if err != nil {
				return "", Artifact{}, err
			}
			absPath, err := filepath.Abs(path)
			if err != nil {
				return "", Artifact{}, fmt.Errorf("agent role: resolve absolute path for %s: %w", path, err)
			}
			return text, Artifact{Path: absPath}, nil
		}
		if statErr != nil && !os.IsNotExist(statErr) {
			return "", Artifact{}, fmt.Errorf("agent role: inspect %s: %w", path, statErr)
		}
		state, err := inspectDir(dir, kind.ext)
		if err != nil {
			return "", Artifact{}, err
		}
		states = append(states, state)
	}

	if other, ok := otherEngine(engineID); ok {
		if hintErr := crossEngineHint(engineID, other, kind, role, repo, home); hintErr != nil {
			return "", Artifact{}, hintErr
		}
	}

	return "", Artifact{}, unknownRoleError(role, engineID, states)
}

// readArtifact reads and validates the constitution once the ladder has
// already found which file it is; it never falls through to the other
// engine's artifact shape.
func readArtifact(engineID pfmengine.ID, path string) (string, error) {
	switch engineID {
	case pfmengine.Claude:
		return readMarkdownConstitution(path)
	case pfmengine.Codex:
		return readTOMLConstitution(path)
	default:
		return "", fmt.Errorf("agent role: engine %q has no registered agent artifact ladder", engineID)
	}
}

func readMarkdownConstitution(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("agent role: read %s: %w", path, err)
	}
	body := stripFrontmatter(string(raw))
	if strings.TrimSpace(body) == "" {
		return "", fmt.Errorf(
			"agent role: %s is empty after its frontmatter — no constitution to launch a seat with",
			path,
		)
	}
	return body, nil
}

// stripFrontmatter removes exactly the "---\n...\n---\n" block a role .md
// opens with. It is not a YAML parser: it only has to find the SECOND "---"
// line, never read a key between them. A file that does not open with a
// "---" line has no frontmatter — the whole file is the constitution. A file
// that opens with "---" but never closes it is left untouched for the same
// reason: there is no well-formed block to strip.
func stripFrontmatter(text string) string {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || lines[0] != "---" {
		return text
	}
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			return strings.Join(lines[i+1:], "\n")
		}
	}
	return text
}

// roleTOML is the one field agentrole reads out of a compiled Codex agent —
// deliberately narrow, because a role's TOML may carry other keys (name,
// description, sandbox_mode) this package has no business validating.
type roleTOML struct {
	DeveloperInstructions string `toml:"developer_instructions"`
}

func readTOMLConstitution(path string) (string, error) {
	var doc roleTOML
	if _, err := toml.DecodeFile(path, &doc); err != nil {
		return "", fmt.Errorf("agent role: parse %s: %w", path, err)
	}
	if strings.TrimSpace(doc.DeveloperInstructions) == "" {
		return "", fmt.Errorf("agent role: %s has an empty or missing developer_instructions key", path)
	}
	// A seat's -c developer_instructions REPLACES the config-level fleet
	// prompt (codex-rs/core/src/agent/role.rs build_next_config), and a
	// compiled role file carries only its own body — so the seat's
	// constitution is the fleet prompt, then the role.
	fleetPrompt, err := codexgen.FleetPrompt()
	if err != nil {
		return "", fmt.Errorf("agent role: compose the Codex fleet prompt for %s: %w", path, err)
	}
	return fleetPrompt + "\n---\n\n" + doc.DeveloperInstructions, nil
}

// repoRoot walks upward from start (inclusive) to the nearest ancestor that
// contains a .claude/agents or a .codex/agents directory — the shared repo
// boundary the whole ladder resolves against, computed once regardless of
// which engine is asking. When no ancestor qualifies, start stands in for
// <repo>, so the ladder's repo-local directory is still a concrete path that
// correctly reports "does not exist" rather than inventing a third state.
func repoRoot(start string) (string, error) {
	dir := start
	for {
		for _, sub := range []string{filepath.Join(".claude", "agents"), filepath.Join(".codex", "agents")} {
			candidate := filepath.Join(dir, sub)
			info, err := os.Stat(candidate)
			if err == nil && info.IsDir() {
				return dir, nil
			}
			if err != nil && !os.IsNotExist(err) {
				return "", fmt.Errorf("agent role: inspect %s: %w", candidate, err)
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return start, nil
		}
		dir = parent
	}
}

// dirState is one ladder rung's search result, kept only for the unknown-role
// report: which directory, whether it exists at all, and which role names it
// offers for this engine's extension.
type dirState struct {
	dir    string
	exists bool
	roles  []string
}

func inspectDir(dir, ext string) (dirState, error) {
	state := dirState{dir: dir}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return state, nil
		}
		return dirState{}, fmt.Errorf("agent role: read %s: %w", dir, err)
	}
	state.exists = true
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if name := entry.Name(); strings.HasSuffix(name, ext) {
			state.roles = append(state.roles, strings.TrimSuffix(name, ext))
		}
	}
	sort.Strings(state.roles)
	return state, nil
}

// crossEngineHint checks the OTHER engine's own ladder for the same role. A
// role that exists as a .claude/agents/<role>.md source but was asked for on
// a cx seat has not failed to exist — it has failed to compile — and gets a
// fix, not a bare "unknown role". The reverse (a role that exists only as a
// compiled .codex/agents/<role>.toml, asked for on a cc seat) has no
// automatic fix to name, so it says what's missing without inventing one.
func crossEngineHint(engineID, other pfmengine.ID, kind artifactKind, role, repo, home string) error {
	otherKind, err := kindFor(other)
	if err != nil {
		return nil
	}
	for _, dir := range []string{filepath.Join(repo, otherKind.subdir), filepath.Join(home, otherKind.subdir)} {
		path := filepath.Join(dir, role+otherKind.ext)
		info, statErr := os.Stat(path)
		if statErr != nil || info.IsDir() {
			continue
		}
		want := filepath.Join(repo, kind.subdir, role+kind.ext)
		if engineID == pfmengine.Codex {
			return fmt.Errorf(
				"agent role %q: found %s but no compiled %s — this seat is cx (codex) and reads the compiled artifact, never the .claude source; run: pfm codex build %s",
				role,
				path,
				want,
				repo,
			)
		}
		return fmt.Errorf(
			"agent role %q: found %s but no %s — this seat is cc (claude) and reads the .claude/agents source directly; this role only exists as a compiled Codex agent",
			role,
			path,
			want,
		)
	}
	return nil
}

// unknownRoleError builds the exit-2 message for a role neither ladder rung
// has for this engine. It lists every role name discoverable in the searched
// directories, names each directory searched, and marks each rung as
// "directory not found" or "directory exists, no roles registered" — those
// two states must never collapse into the same blank list, or an error looks
// like an exhaustive search that found nothing.
func unknownRoleError(role string, engineID pfmengine.ID, states []dirState) error {
	var b strings.Builder
	fmt.Fprintf(&b, "agent role: no %s role %q found", engineID, role)
	all := map[string]bool{}
	for _, state := range states {
		switch {
		case !state.exists:
			fmt.Fprintf(&b, "\n  searched %s: directory not found", state.dir)
		case len(state.roles) == 0:
			fmt.Fprintf(&b, "\n  searched %s: directory exists, no roles registered", state.dir)
		default:
			fmt.Fprintf(&b, "\n  searched %s: %s", state.dir, strings.Join(state.roles, ", "))
			for _, r := range state.roles {
				all[r] = true
			}
		}
	}
	if len(all) == 0 {
		b.WriteString("\navailable roles: none found")
	} else {
		names := make([]string, 0, len(all))
		for r := range all {
			names = append(names, r)
		}
		sort.Strings(names)
		fmt.Fprintf(&b, "\navailable roles: %s", strings.Join(names, ", "))
	}
	return errors.New(b.String())
}
