package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexCommandHelpAndOnlyBuildCheckActions(t *testing.T) {
	jailTest(t)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"codex", "help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("codex help code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	for _, want := range []string{
		"usage: pfm codex build|check",
		"--home",
		"--model alias=value",
		"--root-adapter",
		"--agent-preamble",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("codex help missing %q:\n%s", want, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "--overrides-dir") {
		t.Fatalf("codex help still advertises retired override support:\n%s", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(
		[]string{"codex", "generate"},
		&stdout,
		&stderr,
	); code != 2 ||
		!strings.Contains(stderr.String(), "unknown action") {
		t.Fatalf("retired codex generate route code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(
		[]string{"codex", "doctor"},
		&stdout,
		&stderr,
	); code != 2 ||
		!strings.Contains(stderr.String(), "unknown action") {
		t.Fatalf("retired codex doctor route code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestCodexCommandBuildCheckAndCheckIsReadOnly(t *testing.T) {
	jailTest(t)
	repo := filepath.Join(os.Getenv("PFM_HOME"), "repo")
	writeCodexCLIFile(t, filepath.Join(repo, "CLAUDE.md"), "# Fixture\n\nUse /build.\n")

	var stdout, stderr bytes.Buffer
	if code := run([]string{"codex", "build", repo}, &stdout, &stderr); code != 0 {
		t.Fatalf("codex build code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(repo, "AGENTS.md")); err != nil {
		t.Fatalf("build did not materialize AGENTS.md: %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"codex", "check", repo}, &stdout, &stderr); code != 0 {
		t.Fatalf("current codex check code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	if err := os.Remove(filepath.Join(repo, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	before := codexCLISnapshot(t, repo)
	stdout.Reset()
	stderr.Reset()
	if code := run(
		[]string{"codex", "check", repo},
		&stdout,
		&stderr,
	); code != 1 ||
		!strings.Contains(stderr.String(), "MISSING") {
		t.Fatalf("missing-output check code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	after := codexCLISnapshot(t, repo)
	if before != after {
		t.Fatalf("check changed the output tree:\n before=%s\n after=%s", before, after)
	}
}

func TestCodexCommandStrictRepoConfigAndCLIModelValidation(t *testing.T) {
	jailTest(t)
	repo := filepath.Join(os.Getenv("PFM_HOME"), "strict-repo")
	writeCodexCLIFile(t, filepath.Join(repo, "CLAUDE.md"), "# Fixture\n")
	writeCodexCLIFile(t, filepath.Join(repo, ".claude", "codex-build.json"), `{"version":1,"unexpected":true}`)

	var stdout, stderr bytes.Buffer
	if code := run(
		[]string{"codex", "build", repo},
		&stdout,
		&stderr,
	); code != 1 ||
		!strings.Contains(stderr.String(), "unexpected") {
		t.Fatalf("unknown repo config key code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	writeCodexCLIFile(
		t,
		filepath.Join(repo, ".claude", "codex-build.json"),
		`{"version":1,"overridesDir":".claude/codex-overrides"}`,
	)
	stdout.Reset()
	stderr.Reset()
	if code := run(
		[]string{"codex", "build", repo},
		&stdout,
		&stderr,
	); code != 1 ||
		!strings.Contains(stderr.String(), "overridesDir") {
		t.Fatalf("retired overridesDir config key code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run(
		[]string{"codex", "build", "--model", "broken", repo},
		&stdout,
		&stderr,
	); code != 2 ||
		!strings.Contains(stderr.String(), "alias=value") {
		t.Fatalf("invalid model override code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestCodexCommandFlagsReachCompiler(t *testing.T) {
	jailTest(t)
	repo := filepath.Join(os.Getenv("PFM_HOME"), "flags-repo")
	writeCodexCLIFile(t, filepath.Join(repo, "CLAUDE.md"), "# Fixture\n\n## Subagents\n\nUse sonnet.\n")

	var stdout, stderr bytes.Buffer
	args := []string{
		"codex", "build", "--home", filepath.Join(os.Getenv("PFM_HOME"), "codex-home"),
		"--model", "sonnet=gpt-test", "--root-adapter", "Fixture adapter",
		"--agent-preamble", "Fixture preamble", "--exclude-dir", "skip",
		"--exclude-project", "ignored", "--never-register", "secret",
		"--suffix-mode", "project", "--suffix-prefix", "fixture-",
		repo,
	}
	if code := run(args, &stdout, &stderr); code != 0 {
		t.Fatalf("codex flag build code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	agents, err := os.ReadFile(filepath.Join(repo, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(agents), "Fixture adapter") {
		t.Fatalf("root adapter flag did not reach compiler: %q", agents)
	}
}

func writeCodexCLIFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func codexCLISnapshot(t *testing.T, root string) string {
	t.Helper()
	var result strings.Builder
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		result.WriteString(rel)
		result.WriteByte('=')
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			result.WriteString("link:")
			result.WriteString(target)
		} else {
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			result.Write(content)
		}
		result.WriteByte('\n')
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result.String()
}
