package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hostops/pfm/internal/paths"
)

func TestOpenCodeCommandBuildCheckDoctorAndUsage(t *testing.T) {
	jailTest(t)
	repo := filepath.Join(os.Getenv("PFM_HOME"), "opencode-repo")
	home := filepath.Join(os.Getenv("PFM_HOME"), "opencode-home")
	writeCodexCLIFile(t, filepath.Join(repo, "CLAUDE.md"), "# Fixture\n")
	writeCodexCLIFile(
		t,
		filepath.Join(repo, ".claude", "commands", "wave", "review.md"),
		"---\ndescription: Review.\n---\nReview.\n",
	)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"opencode", "build", "--home", home, repo}, &stdout, &stderr); code != 0 {
		t.Fatalf("opencode build code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(repo, ".opencode", "command", "wave-review.md")); err != nil {
		t.Fatalf("opencode build did not write command: %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"opencode", "check", "--home", home, repo}, &stdout, &stderr); code != 0 {
		t.Fatalf("opencode check code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"opencode", "doctor", "--home", home, repo}, &stdout, &stderr); code != 0 {
		t.Fatalf("opencode doctor code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(
		[]string{"opencode", "help"},
		&stdout,
		&stderr,
	); code != 0 ||
		!strings.Contains(stdout.String(), "usage: pfm opencode") {
		t.Fatalf("opencode help code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestOpenCodeHelpAndExplicitRootDoNotRequireCWDRepo(t *testing.T) {
	t.Chdir(t.TempDir())
	repo := t.TempDir()
	home := t.TempDir()
	writeCodexCLIFile(
		t,
		filepath.Join(repo, ".claude", "commands", "review.md"),
		"---\ndescription: Review.\n---\nReview.\n",
	)
	runtime := commandRuntime{Paths: paths.Values{Home: home}}
	var stdout, stderr bytes.Buffer
	if code := runOpenCode([]string{"help"}, &stdout, &stderr, runtime); code != 0 {
		t.Fatalf("help from outside a repo code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := runOpenCode([]string{"build", repo}, &stdout, &stderr, runtime); code != 0 {
		t.Fatalf(
			"explicit-root build from outside a repo code=%d stdout=%q stderr=%q",
			code,
			stdout.String(),
			stderr.String(),
		)
	}
}
