package main

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
)

const tmuxExecutable = "tmux"

var (
	actionOutputIsTerminal = func(writer io.Writer) bool {
		file, ok := writer.(interface{ Fd() uintptr })
		return ok && term.IsTerminal(file.Fd())
	}
	actionLookPath = deps.Resolve
	actionExec     = syscall.Exec
)

// dispatchAction preserves the K1 one-line protocol for a captured stdout.
// With a human-facing terminal, pfm becomes the selected action instead.
func dispatchAction(stdout io.Writer, line string) error {
	if !actionOutputIsTerminal(stdout) {
		_, err := fmt.Fprintln(stdout, line)
		return err
	}
	return executeAction(line)
}

func executeAction(line string) error {
	arguments, tmuxAction, err := directTmuxArguments(line)
	if err != nil {
		return err
	}
	if tmuxAction {
		path, err := actionLookPath(tmuxExecutable)
		if err != nil {
			return fmt.Errorf("find tmux: %w", err)
		}
		return actionExec(
			path,
			append([]string{tmuxExecutable}, arguments...),
			environmentWith("TMUX", ""),
		)
	}

	path, err := actionLookPath("zsh")
	if err != nil {
		return fmt.Errorf("find zsh: %w", err)
	}
	return actionExec(path, []string{"zsh", "-ic", line}, os.Environ())
}

func directTmuxArguments(line string) ([]string, bool, error) {
	words, err := splitGeneratedShellWords(line)
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
	if index >= len(words) || words[index] != tmuxExecutable {
		return nil, false, errors.New("generated TMUX action is not a tmux command")
	}
	if index+1 >= len(words) {
		return nil, false, errors.New("generated tmux action has no arguments")
	}
	return words[index+1:], true, nil
}

// splitGeneratedShellWords decodes the quoting emitted by action.Quote. It
// performs no expansion, substitution, globbing, or operator interpretation.
func splitGeneratedShellWords(line string) ([]string, error) {
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

func environmentWith(key, value string) []string {
	prefix := key + "="
	environment := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		environment = append(environment, entry)
	}
	return append(environment, prefix+value)
}
