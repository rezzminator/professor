package harvestcli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/ask"
	"github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/harvest"
	"github.com/rezzminator/professor/pfm/internal/harvestmcp"
	"github.com/rezzminator/professor/pfm/internal/pricing"
)

// askAlias keeps the originally attempted `pfm harvest --ask -p ...`
// spelling useful while `pfm harvest ask -p ...` remains the canonical form.
func askAlias(args []string) ([]string, bool) {
	for index, argument := range args {
		if argument != "--ask" {
			continue
		}
		result := make([]string, 0, len(args)-1)
		result = append(result, args[:index]...)
		result = append(result, args[index+1:]...)
		return result, true
	}
	return nil, false
}

func runAsk(args []string, stdout, stderr io.Writer, runtime config.Runtime) int {
	flags := cli.NewFlagSet(
		"harvest ask",
		"usage: pfm harvest ask -p <prompt> [--engine claude|codex] [--model MODEL] [--effort EFFORT] [--refresh] <url|doi|path>...",
		stderr,
	)
	prompt := flags.String("prompt", "", "question answered only from the harvested sources")
	flags.StringVar(prompt, "p", "", "question answered only from the harvested sources")
	engineName := flags.String("engine", "", "ask engine (claude or codex; default from config)")
	model := flags.String("model", "", "override the configured ask model")
	effort := flags.String("effort", "", "override the configured reasoning effort")
	refresh := flags.Bool("refresh", false, "bypass the harvest cache")
	sources, code, ok := cli.ParseFlagsAnywhere(flags, args)
	if !ok {
		return code
	}
	if strings.TrimSpace(*prompt) == "" || len(sources) == 0 || len(sources) > maxSources {
		flags.Usage()
		return 2
	}

	engineID := pfmengine.ID("")
	var err error
	if strings.TrimSpace(*engineName) != "" {
		engineID, err = pfmengine.Parse(*engineName)
	} else {
		engineID, err = runtime.Config.DefaultEngine()
	}
	if err != nil {
		fmt.Fprintf(stderr, "pfm harvest ask: resolve engine: %v\n", err)
		return 2
	}
	if strings.TrimSpace(*model) != "" {
		prices, err := pricing.LoadPrices(runtime.Paths.Home, runtime.Config.Path)
		if err != nil {
			fmt.Fprintf(
				stderr,
				"pfm harvest ask: model %q: load the price table that names the models: %v\n",
				*model,
				err,
			)
			return 1
		}
		// The model launched is the one checked: trimmed, an alias lower-cased.
		*model, err = ask.CheckModel(engineID, *model, prices.Table)
		if err != nil {
			fmt.Fprintf(stderr, "pfm harvest ask: %v\n", err)
			return 2
		}
	}

	runner, err := ask.ResolveEngine(engineID, runtime.Config)
	if err != nil {
		fmt.Fprintf(stderr, "pfm harvest ask: resolve %s engine: %v\n", engineID, err)
		return 1
	}

	harvester, err := newHarvester(harvesterRuntime(runtime))
	if err != nil {
		fmt.Fprintf(stderr, "pfm harvest ask: configure harvester: %v\n", err)
		return 1
	}
	results := make([]harvest.Result, 0, len(sources))
	for _, source := range sources {
		results = append(results, harvester.FetchPublic(
			context.Background(),
			source,
			harvest.FetchOptions{Refresh: *refresh, SizeOnly: true},
		))
	}
	answer, err := harvestmcp.AskOver(context.Background(), runtime.Config, runtime.Paths.Home, runner, ask.AskInput{
		Prompt: *prompt, Engine: engineID, Model: *model, Effort: *effort,
	}, sources, results)
	if err != nil {
		fmt.Fprintf(stderr, "pfm harvest ask: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, answer.Answer)
	if answer.Usage != nil {
		fmt.Fprintf(
			stderr,
			"pfm harvest ask: usage input=%d cached_input=%d cache_creation=%d output=%d\n",
			answer.Usage.Input,
			answer.Usage.CachedInput,
			answer.Usage.CacheCreation,
			answer.Usage.Output,
		)
	} else {
		fmt.Fprintln(stderr, "pfm harvest ask: usage unknown: the engine reported no token counts")
	}
	return 0
}
