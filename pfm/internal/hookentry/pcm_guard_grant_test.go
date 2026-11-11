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
func pcmGuardRequireDenyWarning(t *testing.T, payload, warn string) {
	t.Helper()
	pcmGuardRequireDeny(t, payload, pcmGuardOnlyPcm)
	_, _, stderr := runPcmGuard(payload)
	if (warn == "") != (stderr == "") || !strings.Contains(stderr, warn) {
		t.Fatalf("stderr=%q, want %q\npayload=%s", stderr, warn, payload)
	}
}

// Without a grant file the agent is refused in silence; a malformed or
// linked grant file and an entry outside .claude/ grant nothing, saying so.
func TestPcmGuardGrantRefusesWhatItCannotTrust(t *testing.T) {
	for _, row := range []struct{ name, body, link, warn string }{
		{name: "missing"},
		{name: "malformed", body: `{"writers": [`, warn: "grants nothing"},
		{name: "symlink", link: `{"writers": {"documenter": [".claude/skills"]}}`, warn: "symlink"},
		{name: "whole .claude", body: `{"writers": {"documenter": [".claude"]}}`, warn: "no directory under"},
		{name: "escape", body: `{"writers": {"documenter": [".claude/../.claude"]}}`, warn: "no directory under"},
		{name: "outside", body: `{"writers": {"documenter": ["src"]}}`, warn: "no directory under"},
		{name: "absolute", body: `{"writers": {"documenter": ["/tmp/.claude/skills"]}}`, warn: "no directory under"},
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
			pcmGuardRequireDenyWarning(t, pcmGuardEdit(t, "Write", target, "documenter"), row.warn)
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
	if code != 0 || !strings.Contains(stdout, gitGuardDenied) {
		t.Fatalf("code=%d stdout=%q, want the engine guard to deny a granted agent", code, stdout)
	}
}
