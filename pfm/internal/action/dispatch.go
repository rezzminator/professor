package action

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"

	"github.com/charmbracelet/x/term"

	"github.com/rezzminator/professor/pfm/internal/cmdparse"
	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmtmux "github.com/rezzminator/professor/pfm/internal/tmux"
)

// OutputIsTerminal is the terminal-detection seam used by Dispatch.
var OutputIsTerminal = func(writer io.Writer) bool {
	file, ok := writer.(interface{ Fd() uintptr })
	return ok && term.IsTerminal(file.Fd())
}

// LookPath is the executable-resolution seam used by Dispatch.
var LookPath = deps.Resolve

// Exec is the process-replacement seam used by Dispatch.
var Exec = syscall.Exec

// Dispatch preserves the K1 one-line protocol for captured stdout. With a
// human-facing terminal, pfm becomes the selected action instead.
func Dispatch(stdout io.Writer, line string) error {
	if !OutputIsTerminal(stdout) {
		_, err := fmt.Fprintln(stdout, line)
		return err
	}
	return execute(line)
}

func execute(line string) error {
	arguments, tmuxAction, err := directTmuxArguments(line)
	if err != nil {
		return err
	}
	if tmuxAction {
		path, err := LookPath(pfmtmux.Binary)
		if err != nil {
			return fmt.Errorf("find tmux: %w", err)
		}
		return Exec(path, append([]string{pfmtmux.Binary}, arguments...), pfmtmux.Environment())
	}

	path, err := LookPath("zsh")
	if err != nil {
		return fmt.Errorf("find zsh: %w", err)
	}
	return Exec(path, []string{"zsh", "-ic", line}, os.Environ())
}

func directTmuxArguments(line string) ([]string, bool, error) {
	// Only a generated TMUX= line is split; any other line is zsh's to parse.
	if !strings.HasPrefix(line, "TMUX=") {
		return nil, false, nil
	}
	words, err := cmdparse.SplitShellWords(line)
	if err != nil {
		return nil, false, fmt.Errorf("decode tmux action: %w", err)
	}
	if len(words) == 0 || words[0] != "TMUX=" {
		return nil, false, nil
	}
	index := 1
	if index < len(words) && words[index] == "exec" {
		index++
	}
	if index >= len(words) || words[index] != pfmtmux.Binary {
		return nil, false, errors.New("generated TMUX action is not a tmux command")
	}
	if index+1 >= len(words) {
		return nil, false, errors.New("generated tmux action has no arguments")
	}
	return words[index+1:], true, nil
}
