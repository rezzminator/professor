package headless

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"hostops/pfm/internal/ask"
	pfmconfig "hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/store"
)

const summaryPrompt = "Summarize this exchange in ≤ 40 words: what was asked, what was delivered or is still in flight."

type SummaryOptions struct {
	Config   pfmconfig.Config
	Database *store.Store
	TempDir  string
	Engine   pfmengine.ID
	Model    string
}

type SummaryResult struct {
	Text    string
	Cached  bool
	Warning error
}

// Summarize isolates one exchange, consults its exact-offset cache, and pays
// an ask runner only on a miss. Every failure becomes visible summary text so
// an unavailable or crashed engine can never look like an empty exchange.
func Summarize(ctx context.Context, chat Chat, options SummaryOptions) SummaryResult {
	if options.Database == nil {
		return failedSummary(fmt.Errorf("summary cache is not configured"))
	}
	exchange, ok, err := readLatestExchange(ctx, chat)
	if err != nil {
		return failedSummary(fmt.Errorf("read exchange: %w", err))
	}
	if !ok {
		return SummaryResult{Text: "unavailable (no human exchange)"}
	}
	if exchange.complete {
		cached, found, cacheErr := options.Database.ChatSummary(ctx, chat.Path, exchange.offset)
		if cacheErr != nil {
			return failedSummary(cacheErr)
		}
		if found {
			return SummaryResult{Text: cached, Cached: true}
		}
	}

	engineName := options.Engine
	if engineName == "" {
		engineName, err = options.Config.DefaultEngine()
		if err != nil {
			return failedSummary(err)
		}
	}
	runner, err := ask.ResolveEngine(engineName, options.Config)
	if err != nil {
		var missing *ask.BinaryMissingError
		if errors.As(err, &missing) {
			return SummaryResult{Text: fmt.Sprintf("unavailable (%s binary MISSING)", missing.Engine)}
		}
		return failedSummary(err)
	}

	prepared, err := writePreparedExchange(options.TempDir, exchange.prompt, exchange.response, exchange.complete)
	if err != nil {
		return failedSummary(err)
	}
	input, resolveErr := ask.ResolveInput(ask.AskInput{
		ContentFiles: []string{prepared},
		SourceLabels: []string{chat.Name + " last exchange"},
		Prompt:       summaryPrompt,
		Engine:       engineName,
		Model:        options.Model,
	}, options.Config)
	if !exchange.complete {
		input.Prompt += " The response is PARTIAL because the seat is still working."
	}
	if resolveErr != nil {
		removeErr := removePreparedFiles([]string{prepared})
		return failedSummary(errors.Join(resolveErr, removeErr))
	}
	answer, runErr := runner.Run(ctx, input)
	removeErr := removePreparedFiles([]string{prepared})
	if runErr != nil {
		return failedSummary(errors.Join(runErr, removeErr))
	}
	text := strings.Join(strings.Fields(answer.Answer), " ")
	if !exchange.complete {
		text = "PARTIAL: " + text
	}
	text = limitWords(text, 40)
	if exchange.complete {
		if err := options.Database.PutChatSummary(ctx, chat.Path, exchange.offset, text); err != nil {
			return failedSummary(errors.Join(err, removeErr))
		}
	}
	return SummaryResult{Text: text, Warning: removeErr}
}

func failedSummary(err error) SummaryResult {
	return SummaryResult{Text: failedResultText(err)}
}

func limitWords(value string, limit int) string {
	words := strings.Fields(value)
	if len(words) > limit {
		words = words[:limit]
	}
	return strings.Join(words, " ")
}
