package call

import (
	"context"
	"strings"
	"unicode"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// herdrRawCapture projects OpenCode's box onto the shared composer guard.
// The transcript and footer stay intact for busy and receipt checks.
func herdrRawCapture(ctx context.Context, target Target, format string) (string, string, error) {
	screen, pane, err := herdrCaptureFormat(ctx, target, herdrRecentLines, format)
	if err != nil {
		return "", pane, err
	}
	if target.Engine == string(pfmengine.OpenCode) {
		screen = openCodeComposer(screen)
	}
	return screen, pane, nil
}

// OpenCode 1.14.30 draws contiguous left-border rows, a model row, and
// a bottom border. Never infer an empty box from a missing or partial frame.
func openCodeComposer(screen string) string {
	lines := strings.Split(screen, "\n")
	for end := len(lines) - 2; end >= 3; end-- {
		if !strings.HasPrefix(strings.TrimSpace(stripTerminalControl(lines[end])), "╹▀") {
			continue
		}
		model := strings.TrimSpace(stripTerminalControl(lines[end-1]))
		if !strings.HasPrefix(model, "┃") || !strings.Contains(model, " · ") {
			return screen
		}
		start := end - 2
		for start >= 0 && strings.HasPrefix(strings.TrimSpace(stripTerminalControl(lines[start])), "┃") {
			start--
		}
		start++
		if end-start < 4 {
			return screen
		}
		var body []string
		for _, line := range lines[start : end-1] {
			_, text, _ := strings.Cut(line, "┃")
			if strings.TrimSpace(stripTerminalControl(text)) != "" {
				body = append(body, text)
			}
		}
		draft := strings.Join(body, " ")
		plain := strings.TrimSpace(stripTerminalControl(draft))
		// Hints use textMuted, not SGR dim. Compare to the commands hint in
		// this capture, never a hardcoded theme colour. A typed lookalike uses
		// the foreground; indistinguishable themes fail closed.
		if len(body) == 1 && strings.HasPrefix(plain, "Ask anything...") {
			footer := lines[end+1]
			muted := openCodeWordColor(footer, "commands")
			foreground := openCodeWordColor(footer, "ctrl+p")
			if muted != "" && foreground != "" && muted != foreground && openCodeWordColor(draft, plain) == muted {
				draft = "\x1b[2m" + plain + "\x1b[0m"
			}
		}
		return strings.Join(lines[:start], "\n") + "\n" + strings.Join(lines[end:], "\n") + "\n❯ " + draft
	}
	return screen
}

// Read only the explicitly rendered foreground of every nonspace rune in
// word. Unsupported or mixed styling is not evidence of a placeholder.
func openCodeWordColor(styled, word string) string {
	var text strings.Builder
	var colors []string
	color := ""
	for styled != "" {
		match := ansiPattern.FindStringIndex(styled)
		end := len(styled)
		if match != nil {
			end = match[0]
		}
		for _, r := range styled[:end] {
			text.WriteRune(r)
			for range len(string(r)) {
				colors = append(colors, color)
			}
		}
		if match == nil {
			break
		}
		sequence := styled[match[0]:match[1]]
		switch {
		case sequence == "\x1b[0m", sequence == "\x1b[m", sequence == "\x1b[39m":
			color = ""
		case strings.HasPrefix(sequence, "\x1b[38;") && strings.HasSuffix(sequence, "m"):
			color = sequence
		case strings.HasPrefix(sequence, "\x1b[48;") && strings.HasSuffix(sequence, "m"):
			// Background-only changes preserve foreground.
		default:
			color = ""
		}
		styled = styled[match[1]:]
	}
	start := strings.Index(text.String(), word)
	if start < 0 {
		return ""
	}
	want := ""
	for i, r := range word {
		if unicode.IsSpace(r) {
			continue
		}
		got := colors[start+i]
		if got == "" || (want != "" && got != want) {
			return ""
		}
		want = got
	}
	return want
}
