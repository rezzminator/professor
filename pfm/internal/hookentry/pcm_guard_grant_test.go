package hookentry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pcmGuardWriteGrant writes body as root's .claude/pcm-guard.json.
func pcmGuardWriteGrant(t *testing.T, root, body string) {
	t.Helper()
	path := filepath.Join(root, ".claude", pcmGuardGrantFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// pcmGuardRequireDenyWarning fails unless the guard denied the payload to
// pcm and named warn on stderr ("" for silence).
func pcmGuardRequireDenyWarning(t *testing.T, payload, home, warn string) {
	t.Helper()
	pcmGuardRequireDeny(t, payload, pcmGuardOnlyPcm)
	_, _, stderr := runPcmGuard(payload)
	if stderr = strings.ReplaceAll(stderr, home, "{home}"); stderr != warn {
		t.Fatalf("stderr = %q, want %q\npayload=%s", stderr, warn, payload)
	}
}

// Without a grant file the agent is refused in silence; a malformed or
// linked grant file and an entry outside .claude/ grant nothing, saying so.
func TestPcmGuardGrantRefusesWhatItCannotTrust(t *testing.T) {
	for _, row := range []struct{ name, body, link, warn string }{
		{name: "missing"},
		{name: "malformed", body: `{"writers": [`, warn: "pfm internal pcm-guard: grant file {home}/proj/.claude/pcm-guard.json grants nothing: decode: unexpected end of JSON input\n"},
		{name: "symlink", link: `{"writers": {"documenter": [".claude/skills"]}}`, warn: "pfm internal pcm-guard: grant file {home}/proj/.claude/pcm-guard.json grants nothing: it is a symlink\n"},
		{name: "whole .claude", body: `{"writers": {"documenter": [".claude"]}}`, warn: "pfm internal pcm-guard: grant file {home}/proj/.claude/pcm-guard.json: \".claude\" is no directory under .claude/; it grants nothing\n"},
		{name: "escape", body: `{"writers": {"documenter": [".claude/../.claude"]}}`, warn: "pfm internal pcm-guard: grant file {home}/proj/.claude/pcm-guard.json: \".claude/../.claude\" is no directory under .claude/; it grants nothing\n"},
		{name: "outside", body: `{"writers": {"documenter": ["src"]}}`, warn: "pfm internal pcm-guard: grant file {home}/proj/.claude/pcm-guard.json: \"src\" is no directory under .claude/; it grants nothing\n"},
		{name: "absolute", body: `{"writers": {"documenter": ["/tmp/.claude/skills"]}}`, warn: "pfm internal pcm-guard: grant file {home}/proj/.claude/pcm-guard.json: \"/tmp/.claude/skills\" is no directory under .claude/; it grants nothing\n"},
	} {
		t.Run(row.name, func(t *testing.T) {
			f := pcmGuardHome(t)
			if row.body != "" {
				pcmGuardWriteGrant(t, f.project, row.body)
			}
			if row.link != "" {
				outside := filepath.Join(f.home, "grant.json")
				if err := os.WriteFile(outside, []byte(row.link), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(f.project, ".claude", pcmGuardGrantFile)); err != nil {
					t.Fatal(err)
				}
			}
			target := filepath.Join(f.project, ".claude", "skills", "s", "SKILL.md")
			pcmGuardRequireDenyWarning(t, pcmGuardEdit(t, "Write", target, "documenter"), f.home, row.warn)
		})
	}
}

// A grant covers only the project's own .claude/: a link inside the granted
// directory resolving into the clone's templates, a grant file in a Claude
// config dir (whose own files anyone may write) or in the clone's templates,
// and an engine caller stay refused.
func TestPcmGuardGrantStaysInsideItsProject(t *testing.T) {
	f := pcmGuardHome(t)
	grant := `{"writers": {"documenter": [".claude/skills"]}}`
	pcmGuardWriteGrant(t, f.project, grant)
	link := filepath.Join(f.project, ".claude", "skills", "s", "link.md")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(f.clone, "templates", "global", "rules", "x.md"), link); err != nil {
		t.Fatal(err)
	}
	pcmGuardRequireDeny(t, pcmGuardEdit(t, "Write", link, "documenter"), pcmGuardOnlyPcm)
	pcmGuardWriteGrant(t, f.home, grant)
	store := filepath.Join(f.home, ".claude", "skills", "s", "SKILL.md")
	pcmGuardRequireDeny(t, pcmGuardEdit(t, "Write", store, "documenter"), pcmGuardOnlyPcm)
	templates := filepath.Join(f.clone, "templates", "project")
	pcmGuardWriteGrant(t, templates, grant)
	shipped := filepath.Join(templates, ".claude", "skills", "s", "SKILL.md")
	pcmGuardRequireDeny(t, pcmGuardEdit(t, "Write", shipped, "documenter"), pcmGuardOnlyPcm)
	t.Setenv("CODEX_HOME", "")
	call := engineGuardShellCall("codex", `echo x > .claude/skills/s/SKILL.md`, f.project)
	call["agent_type"] = "documenter"
	code, stdout, _ := runEngineGuard(t, "codex", call)
	if code != 0 || gitGuardDecision(t, stdout) != "deny" {
		t.Fatalf("code=%d stdout=%q, want the engine guard to deny a granted agent", code, stdout)
	}
}
