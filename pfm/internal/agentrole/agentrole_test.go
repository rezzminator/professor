package agentrole

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// mustMkdir and mustWrite are the two filesystem primitives every test below
// builds its jail out of. Every directory lives under t.TempDir(): never the
// real fleet, never $HOME, never this repo.
func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	mustMkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// Test 1 (brief numbering) — a .claude/agents/<role>.md with frontmatter
// resolves to exactly the body after the closing "---", byte-for-byte, with
// the frontmatter itself absent. This is the dev agent's own named gap: this
// path was never exercised by a live run before this test.
func TestResolveClaudeSuccessReturnsBodyAfterFrontmatter(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	mustWrite(t, filepath.Join(repo, ".claude", "agents", "reviewer.md"),
		"---\nname: reviewer\ndescription: reads diffs\n---\nBody line one.\nBody line two.\n")

	got, _, err := Resolve(pfmengine.Claude, "reviewer", repo, home)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	want := "Body line one.\nBody line two.\n"
	if got != want {
		t.Fatalf("Resolve() = %q, want %q", got, want)
	}
	if strings.Contains(got, "---") || strings.Contains(got, "name: reviewer") {
		t.Fatalf("Resolve() = %q, frontmatter leaked into the constitution", got)
	}
}

// Test 2 — a .codex/agents/<role>.toml resolves to exactly its
// developer_instructions value, byte-for-byte, and the other keys never leak
// into it.
func TestResolveCodexSuccessReturnsDeveloperInstructions(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	mustWrite(t, filepath.Join(repo, ".codex", "agents", "reviewer.toml"),
		"name = \"reviewer\"\n"+
			"description = \"reads diffs\"\n"+
			"developer_instructions = \"\"\"\n"+
			"Body line one.\n"+
			"Body line two.\n"+
			"\"\"\"\n")

	got, _, err := Resolve(pfmengine.Codex, "reviewer", repo, home)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	want := "Body line one.\nBody line two.\n"
	if got != want {
		t.Fatalf("Resolve() = %q, want %q", got, want)
	}
	if strings.Contains(got, "name") || strings.Contains(got, "description") ||
		strings.Contains(got, "reads diffs") {
		t.Fatalf("Resolve() = %q, want no leakage of the name/description keys", got)
	}
}

// Test 4 — stripFrontmatter's three shapes: a normal "---"/"---" block; a
// file that never opens one (the whole file is the constitution); and a file
// that opens one but never closes it (left untouched — there is no
// well-formed block to strip).
func TestStripFrontmatterThreeShapes(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "normal frontmatter is removed",
			input: "---\nname: x\n---\nBODY\n",
			want:  "BODY\n",
		},
		{
			name:  "no opening --- leaves the whole file",
			input: "BODY only\nsecond line\n",
			want:  "BODY only\nsecond line\n",
		},
		{
			name:  "opens but never closes is left untouched",
			input: "---\nname: x\nBODY\n",
			want:  "---\nname: x\nBODY\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := stripFrontmatter(test.input); got != test.want {
				t.Fatalf("stripFrontmatter(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

// Test 5 — an unknown role must render THREE distinguishable directory
// states: a rung that does not exist, a rung that exists with no roles
// registered, and a rung that lists roles. A search that found nothing
// anywhere must still say so explicitly rather than printing a bare empty
// list. This is the repo's absence-vs-error law at a visible surface.
func TestUnknownRoleReportsDistinctDirectoryStates(t *testing.T) {
	// pinRepoBoundary anchors repoRoot(repo) at repo itself via a
	// .codex/agents marker, WITHOUT creating repo/.claude/agents — so the
	// cc-side rung can independently be "not found", "empty", or "listing"
	// while repoRoot still resolves deterministically to repo.
	pinRepoBoundary := func(t *testing.T, repo string) {
		t.Helper()
		mustMkdir(t, filepath.Join(repo, ".codex", "agents"))
	}

	t.Run("neither rung exists anywhere", func(t *testing.T) {
		repo := t.TempDir()
		home := t.TempDir()
		pinRepoBoundary(t, repo)

		_, _, err := Resolve(pfmengine.Claude, "ghost", repo, home)
		if err == nil {
			t.Fatal("Resolve() returned nil error for an unregistered role")
		}
		msg := err.Error()
		repoDir := filepath.Join(repo, ".claude", "agents")
		homeDir := filepath.Join(home, ".claude", "agents")
		if !strings.Contains(msg, repoDir+": directory not found") {
			t.Fatalf("error = %q, want %q marked as directory not found", msg, repoDir)
		}
		if !strings.Contains(msg, homeDir+": directory not found") {
			t.Fatalf("error = %q, want %q marked as directory not found", msg, homeDir)
		}
		if !strings.Contains(msg, "available roles: none found") {
			t.Fatalf("error = %q, want an EXPLICIT \"none found\" rather than a bare empty list", msg)
		}
	})

	t.Run("repo rung exists with no roles registered", func(t *testing.T) {
		repo := t.TempDir()
		home := t.TempDir()
		pinRepoBoundary(t, repo)
		mustMkdir(t, filepath.Join(repo, ".claude", "agents"))

		_, _, err := Resolve(pfmengine.Claude, "ghost", repo, home)
		if err == nil {
			t.Fatal("Resolve() returned nil error for an unregistered role")
		}
		msg := err.Error()
		repoDir := filepath.Join(repo, ".claude", "agents")
		if !strings.Contains(msg, repoDir+": directory exists, no roles registered") {
			t.Fatalf("error = %q, want %q marked exists-but-empty, distinct from \"not found\"", msg, repoDir)
		}
		if strings.Contains(msg, repoDir+": directory not found") {
			t.Fatalf("error = %q, an existing empty directory must not render as \"not found\"", msg)
		}
	})

	t.Run("repo rung lists the roles it has", func(t *testing.T) {
		repo := t.TempDir()
		home := t.TempDir()
		pinRepoBoundary(t, repo)
		mustWrite(t, filepath.Join(repo, ".claude", "agents", "alpha.md"), "---\n---\nalpha body\n")
		mustWrite(t, filepath.Join(repo, ".claude", "agents", "beta.md"), "---\n---\nbeta body\n")

		_, _, err := Resolve(pfmengine.Claude, "ghost", repo, home)
		if err == nil {
			t.Fatal("Resolve() returned nil error for an unregistered role")
		}
		msg := err.Error()
		repoDir := filepath.Join(repo, ".claude", "agents")
		if !strings.Contains(msg, repoDir+": alpha, beta") {
			t.Fatalf("error = %q, want the repo rung to list its registered roles", msg)
		}
		if !strings.Contains(msg, "available roles: alpha, beta") {
			t.Fatalf("error = %q, want a combined available-roles summary", msg)
		}
	})
}

// Test 6 — ladder dedupe. When repoRoot(cwd) resolves to the same directory
// as home, the unknown-role message must list that directory ONCE, not
// twice: one directory searched once must not claim a breadth of search it
// never had.
//
// This test is proved load-bearing separately: reverting the `ladder`
// dedupe guard in Resolve to the old two-element literal makes it FAIL (see
// the red-then-green evidence in the report this test file shipped with).
func TestLadderDedupeSameDirectoryListedOnce(t *testing.T) {
	// home == cwd, and NEITHER has a .claude/agents or .codex/agents marker
	// anywhere above it (t.TempDir() is a fresh leaf), so repoRoot(home)
	// falls through to "start" — home itself. Both ladder rungs would
	// therefore name the exact same directory without the guard.
	home := t.TempDir()

	_, _, err := Resolve(pfmengine.Claude, "ghost", home, home)
	if err == nil {
		t.Fatal("Resolve() returned nil error for an unregistered role")
	}
	msg := err.Error()
	dir := filepath.Join(home, ".claude", "agents")
	needle := "searched " + dir + ":"
	if count := strings.Count(msg, needle); count != 1 {
		t.Fatalf("error = %q, directory %q appears %d times, want exactly 1", msg, dir, count)
	}
}

// Test 7 — an unreadable directory is an ERROR, not an absence. A rung pfm
// cannot even look inside must never render as "unregistered": that is a
// silent skip wearing the same face as a role that was never created.
//
// Skipped under root, which ignores permission bits entirely — a silently
// passing permission test here would be worse than none.
func TestUnreadableDirectoryIsAnError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits are ignored, so chmod 000 proves nothing here")
	}
	repo := t.TempDir()
	home := t.TempDir()
	agentsDir := filepath.Join(repo, ".claude", "agents")
	mustMkdir(t, agentsDir)
	if err := os.Chmod(agentsDir, 0o000); err != nil {
		t.Fatalf("chmod %s: %v", agentsDir, err)
	}
	t.Cleanup(func() {
		// Restore so t.TempDir()'s own cleanup can remove the directory.
		_ = os.Chmod(agentsDir, 0o700)
	})

	got, _, err := Resolve(pfmengine.Claude, "ghost", repo, home)
	if err == nil {
		t.Fatalf("Resolve() = %q, nil error; want an error naming the unreadable path", got)
	}
	if got != "" {
		t.Fatalf("Resolve() returned text %q alongside an error", got)
	}
	wantPath := filepath.Join(agentsDir, "ghost.md")
	if !strings.Contains(err.Error(), wantPath) {
		t.Fatalf("error = %q, want it to name the unreadable path %q", err.Error(), wantPath)
	}
	if strings.Contains(err.Error(), "no cc role") {
		t.Fatalf("error = %q, an unreadable directory must not render as an unknown-role absence", err.Error())
	}
}

// Test 8 — an empty constitution is an error, both shapes: a .md empty
// after its frontmatter, and a .toml whose developer_instructions is missing
// or blank. Neither may return ("", nil).
func TestEmptyConstitutionIsAnError(t *testing.T) {
	t.Run("markdown empty after frontmatter", func(t *testing.T) {
		repo := t.TempDir()
		home := t.TempDir()
		mustWrite(t, filepath.Join(repo, ".claude", "agents", "hollow.md"),
			"---\nname: hollow\n---\n\n   \n")

		got, _, err := Resolve(pfmengine.Claude, "hollow", repo, home)
		if err == nil {
			t.Fatalf("Resolve() = %q, nil error; want an error for an empty constitution", got)
		}
		if got != "" {
			t.Fatalf("Resolve() = (%q, %v); want (\"\", err)", got, err)
		}
		if !strings.Contains(err.Error(), "empty after its frontmatter") {
			t.Fatalf("error = %q, want it to name the empty-after-frontmatter case", err.Error())
		}
	})

	t.Run("toml missing developer_instructions", func(t *testing.T) {
		repo := t.TempDir()
		home := t.TempDir()
		mustWrite(t, filepath.Join(repo, ".codex", "agents", "hollow.toml"),
			"name = \"hollow\"\ndescription = \"nothing\"\n")

		got, _, err := Resolve(pfmengine.Codex, "hollow", repo, home)
		if err == nil {
			t.Fatalf("Resolve() = %q, nil error; want an error for a missing key", got)
		}
		if got != "" {
			t.Fatalf("Resolve() = (%q, %v); want (\"\", err)", got, err)
		}
		if !strings.Contains(err.Error(), "empty or missing developer_instructions") {
			t.Fatalf("error = %q, want it to name the missing key", err.Error())
		}
	})

	t.Run("toml blank developer_instructions", func(t *testing.T) {
		repo := t.TempDir()
		home := t.TempDir()
		mustWrite(t, filepath.Join(repo, ".codex", "agents", "blank.toml"),
			"name = \"blank\"\ndeveloper_instructions = \"   \"\n")

		got, _, err := Resolve(pfmengine.Codex, "blank", repo, home)
		if err == nil {
			t.Fatalf("Resolve() = %q, nil error; want an error for a blank key", got)
		}
		if got != "" {
			t.Fatalf("Resolve() = (%q, %v); want (\"\", err)", got, err)
		}
		if !strings.Contains(err.Error(), "empty or missing developer_instructions") {
			t.Fatalf("error = %q, want it to name the blank key", err.Error())
		}
	})
}

// Test 9 — no cross-engine fallback. A role that exists ONLY as
// .claude/agents/<role>.md, asked for on a cx seat, must error and name the
// compile fix (pfm codex build <repo>) — never silently return the .md body.
func TestNoCrossEngineFallback(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	mustWrite(t, filepath.Join(repo, ".claude", "agents", "reviewer.md"),
		"---\nname: reviewer\n---\ncc-only constitution body\n")

	got, _, err := Resolve(pfmengine.Codex, "reviewer", repo, home)
	if err == nil {
		t.Fatalf("Resolve() = %q, nil error; a cc-only role must not resolve on a cx seat", got)
	}
	if got != "" {
		t.Fatalf("Resolve() returned text %q for a cc-only role on a cx seat; want empty", got)
	}
	if strings.Contains(got, "cc-only constitution body") {
		t.Fatal("the .claude source body leaked into a cx resolution")
	}
	msg := err.Error()
	if !strings.Contains(msg, "pfm codex build "+repo) {
		t.Fatalf("error = %q, want it to name the compile fix %q", msg, "pfm codex build "+repo)
	}
	if !strings.Contains(msg, "reviewer") {
		t.Fatalf("error = %q, want it to name the role", msg)
	}
}

// Test 10 — repo-local beats host-global. The same role name registered in
// both jails with different bodies must resolve to the repo-local body: the
// first rung of the ladder that names an existing file wins, full stop.
func TestRepoLocalBeatsHostGlobal(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	mustWrite(t, filepath.Join(repo, ".claude", "agents", "reviewer.md"),
		"---\nname: reviewer\n---\nREPO LOCAL BODY\n")
	mustWrite(t, filepath.Join(home, ".claude", "agents", "reviewer.md"),
		"---\nname: reviewer\n---\nHOST GLOBAL BODY\n")

	got, _, err := Resolve(pfmengine.Claude, "reviewer", repo, home)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	want := "REPO LOCAL BODY\n"
	if got != want {
		t.Fatalf("Resolve() = %q, want the repo-local body %q", got, want)
	}
	if strings.Contains(got, "HOST GLOBAL BODY") {
		t.Fatalf("Resolve() = %q, the host-global body leaked in alongside the repo-local one", got)
	}
}

// Test 11 — Resolve's Artifact: Path is always ABSOLUTE, and TOMLKey
// matches the engine that resolved it (cc -> false, the whole .md file; cx
// -> true, the developer_instructions value inside the .toml). T1 re-arm
// persists exactly this bit alongside the role name so a later reload or
// self-compact re-reads the SAME rung birth used.
func TestResolveArtifactPathIsAbsoluteAndTOMLKeyMatchesEngine(t *testing.T) {
	t.Run("cc: RELATIVE cwd still resolves an absolute .md path, TOMLKey false", func(t *testing.T) {
		// t.TempDir() itself already returns an absolute path, which would
		// make Artifact.Path absolute by inheritance alone and prove
		// nothing about Resolve's own filepath.Abs step. t.Chdir into the
		// repo and pass "." as cwd instead, so a relative walk is the only
		// thing Resolve has to work from — the one shape that actually
		// exercises the conversion this assertion pins.
		repo := t.TempDir()
		home := t.TempDir()
		mdPath := filepath.Join(repo, ".claude", "agents", "reviewer.md")
		mustWrite(t, mdPath, "---\nname: reviewer\n---\nbody\n")
		t.Chdir(repo)

		_, artifact, err := Resolve(pfmengine.Claude, "reviewer", ".", home)
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		if !filepath.IsAbs(artifact.Path) {
			t.Fatalf("Artifact.Path = %q, want an absolute path even from a relative cwd", artifact.Path)
		}
		wantPath, err := filepath.Abs(mdPath)
		if err != nil {
			t.Fatal(err)
		}
		if artifact.Path != wantPath {
			t.Fatalf("Artifact.Path = %q, want %q", artifact.Path, wantPath)
		}
		if artifact.TOMLKey {
			t.Fatal("Artifact.TOMLKey = true for a cc (.md) seat, want false")
		}
	})

	t.Run("cx: RELATIVE cwd still resolves an absolute .toml path, TOMLKey true", func(t *testing.T) {
		repo := t.TempDir()
		home := t.TempDir()
		tomlPath := filepath.Join(repo, ".codex", "agents", "reviewer.toml")
		mustWrite(t, tomlPath, "name = \"reviewer\"\ndeveloper_instructions = \"body\"\n")
		t.Chdir(repo)

		_, artifact, err := Resolve(pfmengine.Codex, "reviewer", ".", home)
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		if !filepath.IsAbs(artifact.Path) {
			t.Fatalf("Artifact.Path = %q, want an absolute path even from a relative cwd", artifact.Path)
		}
		wantPath, err := filepath.Abs(tomlPath)
		if err != nil {
			t.Fatal(err)
		}
		if artifact.Path != wantPath {
			t.Fatalf("Artifact.Path = %q, want %q", artifact.Path, wantPath)
		}
		if !artifact.TOMLKey {
			t.Fatal("Artifact.TOMLKey = false for a cx (.toml) seat, want true")
		}
	})
}
