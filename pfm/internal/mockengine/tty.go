package mockengine

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/term"
)

type keyKind int

const (
	keyRune keyKind = iota
	keyEnter
	keyEscape
	keyBackspace
	// keyStash is C-s, the keystroke pfm's reload sends to park a draft
	// (internal/reload/reload.go:290) before typing /exit.
	keyStash
)

type keyEvent struct {
	kind keyKind
	r    rune
}

// keyDecoder turns raw terminal bytes into key events. CSI sequences (arrows,
// function keys) are swallowed even when a read splits them; a lone ESC that
// ends a read is the Escape key, which is how tmux send-keys Escape arrives.
type keyDecoder struct {
	pending []byte
	inCSI   bool
}

func (decoder *keyDecoder) feed(input []byte) []keyEvent {
	events := make([]keyEvent, 0, len(input))
	buffer := append(append([]byte(nil), decoder.pending...), input...)
	decoder.pending = nil
	for index := 0; index < len(buffer); {
		if decoder.inCSI {
			final := buffer[index]
			index++
			if final >= 0x40 && final <= 0x7e {
				decoder.inCSI = false
			}
			continue
		}
		b := buffer[index]
		switch {
		case b == '\r' || b == '\n':
			events = append(events, keyEvent{kind: keyEnter})
			index++
		case b == 0x7f || b == 0x08:
			events = append(events, keyEvent{kind: keyBackspace})
			index++
		case b == 0x13:
			events = append(events, keyEvent{kind: keyStash})
			index++
		case b == 0x1b:
			if index+1 >= len(buffer) {
				events = append(events, keyEvent{kind: keyEscape})
				index++
				continue
			}
			if buffer[index+1] == '[' || buffer[index+1] == 'O' {
				decoder.inCSI = true
				index += 2
				continue
			}
			events = append(events, keyEvent{kind: keyEscape})
			index++
		case b < 0x20:
			index++
		default:
			r, size := utf8.DecodeRune(buffer[index:])
			if r == utf8.RuneError && size <= 1 {
				if !utf8.FullRune(buffer[index:]) {
					decoder.pending = append([]byte(nil), buffer[index:]...)
					return events
				}
				index++
				continue
			}
			events = append(events, keyEvent{kind: keyRune, r: r})
			index += size
		}
	}
	return events
}

// composer is the one-line draft every engine TUI edits.
type composer struct {
	draft   []rune
	stashed string
}

func (draft *composer) apply(event keyEvent) {
	switch event.kind {
	case keyRune:
		draft.draft = append(draft.draft, event.r)
	case keyBackspace:
		if len(draft.draft) > 0 {
			draft.draft = draft.draft[:len(draft.draft)-1]
		}
	case keyStash:
		draft.stashed = string(draft.draft)
		draft.draft = nil
	case keyEnter, keyEscape:
	}
}

func (draft *composer) text() string { return string(draft.draft) }

// take returns the draft and clears it; an empty draft returns "".
func (draft *composer) take() string {
	line := strings.TrimSpace(string(draft.draft))
	draft.draft = nil
	return line
}

// rawTerminal puts a terminal stdin into raw mode for the life of ctx and
// returns the restore function. A non-terminal stdin (a pipe in a test) is
// left alone: cooked bytes decode the same way.
func rawTerminal(stdin io.Reader, stderr io.Writer) (restore func(), err error) {
	file, ok := stdin.(*os.File)
	if !ok || !term.IsTerminal(file.Fd()) {
		return func() {}, nil
	}
	state, err := term.MakeRaw(file.Fd())
	if err != nil {
		return nil, fmt.Errorf("raw terminal: %w", err)
	}
	return func() {
		if err := term.Restore(file.Fd(), state); err != nil {
			fmt.Fprintf(stderr, "mock-engine: restore terminal: %v\n", err)
		}
	}, nil
}

// readKeys pumps stdin into a channel of key events until stdin closes or ctx
// ends. The channel closes on EOF so a TUI can tell "the pane went away".
func readKeys(ctx context.Context, stdin io.Reader) <-chan keyEvent {
	keys := make(chan keyEvent, 64)
	go func() {
		defer close(keys)
		var decoder keyDecoder
		buffer := make([]byte, 256)
		for {
			count, err := stdin.Read(buffer)
			for _, event := range decoder.feed(buffer[:count]) {
				select {
				case keys <- event:
				case <-ctx.Done():
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	return keys
}
