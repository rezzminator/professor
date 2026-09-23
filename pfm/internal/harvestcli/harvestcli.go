// Package harvestcli owns the `pfm harvest` verbs — the universal read, `ask`
// and `download` — as the command-line face of the same Harvester core the MCP
// tools serve; cmd/pfm keeps only the dispatch into Harvest.
package harvestcli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/harvest"
	"github.com/rezzminator/professor/pfm/internal/harvestmcp"
)

const (
	askVerb      = "ask"
	downloadVerb = "download"
	jsonFlag     = "json"
	maxSources   = 50
)

// newHarvester builds the configured harvester every verb reads through; a
// test swaps it for one whose transports reach a local fixture server.
var newHarvester = harvestmcp.NewHarvester

// Harvest dispatches `pfm harvest`: `ask` (and its `--ask` alias), `download`, and
// otherwise the universal read of each source, printed in the order supplied.
func Harvest(args []string, stdout, stderr io.Writer, runtime config.Runtime) int {
	if len(args) != 0 && args[0] == askVerb {
		return runAsk(args[1:], stdout, stderr, runtime)
	}
	if askArgs, ok := askAlias(args); ok {
		return runAsk(askArgs, stdout, stderr, runtime)
	}
	if len(args) != 0 && args[0] == downloadVerb {
		return runDownload(args[1:], stdout, stderr, runtime)
	}
	return runRead(args, stdout, stderr, runtime)
}

// harvesterRuntime resolves this machine's harvester config into the MCP
// adapter's runtime (harvestmcp.RuntimeFromConfig).
func harvesterRuntime(runtime config.Runtime) harvestmcp.Runtime {
	return harvestmcp.RuntimeFromConfig(runtime.Paths.Home, runtime.Config.Harvester)
}

// runRead is the universal read: a URL, DOI, ISBN or local path routed the
// way the tools route it.
func runRead(args []string, stdout, stderr io.Writer, runtime config.Runtime) int {
	flags := cli.NewFlagSet(
		"harvest",
		"usage: pfm harvest [--refresh] [--size-only] [--ocr-lang latin|zh|ja|ar|ru|he] [--json] [--header 'Name: value']... <url|doi|path>...",
		stderr,
	)
	refresh := flags.Bool("refresh", false, "bypass the cache and fetch fresh content")
	sizeOnly := flags.Bool("size-only", false, "fetch and cache content but print only size and cache path")
	ocrLangFlag := flags.String(
		"ocr-lang",
		"",
		"script to OCR a scanned document in (latin, zh, ja, ar, ru, he); a fresh read",
	)
	jsonOutput := flags.Bool(jsonFlag, false, "print machine-readable result objects")
	headers := harvest.HeaderFlag(flags)
	sources, code, ok := cli.ParseFlagsAnywhere(flags, args)
	if !ok {
		return code
	}
	if len(sources) == 0 || len(sources) > maxSources {
		flags.Usage()
		return 2
	}
	ocrLang, err := harvest.ParseOCRLang(*ocrLangFlag)
	if err != nil {
		fmt.Fprintf(stderr, "pfm harvest: %v\n", err)
		return 2
	}
	harvester, err := newHarvester(harvesterRuntime(runtime))
	if err != nil {
		fmt.Fprintf(stderr, "pfm harvest: configure: %v\n", err)
		return 1
	}
	results := make([]harvest.Result, 0, len(sources))
	for _, source := range sources {
		result := headers.Fetch(
			context.Background(),
			harvester,
			source,
			harvest.FetchOptions{Refresh: *refresh, SizeOnly: *sizeOnly, OCRLang: ocrLang},
		)
		results = append(results, result)
		if !*jsonOutput {
			fmt.Fprintln(stdout, renderRead(result, *sizeOnly))
		}
	}
	if *jsonOutput {
		encoded, err := json.MarshalIndent(harvest.JSONResults(results), "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "pfm harvest: encode results: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, string(encoded))
	}
	for index := range results {
		result := &results[index]
		if result.Error != "" {
			return 1
		}
	}
	return 0
}

// partialSuffix names a known-incomplete artifact in every receipt.
func partialSuffix(partial string) string {
	if partial == "" {
		return ""
	}
	return " / PARTIAL: " + partial
}

func renderRead(result harvest.Result, sizeOnly bool) string {
	if result.Error != "" {
		return fmt.Sprintf("# %s\nERROR: %s", result.Source, result.Error)
	}
	partial := partialSuffix(result.Partial) // the size probe's receipt says so too
	if sizeOnly {
		return fmt.Sprintf(
			"source: %s\nsize: %d tokens / chars: %d / path: %s / cache_status: %s%s",
			result.Source,
			result.Tokens,
			result.Chars,
			result.Path,
			result.CacheStatus,
			partial,
		)
	}
	return fmt.Sprintf(
		"# %s\ncache_status: %s / bytes: %d / tokens: %d / path: %s%s\n\n%s",
		result.Source,
		result.CacheStatus,
		result.Bytes,
		result.Tokens,
		result.Path,
		partial,
		strings.TrimSpace(result.Content),
	)
}
