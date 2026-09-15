package headless

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/transcript"
)

// StreamOptions controls a follow.
type StreamOptions struct {
	// Filter keeps only the lines it matches. Empty streams everything.
	Filter *regexp.Regexp
	// Margin is how many lines of context accompany a match, on BOTH sides —
	// grep -C semantics, because a hit without what led to it is half an
	// answer.
	Margin int
	// FromStart replays the whole transcript before following. The default
	// starts at the end: a stream is for what happens next.
	FromStart bool
	// Follow keeps reading as the file grows. False drains once and returns.
	Follow bool
	// Poll is how often a quiet file is re-read.
	Poll time.Duration
	// Alive is asked whether the chat still exists whenever the file is quiet.
	// A stream that outlives its chat must END, not hang: silence is the one
	// thing a dead chat and a thinking chat have in common.
	Alive func() bool
	// Raw prints the entry's full text instead of the condensed line.
	Raw bool
}

// Stream follows a transcript and writes one line per entry. It returns when
// the context is cancelled, when the chat dies, or — with Follow false — when
// the file is drained.
func Stream(
	ctx context.Context,
	path string,
	engine pfmengine.ID,
	options StreamOptions,
	out io.Writer,
) (returnErr error) {
	if path == "" {
		return errors.New("this chat has no transcript to stream yet")
	}
	poll := options.Poll
	if poll <= 0 {
		poll = 500 * time.Millisecond
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() {
		if err := file.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close transcript %s: %w", path, err))
		}
	}()
	if !options.FromStart {
		if _, err := file.Seek(0, io.SeekEnd); err != nil {
			return err
		}
	}

	window := newContextWindow(options.Filter, options.Margin, out)
	reader := bufio.NewReaderSize(file, 64<<10)
	var pending []byte
	for {
		if err := ctx.Err(); err != nil {
			return window.flush()
		}
		line, readErr := reader.ReadBytes('\n')
		switch {
		case len(line) > 0 && readErr == nil:
			full := append(pending, line...)
			pending = nil
			if entry, ok := transcript.Parse(full, string(engine)); ok {
				if err := window.add(render(entry, options.Raw)); err != nil {
					return err
				}
			}
			continue
		case len(line) > 0:
			// A partial line: the engine is mid-write. Hold it and re-read.
			pending = append(pending, line...)
		}
		if readErr != nil && readErr != io.EOF {
			return readErr
		}
		if !options.Follow {
			return window.flush()
		}
		if options.Alive != nil && !options.Alive() {
			if err := window.flush(); err != nil {
				return err
			}
			return ErrChatGone
		}
		select {
		case <-ctx.Done():
			return window.flush()
		case <-time.After(poll):
		}
	}
}

// ErrChatGone ends a follow whose chat stopped existing.
var ErrChatGone = errors.New("the chat is gone")

func render(entry transcript.Entry, raw bool) string {
	if !raw {
		return transcript.Condensed(entry)
	}
	if entry.Role == transcript.RoleTool {
		return entry.Role + " " + entry.Tool + " " + entry.Input
	}
	return entry.Role + " " + entry.Text
}

// contextWindow implements grep -C over a stream: it holds back the last
// Margin lines so a match can be preceded by them, and keeps printing for
// Margin lines after one.
type contextWindow struct {
	filter *regexp.Regexp
	margin int
	out    io.Writer
	before []string
	after  int
	err    error
	// separated tracks whether lines were skipped since the last printed one,
	// so non-adjacent hits are visibly distinct — and printed marks whether
	// anything has been written at all, because a separator before the first
	// group would be a gap from nothing.
	separated bool
	printed   bool
}

func newContextWindow(filter *regexp.Regexp, margin int, out io.Writer) *contextWindow {
	if margin < 0 {
		margin = 0
	}
	return &contextWindow{filter: filter, margin: margin, out: out}
}

func (window *contextWindow) add(line string) error {
	if window.err != nil {
		return window.err
	}
	if line == "" {
		return nil
	}
	if window.filter == nil {
		return window.write(line)
	}
	if window.filter.MatchString(line) {
		if window.separated && window.printed {
			if err := window.write("--"); err != nil {
				return err
			}
		}
		for _, held := range window.before {
			if err := window.write(held); err != nil {
				return err
			}
		}
		window.before = nil
		window.separated = false
		window.after = window.margin
		return window.write(line)
	}
	if window.after > 0 {
		window.after--
		return window.write(line)
	}
	if window.margin > 0 {
		window.before = append(window.before, line)
		if len(window.before) > window.margin {
			window.before = window.before[1:]
			window.separated = true
		}
	}
	return nil
}

func (window *contextWindow) flush() error {
	return window.err
}

func (window *contextWindow) write(line string) error {
	if _, err := fmt.Fprintln(window.out, strings.TrimRight(line, "\n")); err != nil {
		window.err = err
		return err
	}
	window.printed = true
	return nil
}
