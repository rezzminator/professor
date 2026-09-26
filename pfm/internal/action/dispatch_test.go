package action

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const actionDispatchHelperEnv = "PFM_ACTION_DISPATCH_HELPER"

func TestActionDispatchExecHelper(t *testing.T) {
	line := os.Getenv(actionDispatchHelperEnv)
	if line == "" {
		return
	}
	if err := execute(line); err != nil {
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
	command := exec.Command(executable, "-test.run=^TestActionDispatchExecHelper$")
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

func TestDispatchPipeGoldenAndTTYExec(t *testing.T) {
	originalTerminal := OutputIsTerminal
	originalLookPath := LookPath
	originalExec := Exec
	t.Cleanup(func() {
		OutputIsTerminal = originalTerminal
		LookPath = originalLookPath
		Exec = originalExec
	})

	const attachLine = "TMUX= tmux -L 'cc-1-2-3' attach -t 'live-session'"
	var stdout bytes.Buffer
	OutputIsTerminal = func(io.Writer) bool { return false }
	if err := Dispatch(&stdout, attachLine); err != nil {
		t.Fatal(err)
	}
	if got, want := stdout.String(), attachLine+"\n"; got != want {
		t.Fatalf("pipe action = %q, want byte-identical %q", got, want)
	}

	type execCall struct {
		path string
		args []string
		env  []string
	}
	var calls []execCall
	execReturned := errors.New("exec test return")
	OutputIsTerminal = func(io.Writer) bool { return true }
	LookPath = func(file string) (string, error) { return "/jail/bin/" + file, nil }
	Exec = func(path string, args, env []string) error {
		calls = append(
			calls,
			execCall{path: path, args: append([]string(nil), args...), env: append([]string(nil), env...)},
		)
		return execReturned
	}

	t.Setenv("TMUX", "/tmp/driver,1,0")
	stdout.Reset()
	if err := Dispatch(&stdout, attachLine); !errors.Is(err, execReturned) {
		t.Fatalf("terminal attach error = %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("terminal attach printed %q", stdout.String())
	}
	if len(calls) != 1 || calls[0].path != "/jail/bin/tmux" ||
		!reflect.DeepEqual(calls[0].args, []string{"tmux", "-L", "cc-1-2-3", "attach", "-t", "live-session"}) ||
		environmentValue(calls[0].env, "TMUX") != "" {
		t.Fatalf("terminal tmux exec = %#v", calls)
	}

	const shellLine = "(cd -- '/work/project' && CODEX_HOME='/work/codex' cx)"
	if err := Dispatch(&stdout, shellLine); !errors.Is(err, execReturned) {
		t.Fatalf("terminal shell error = %v", err)
	}
	if len(calls) != 2 || calls[1].path != "/jail/bin/zsh" ||
		!reflect.DeepEqual(calls[1].args, []string{"zsh", "-ic", shellLine}) {
		t.Fatalf("terminal zsh exec = %#v", calls)
	}
}

func TestDirectTmuxArgumentsPreserveQuotedData(t *testing.T) {
	line := `TMUX= exec tmux -L 'cc-1'"'"'quoted' new-session -c '/work/a b' 'printf "$HOME;*"!'`
	got, direct, err := directTmuxArguments(line)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-L", "cc-1'quoted", "new-session", "-c", "/work/a b", `printf "$HOME;*"!`}
	if !direct || !reflect.DeepEqual(got, want) {
		t.Fatalf("directTmuxArguments() = %q, %v; want %q, true", got, direct, want)
	}
}

func replaceActionTestEnvironment(environment []string, replacements map[string]string) []string {
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

func environmentValue(environment []string, key string) string {
	prefix := key + "="
	for _, entry := range environment {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}
	return "\x00missing"
}
