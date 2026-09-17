package action

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"unicode"

	"github.com/charmbracelet/x/term"

	"hostops/pfm/internal/deps"
	pfmtmux "hostops/pfm/internal/tmux"
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
		return Exec(path, append([]string{pfmtmux.Binary}, arguments...), deps.EnvironmentWith("TMUX", ""))
	}

	path, err := LookPath("zsh")
	if err != nil {
		return fmt.Errorf("find zsh: %w", err)
	}
	return Exec(path, []string{"zsh", "-ic", line}, os.Environ())
}

func directTmuxArguments(line string) ([]string, bool, error) {
	words, err := SplitShellWords(line)
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

// SplitShellWords decodes the quoting emitted by Quote. It performs no
// expansion, substitution, globbing, or operator interpretation.
func SplitShellWords(line string) ([]string, error) {
	words := make([]string, 0, 12)
	var word strings.Builder
	var quote rune
	inWord := false
	escaped := false
	flush := func() {
		if !inWord {
			return
		}
		words = append(words, word.String())
		word.Reset()
		inWord = false
	}

	for _, character := range line {
		if escaped {
			word.WriteRune(character)
			inWord = true
			escaped = false
			continue
		}
		switch quote {
		case '\'':
			if character == '\'' {
				quote = 0
			} else {
				word.WriteRune(character)
			}
			inWord = true
		case '"':
			switch character {
			case '"':
				quote = 0
			case '\\':
				escaped = true
			default:
				word.WriteRune(character)
			}
			inWord = true
		default:
			switch {
			case unicode.IsSpace(character):
				flush()
			case character == '\'' || character == '"':
				quote = character
				inWord = true
			case character == '\\':
				escaped = true
				inWord = true
			default:
				word.WriteRune(character)
				inWord = true
			}
		}
	}
	if escaped {
		return nil, errors.New("trailing escape")
	}
	if quote != 0 {
		return nil, errors.New("unterminated quote")
	}
	flush()
	return words, nil
}
