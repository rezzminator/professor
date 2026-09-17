package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const actionDispatchHelperEnv = "PFM_ACTION_DISPATCH_HELPER"

func TestActionDispatchExecHelper(t *testing.T) {
	line := os.Getenv(actionDispatchHelperEnv)
	if line == "" {
		return
	}
	if err := executeAction(line); err != nil {
		t.Fatalf("execute helper action: %v", err)
	}
	t.Fatal("execute helper action returned without replacing the process")
}

func TestTerminalShellActionExecsInteractiveZsh(t *testing.T) {
	home := t.TempDir()
	marker := filepath.Join(home, "called")
	zshrc := "cx() {\n  print -r -- \"$1\" > \"$PFM_ACTION_MARKER\"\n}\n"
	if err := os.WriteFile(filepath.Join(home, ".zshrc"), []byte(zshrc), 0o600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(
		executable,
		"-test.run=^TestActionDispatchExecHelper$",
	)
	command.Env = replaceActionTestEnvironment(os.Environ(), map[string]string{
		"HOME":                  home,
		"ZDOTDIR":               home,
		"PFM_ACTION_MARKER":     marker,
		actionDispatchHelperEnv: "(cx 'shell-function-proof')",
	})
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("interactive zsh action: %v: %s", err, output)
	}
	content, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(content), "shell-function-proof\n"; got != want {
		t.Fatalf("shell function marker = %q, want %q", got, want)
	}
}

func replaceActionTestEnvironment(
	environment []string,
	replacements map[string]string,
) []string {
	result := make([]string, 0, len(environment)+len(replacements))
	for _, entry := range environment {
		key, _, found := strings.Cut(entry, "=")
		if _, replaced := replacements[key]; found && replaced {
			continue
		}
		result = append(result, entry)
	}
	for key, value := range replacements {
		result = append(result, key+"="+value)
	}
	return result
}
