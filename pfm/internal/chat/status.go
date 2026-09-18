package chat

import (
	"context"
	"fmt"
	"io"

	"hostops/pfm/internal/clock"
	pfmconfig "hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/headless"
	"hostops/pfm/internal/store"
)

// StatusRequest selects one chat and the optional answers to attach.
type StatusRequest struct {
	Target string
	// Summary summarizes the last exchange (cached by transcript offset).
	Summary bool
	// Ask answers the chat's current status from its live pane capture and
	// last exchange — never cached, since a pane changes continuously.
	Ask bool
	// Engine and Model override the configured ask runner for Summary/Ask.
	Engine pfmengine.ID
	Model  string
}

// Status inspects the target. A dead chat is a status, not an error: the
// caller reads status.Alive(). Summary-cache warnings go to warnings.
func Status(
	ctx context.Context,
	runtime *pfmconfig.Runtime,
	request StatusRequest,
	warnings io.Writer,
) (headless.Status, error) {
	target, err := Target(ctx, request.Target, runtime)
	if err != nil {
		return headless.Status{}, err
	}
	status, err := headless.Inspect(ctx, target, clock.Real.Now())
	if err != nil {
		return headless.Status{}, err
	}
	if !request.Summary && !request.Ask {
		return status, nil
	}
	machine, err := pfmconfig.RuntimeOrDefault(runtime)
	if err != nil {
		return headless.Status{}, err
	}
	if request.Summary {
		database, err := store.Open(store.WithWarningWriter(warnings))
		if err != nil {
			return headless.Status{}, err
		}
		summary := headless.Summarize(ctx, target, headless.SummaryOptions{
			Config: machine.Config, Database: database,
			Engine: request.Engine, Model: request.Model,
		})
		status.Summary = summary.Text
		status.SummaryCached = summary.Cached
		if summary.Warning != nil && warnings != nil {
			fmt.Fprintf(warnings, "pfm chat status: summary cleanup warning: %v\n", summary.Warning)
		}
		if err := database.Close(); err != nil {
			return headless.Status{}, fmt.Errorf("close summary cache: %w", err)
		}
	}
	if request.Ask {
		// No cache: a pane changes continuously, so Ask pays an ask runner on
		// every call instead of consulting Summarize's exact-offset cache,
		// which would serve a confidently stale answer about a chat that has
		// moved on.
		answer := headless.Ask(ctx, target, headless.AskOptions{
			Config: machine.Config,
			Engine: request.Engine, Model: request.Model,
		})
		status.Ask = answer.Text
		if answer.Warning != nil && warnings != nil {
			fmt.Fprintf(warnings, "pfm chat status: ask cleanup warning: %v\n", answer.Warning)
		}
	}
	return status, nil
}
