package agentrole

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/codexprompt"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestRefreshSeatPromptReadsComposedClaudePrompt(t *testing.T) {
	repo, home, sidDir := t.TempDir(), t.TempDir(), t.TempDir()
	mustWrite(t, filepath.Join(repo, ".claude", "agents", "reader.md"), "---\nname: reader\n---\nROLE\n")
	clone := t.TempDir()
	if err := paths.WriteSourceRepoMarker(home, clone); err != nil {
		t.Fatal(err)
	}
	prompt := filepath.Join(clone, "pfm", "harness-prompts", "generated", "claude.md")
	mustWrite(t, prompt, "FLEET\n")
	if err := WriteSeatPrompt(sidDir, "cc-role", "", "<!-- pfm agent-role: reader -->\nSTALE"); err != nil {
		t.Fatal(err)
	}
	channel, err := RefreshSeatPrompt(pfmengine.Claude, sidDir, "cc-role", "", repo, home)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(channel)
	if err != nil || string(raw) != `<!-- pfm agent-role: reader -->
FLEET


---

ROLE
` {
		t.Fatalf("seat prompt = %q, %v", raw, err)
	}
	if err := os.Remove(prompt); err != nil {
		t.Fatal(err)
	}
	if _, err := RefreshSeatPrompt(
		pfmengine.Claude,
		sidDir,
		"cc-role",
		"",
		repo,
		home,
	); err == nil ||
		!strings.Contains(err.Error(), "agent role: read Claude prompt "+prompt) {
		t.Fatalf("unreadable prompt error = %v", err)
	}
}

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

// Test 2 — a .codex/agents/<role>.toml resolves to the Codex fleet prompt
// followed by exactly its developer_instructions value, byte-for-byte: the
// seat's developer_instructions replaces the config-level fleet prompt, and
// the compiled role file carries only its own body. The other keys never leak.
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
	fleetPrompt, err := codexprompt.FleetPrompt(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	role := "Body line one.\nBody line two.\n"
	if want := fleetPrompt + "\n---\n\n" + role; got != want {
		t.Fatalf("Resolve() = %q, want the fleet prompt then %q", got, role)
	}
	if strings.Contains(role, "name") || strings.Contains(got, "reads diffs") {
		t.Fatalf("Resolve() = %q, want no leakage of the name/description keys", got)
	}
}

// A Claude-authored role body that spells Claude's /code-review reaches a Codex seat as the shell
// review command: the role-body door rewrites the invocation as the fleet prompt's door does, and
// leaves the prose around it as it was.
func TestResolveCodexRewritesTheCodeReviewInvocationInTheRoleBody(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	mustWrite(t, filepath.Join(repo, ".codex", "agents", "builder.toml"),
		"name = \"builder\"\n"+
			"developer_instructions = \"\"\"\n"+
			"Your last step is `/code-review low` over your own change.\n"+
			"\"\"\"\n")

	got, _, err := Resolve(pfmengine.Codex, "builder", repo, home)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	fleetPrompt, err := codexprompt.FleetPrompt(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	role, found := strings.CutPrefix(got, fleetPrompt+"\n---\n\n")
	if !found {
		t.Fatalf("Resolve() = %q, want the fleet prompt then the role body", got)
	}
	if want := "Your last step is `" + codexprompt.RewriteCodeReview("/code-review low") +
		"` over your own change.\n"; role != want {
		t.Fatalf("role body = %q, want %q", role, want)
	}
	if strings.Contains(role, "/code-review") || !strings.Contains(role, "codex review -c") {
		t.Fatalf("role body still spells Claude's /code-review: %q", role)
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

// An unknown role's error tells a missing rung, an empty one and one listing its roles apart.
func TestUnknownRoleReportsDistinctDirectoryStates(t *testing.T) {
	for _, row := range []struct {
		name  string
		roles []string
		empty bool
		want  string
	}{
		{"neither rung exists anywhere", nil, false, "agent role: no cc role \"ghost\" found\n  searched {repo}/.claude/agents: directory not found\n  searched {home}/.claude/agents: directory not found\navailable roles: none found"},
		{"repo rung exists with no roles registered", nil, true, "agent role: no cc role \"ghost\" found\n  searched {repo}/.claude/agents: directory exists, no roles registered\n  searched {home}/.claude/agents: directory not found\navailable roles: none found"},
		{"repo rung lists the roles it has", []string{"alpha", "beta"}, false, "agent role: no cc role \"ghost\" found\n  searched {repo}/.claude/agents: alpha, beta\n  searched {home}/.claude/agents: directory not found\navailable roles: alpha, beta"},
	} {
		t.Run(row.name, func(t *testing.T) {
			repo, home := t.TempDir(), t.TempDir()
			// A .codex/agents marker pins roleLadderRoot(repo) at repo without creating repo/.claude/agents.
			mustMkdir(t, filepath.Join(repo, ".codex", "agents"))
			if row.empty {
				mustMkdir(t, filepath.Join(repo, ".claude", "agents"))
			}
			for _, role := range row.roles {
				mustWrite(t, filepath.Join(repo, ".claude", "agents", role+".md"), "---\n---\n"+role+" body\n")
			}
			_, _, err := Resolve(pfmengine.Claude, "ghost", repo, home)
			if got := strings.NewReplacer(repo, "{repo}", home, "{home}").Replace(fmt.Sprint(err)); got != row.want {
				t.Fatalf("Resolve() error = %q, want %q", got, row.want)
			}
		})
	}
}

// Test 6 — ladder dedupe. When roleLadderRoot(cwd) resolves to the same directory
// as home, the unknown-role message must list that directory ONCE, not
// twice: one directory searched once must not claim a breadth of search it
// never had.
//
// This test is proved load-bearing separately: reverting the `ladder`
// dedupe guard in Resolve to the old two-element literal makes it FAIL (see
// the red-then-green evidence in the report this test file shipped with).
func TestLadderDedupeSameDirectoryListedOnce(t *testing.T) {
	// home == cwd, and NEITHER has a .claude/agents or .codex/agents marker
	// anywhere above it (t.TempDir() is a fresh leaf), so roleLadderRoot(home)
	// falls through to "start" — home itself. Both ladder rungs would
	// therefore name the exact same directory without the guard.
	home := t.TempDir()

	_, _, err := Resolve(pfmengine.Claude, "ghost", home, home)
	if got, want := strings.ReplaceAll(
		fmt.Sprint(err),
		home,
		"{home}",
	), "agent role: no cc role \"ghost\" found\n  searched {home}/.claude/agents: directory not found\navailable roles: none found"; got != want {
		t.Fatalf("Resolve() error = %q, want %q", got, want)
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

func TestEmptyConstitutionIsAnError(t *testing.T) {
	for _, row := range []struct {
		name   string
		engine pfmengine.ID
		file   string
		body   string
		want   string
	}{
		{"markdown empty after frontmatter", pfmengine.Claude, ".claude/agents/hollow.md", "---\nname: hollow\n---\n\n   \n", " | agent role: {repo}/.claude/agents/hollow.md is empty after its frontmatter — no constitution to launch a seat with"},
		{"toml missing developer_instructions", pfmengine.Codex, ".codex/agents/hollow.toml", "name = \"hollow\"\ndescription = \"nothing\"\n", " | agent role: {repo}/.codex/agents/hollow.toml has an empty or missing developer_instructions key"},
		{"toml blank developer_instructions", pfmengine.Codex, ".codex/agents/hollow.toml", "name = \"blank\"\ndeveloper_instructions = \"   \"\n", " | agent role: {repo}/.codex/agents/hollow.toml has an empty or missing developer_instructions key"},
	} {
		t.Run(row.name, func(t *testing.T) {
			repo, home := t.TempDir(), t.TempDir()
			mustWrite(t, filepath.Join(repo, row.file), row.body)
			got, _, err := Resolve(row.engine, "hollow", repo, home)
			if got := got + " | " + strings.ReplaceAll(fmt.Sprint(err), repo, "{repo}"); got != row.want {
				t.Fatalf("Resolve() = %q, want %q", got, row.want)
			}
		})
	}
}

// Test 9 — no cross-engine fallback. A role that exists ONLY as
// .claude/agents/<role>.md, asked for on a cx seat, must error and name the
// compile fix (engine-sync codex build <repo>) — never silently return the .md body.
func TestNoCrossEngineFallback(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	mustWrite(t, filepath.Join(repo, ".claude", "agents", "reviewer.md"),
		"---\nname: reviewer\n---\ncc-only constitution body\n")

	got, _, err := Resolve(pfmengine.Codex, "reviewer", repo, home)
	if got := got + " | " + strings.NewReplacer(repo, "{repo}", home, "{home}").
		Replace(fmt.Sprint(err)); got != " | agent role \"reviewer\": found {repo}/.claude/agents/reviewer.md but no compiled {repo}/.codex/agents/reviewer.toml — this seat is cx (codex) and reads the compiled artifact, never the .claude source; run: engine-sync codex build {repo}" {
		t.Fatalf(
			"Resolve() = %q, want %q",
			got,
			" | agent role \"reviewer\": found {repo}/.claude/agents/reviewer.md but no compiled {repo}/.codex/agents/reviewer.toml — this seat is cx (codex) and reads the compiled artifact, never the .claude source; run: engine-sync codex build {repo}",
		)
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

// Resolve reports an absolute artifact path for either engine.
func TestResolveArtifactPathIsAbsolute(t *testing.T) {
	t.Run("cc: relative cwd still resolves an absolute md path", func(t *testing.T) {
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
	})

	t.Run("cx: relative cwd still resolves an absolute toml path", func(t *testing.T) {
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
	})
}

func TestWorkbenchRoleAndReload(t *testing.T) {
	for _, name := range []string{"Codex role", "reload no role", "reload Codex", "reload role", "reload disabled", "reload invalid", "role outside"} {
		t.Run(name, func(t *testing.T) {
			root, dir, home := roleWorkbenchFixture(t)
			sid := t.TempDir()
			engine := pfmengine.Claude
			if name == "Codex role" || name == "reload Codex" || name == "reload disabled" {
				engine = pfmengine.Codex
			}
			if name == "Codex role" || name == "reload Codex" {
				mustWrite(t, paths.WorkbenchManifest(dir), `{"prompt":"scribe.md","engines":["codex","claude"]}`)
			}
			if name == "role outside" {
				body, _, err := Resolve(engine, "r", root, home)
				base, baseErr := BasePrompt(engine, root, home)
				got, composeErr := ComposeSeatPrompt(engine, "r", body, base)
				want := "<!-- pfm agent-role: r -->\nFLEET\n\n---\n\nROLE R"
				if err != nil || baseErr != nil || composeErr != nil || got != want {
					t.Fatalf("outside role = %q, %v/%v/%v; want %q", got, err, baseErr, composeErr, want)
				}
				return
			}
			if name == "Codex role" {
				got, _, err := Resolve(engine, "r", dir, home)
				if err != nil || got != "You are scribe.\n---\n\nROLE R" {
					t.Fatalf("Codex role = %q, %v", got, err)
				}
				return
			}
			if name == "reload role" {
				if err := WriteSeatPrompt(sid, "cc-r", "", "<!-- pfm agent-role: r -->\nSTALE"); err != nil {
					t.Fatal(err)
				}
			}
			if name == "reload invalid" {
				mustWrite(t, paths.WorkbenchManifest(dir), `{"prompt":""}`)
			}
			got, err := RefreshSeatPrompt(engine, sid, "cc-r", "", dir, home)
			if name == "reload invalid" {
				if err == nil || err.Error() != paths.WorkbenchManifest(dir)+`: "prompt" is required` {
					t.Fatalf("reload invalid = %q, %v", got, err)
				}
				return
			}
			want := filepath.Join(dir, ".professor", "scribe.md")
			switch name {
			case "reload Codex":
				want = "You are scribe."
			case "reload disabled":
				want = ""
			case "reload role":
				raw, readErr := os.ReadFile(got)
				want = "<!-- pfm agent-role: r -->\nYou are scribe.\n\n---\n\nROLE R"
				if readErr != nil {
					t.Fatal(readErr)
				}
				got = string(raw)
			}
			if err != nil || got != want {
				t.Fatalf("reload = %q, %v; want %q", got, err, want)
			}
		})
	}
}
