package action

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

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
	t.Setenv("HERDR_PANE_ID", "")
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
	// An unterminated quote is not the splitter's to refuse: zsh gets the line.
	got, direct, err = directTmuxArguments(`TMUX= exec tmux -L 'cc-1 attach`)
	if err != nil || direct || got != nil {
		t.Fatalf("directTmuxArguments(unterminated) = %q, %v, %v; want nil, false, nil", got, direct, err)
	}
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

// A tmux client never starts in a Herdr pane: not exec'd here, not printed for
// a shell to eval; a shell line still runs there.
func TestDispatchRefusesTmuxClientInHerdr(t *testing.T) {
	originalTerminal, originalLookPath, originalExec := OutputIsTerminal, LookPath, Exec
	t.Cleanup(func() { OutputIsTerminal, LookPath, Exec = originalTerminal, originalLookPath, originalExec })
	t.Setenv("HERDR_PANE_ID", "w1:p2")
	var calls [][]string
	LookPath = func(file string) (string, error) { return "/jail/bin/" + file, nil }
	Exec = func(_ string, args, _ []string) error {
		calls = append(calls, args)
		return nil
	}
	const want = "host refused at tmux attach: this terminal is Herdr pane w1:p2, where a tmux client is never " +
		"attached; open the chat from a plain terminal"
	for _, terminal := range []bool{false, true} {
		OutputIsTerminal = func(io.Writer) bool { return terminal }
		var stdout bytes.Buffer
		err := Dispatch(&stdout, AttachLine("cc-1-2-3", "live-session", false))
		if err == nil || err.Error() != want || stdout.Len() != 0 || len(calls) != 0 {
			t.Fatalf("terminal=%v: err=%v stdout=%q execs=%v; want %q, nothing printed or run",
				terminal, err, stdout.String(), calls, want)
		}
	}
	if err := Dispatch(io.Discard, "exec pfm internal seat-host 87caf6bf"); err != nil || len(calls) != 1 {
		t.Fatalf("shell line in Herdr: err=%v execs=%v; want it run", err, calls)
	}
}
