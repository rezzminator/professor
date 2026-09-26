package tmux

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLaunchBodyExecsOnlyASimpleCommand(t *testing.T) {
	for _, row := range []struct{ run, want string }{
		{"env -u A codex --x 'a;b|c'", "exec env -u A codex --x 'a;b|c'"},
		{"CODEX_HOME='/h' codex", "exec env CODEX_HOME='/h' codex"},
		{"codex 'developer_instructions=\"\"\"\nline\"\"\"'", "exec codex 'developer_instructions=\"\"\"\nline\"\"\"'"},
		{`codex "a;b" \;`, `exec codex "a;b" \;`},
		{"printf '%s\\n' banner; env codex", "printf '%s\\n' banner; env codex"},
		{"claude || { echo; exec claude; }", "claude || { echo; exec claude; }"},
		{"codex > log", "codex > log"},
		{"codex $(pwd)", "codex $(pwd)"},
		{"if true; then codex; fi", "if true; then codex; fi"},
		{"codex 'unterminated", "codex 'unterminated"},
		{"--flag=1 codex", "exec --flag=1 codex"},
		{"1A=b codex", "exec 1A=b codex"},
	} {
		if got := launchBody(row.run); got != row.want {
			t.Errorf("launchBody(%q) = %q, want %q", row.run, got, row.want)
		}
	}
}

func TestPrepareLaunchKeepsAnInBudgetRunInline(t *testing.T) {
	dir := t.TempDir()
	run := strings.Repeat("x", InlineRunBudget)
	launch, err := PrepareLaunch(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	if launch.Command != run {
		t.Fatalf("an in-budget run was rewritten: %.40q", launch.Command)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("an in-budget run wrote %v (err %v)", entries, err)
	}
	if err := launch.Discard(); err != nil {
		t.Fatalf("discard an inline launch: %v", err)
	}
}

func TestPrepareLaunchWritesAPrivateOneShotScript(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "it's")
	run := "codex '" + strings.Repeat("x", InlineRunBudget) + "'"
	launch, err := PrepareLaunch(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), launchScriptPrefix) {
		t.Fatalf("want one launch script in %s, got %v (err %v)", dir, entries, err)
	}
	script := filepath.Join(dir, entries[0].Name())
	if want := `/bin/sh '` + strings.ReplaceAll(script, "'", `'"'"'`) + `'`; launch.Command != want {
		t.Fatalf("command = %q, want %q", launch.Command, want)
	}
	info, err := os.Stat(script)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("script mode = %v (err %v), want 0700", info, err)
	}
	body, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	if want := "#!/bin/sh\nrm -f -- \"$0\"\nexec " + run + "\n"; string(body) != want {
		t.Fatalf("script body starts %.60q", body)
	}
	if err := launch.Discard(); err != nil {
		t.Fatalf("discard: %v", err)
	}
	if _, err := os.Stat(script); !os.IsNotExist(err) {
		t.Fatalf("discard left the script: %v", err)
	}
	if err := launch.Discard(); err != nil {
		t.Fatalf("discarding a consumed script is not an error: %v", err)
	}
}

func TestPrepareLaunchNamesAnUnwritableDirectory(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := PrepareLaunch(filepath.Join(file, "dir"), strings.Repeat("x", InlineRunBudget+1))
	if err == nil || !strings.Contains(err.Error(), "prepare launch script") {
		t.Fatalf("an unwritable directory = %v, want a named launch-script error", err)
	}
	if strings.Contains(err.Error(), "xxxx") {
		t.Fatalf("the error carries the run: %.80q", err)
	}
}
