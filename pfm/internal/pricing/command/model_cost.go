package command

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/pricing/modelcost"
)

// ModelCost runs `pfm model-cost`: every invocation fetches both official
// pricing pages, so its output is current or it is an error.
func ModelCost(args []string, stdout, stderr io.Writer) int {
	return runModelCost(args, stdout, stderr, modelcost.NewFetcher(nil, nil))
}

func runModelCost(args []string, stdout, stderr io.Writer, fetcher *modelcost.Fetcher) int {
	const usage = "usage: pfm model-cost [--json] MODEL_ID | --all\nFetches the official Claude and OpenAI pricing pages on every run. Errors never return stale or zero prices."
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		fmt.Fprintln(stdout, usage)
		return 0
	}
	flags := cli.NewFlagSet("model-cost", usage, stderr)
	jsonOutput := flags.Bool("json", false, "compact JSON; default is indented JSON")
	all := flags.Bool("all", false, "return complete Claude and OpenAI catalogs, all published models")
	positional, code, ok := cli.ParseFlagsAnywhere(flags, args)
	if !ok {
		return code
	}
	input := modelcost.Input{All: *all}
	if len(positional) == 1 {
		input.Model = positional[0]
	}
	if len(positional) > 1 || (*all && input.Model != "") || (!*all && input.Model == "") {
		flags.Usage()
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	output, err := fetcher.Lookup(ctx, input)
	if err != nil {
		fmt.Fprintf(stderr, "pfm model-cost: %v\n", err)
		return 1
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	if !*jsonOutput {
		encoder.SetIndent("", "  ")
	}
	if err := encoder.Encode(output); err != nil {
		fmt.Fprintf(stderr, "pfm model-cost: write output: %v\n", err)
		return 1
	}
	return 0
}
