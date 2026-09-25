package headless

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/ask"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/inject"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

const askPrompt = "State this chat's CURRENT status in <= 40 words: what it is doing right now, and whether it is working, waiting on input, blocked, finished, or errored. Ground the answer in the live pane capture; use the last human exchange only as background context."

// AskOptions configures Ask. There is no Database field: unlike Summarize,
// Ask never caches (see Ask's doc comment for why).
type AskOptions struct {
	Config  pfmconfig.Config
	TempDir string
	Engine  pfmengine.ID
	Model   string
}

// AskResult is Ask's answer. There is no Cached flag: Ask never caches.
type AskResult struct {
	Text    string
	Warning error
}

// Ask answers what a chat is doing RIGHT NOW, grounded in its live tmux pane
// capture with its last human exchange as background context, and pays an
// ask runner exactly once per call.
//
// Ask deliberately never caches. Summarize's cache keys on transcript offset,
// which is correct there because a completed exchange never changes; a pane
// capture changes continuously, so the same offset can sit behind a
// completely different screen a second later, and a cached answer would be a
// confidently stale claim about a chat that has moved on.
//
// A probe that could not run never reads as "nothing found" (the engine's
// root law): a chat that is not live, or whose capture errors, states so
// explicitly in the returned text instead of silently falling back to a
// transcript-only answer presented as a full status.
func Ask(ctx context.Context, chat Chat, options AskOptions) (result AskResult) {
	var contentFiles, sourceLabels []string
	defer func() {
		if err := removePreparedFiles(contentFiles); err != nil {
			result.Warning = errors.Join(result.Warning, err)
		}
	}()

	var captureNote string
	if !chat.Live {
		captureNote = "chat is not live: there is no pane to capture"
	} else {
		capture, captureErr := capturePane(ctx, chat)
		if captureErr != nil {
			captureNote = "pane capture failed: " + flattenErrorText(captureErr)
		} else {
			capturePath, writeErr := writePreparedCapture(options.TempDir, chat, capture)
			if writeErr != nil {
				return failedAsk(writeErr)
			}
			contentFiles = append(contentFiles, capturePath)
			sourceLabels = append(sourceLabels, chat.Name+" live pane capture")
		}
	}

	var exchangeNote string
	exchangeComplete := true
	exchange, foundExchange, transcriptErr := readLatestExchange(ctx, chat)
	switch {
	case transcriptErr != nil:
		exchangeNote = "read exchange failed: " + flattenErrorText(transcriptErr)
	case foundExchange:
		exchangeComplete = exchange.complete
		exchangePath, writeErr := writePreparedExchange(
			options.TempDir,
			exchange.prompt,
			exchange.response,
			exchange.complete,
		)
		if writeErr != nil {
			return failedAsk(writeErr)
		}
		contentFiles = append(contentFiles, exchangePath)
		sourceLabels = append(sourceLabels, chat.Name+" last exchange")
	default:
		exchangeNote = "no human exchange recorded yet"
	}

	if len(contentFiles) == 0 {
		return AskResult{Text: fmt.Sprintf("unavailable (%s; %s)", captureNote, exchangeNote)}
	}

	engineName := options.Engine
	if engineName == "" {
		var err error
		engineName, err = options.Config.DefaultEngine()
		if err != nil {
			return failedAsk(err)
		}
	}
	runner, engineErr := ask.ResolveEngine(engineName, options.Config)
	if engineErr != nil {
		var missing *ask.BinaryMissingError
		if errors.As(engineErr, &missing) {
			return AskResult{Text: fmt.Sprintf("unavailable (%s binary MISSING)", missing.Engine)}
		}
		return failedAsk(engineErr)
	}

	prompt := askPrompt
	if !exchangeComplete {
		prompt += " The last exchange's response is PARTIAL because the seat is still working."
	}
	if captureNote != "" {
		prompt += " NOTE: " + captureNote + "; answer from the last exchange alone."
	}
	if exchangeNote != "" {
		prompt += " NOTE: " + exchangeNote + "."
	}

	input, resolveErr := ask.ResolveInput(ask.AskInput{
		ContentFiles: contentFiles,
		SourceLabels: sourceLabels,
		Prompt:       prompt,
		Engine:       engineName,
		Model:        options.Model,
	}, options.Config)
	if resolveErr != nil {
		return failedAsk(resolveErr)
	}
	answer, runErr := runner.Run(ctx, input)
	if runErr != nil {
		return failedAsk(runErr)
	}
	text := strings.Join(strings.Fields(answer.Answer), " ")
	switch {
	case captureNote != "":
		text = "TRANSCRIPT-ONLY (" + captureNote + "): " + text
	case exchangeNote != "":
		text = "PANE-ONLY (" + exchangeNote + "): " + text
	}
	text = limitWords(text, 40)
	return AskResult{Text: text}
}

// capturePane turns a live chat into a full-scrollback pane capture. This is
// the same socket-resolution and tmux-capture sequence runChatCapture uses
// in cmd/pfm's chat_command.go (chatSocketPath, then chat.Pane else
// chat.Session else chat.Socket as the target, then a styled
// inject.TmuxInjector.Capture over the full scrollback). internal/headless
// cannot import cmd/pfm (package main) to call chatSocketPath directly, so
// resolveChatSocketPath below mirrors its two-branch shape exactly instead of
// inventing a different resolution rule.
func capturePane(ctx context.Context, chat Chat) (string, error) {
	socketPath, err := paths.SocketPath(chat.Socket)
	if err != nil {
		return "", fmt.Errorf("resolve socket path: %w", err)
	}
	target := chat.Pane
	if target == "" {
		target = chat.Session
	}
	if target == "" {
		target = chat.Socket
	}
	capture, err := (inject.TmuxInjector{}).Capture(ctx, socketPath, target, true, inject.FullScrollback)
	if err != nil {
		return "", fmt.Errorf("capture pane: %w", err)
	}
	return capture, nil
}

// writePreparedCapture mirrors writePreparedExchange's temp-file approach: a
// labeled, disposable file the caller removes on every path.
func writePreparedCapture(directory string, chat Chat, capture string) (string, error) {
	var content strings.Builder
	content.WriteString("LIVE PANE CAPTURE (" + chat.Name + ")\n")
	content.WriteString(capture)
	if !strings.HasSuffix(capture, "\n") {
		content.WriteByte('\n')
	}
	return writePreparedFile(directory, "capture", paths.SIDCaptureScratchPattern, content.String())
}

func failedAsk(err error) AskResult {
	return AskResult{Text: failedResultText(err)}
}

func flattenErrorText(err error) string {
	if err == nil {
		return ""
	}
	detail := strings.Join(strings.Fields(err.Error()), " ")
	return transcript.Truncate(detail, transcript.TextCap)
}
