// Package harvestcli owns the `pfm harvest` verbs — the universal read, `ask`,
// `download-file` and `search` — as the command-line face of the same
// Harvester core the MCP tools serve; cmd/pfm keeps only the dispatch into
// Harvest.
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
	askVerb          = "ask"
	downloadFileVerb = "download-file"
	searchVerb       = "search"
	jsonFlag         = "json"
	maxSources       = 50
	readUsage        = "usage: pfm harvest [--refresh] [--include-content=false] [--ocr-language latin|zh|ja|ar|ru|he] [--json] [--header 'Name: value']... <url|path|identifier>...\n" +
		"       pfm harvest download-file [--json] [--header 'Name: value']... <url>...\n" +
		"       pfm harvest search [--type any|paper|book] [--limit N] [--json] <query>...\n" +
		"       pfm harvest ask -p <prompt> [--engine claude|codex] [--model MODEL] [--effort EFFORT] [--refresh] <url|path|identifier>..."
)

// newHarvester builds the configured harvester every verb reads through; a
// test swaps it for one whose transports reach a local fixture server.
var newHarvester = harvestmcp.NewHarvester

// Harvest dispatches `pfm harvest`: `ask` (and its `--ask` alias),
// `download-file`, `search`, and otherwise the universal read of each source,
// printed in the order supplied. The retired `download` verb is a named usage
// error, never a source to read.
func Harvest(args []string, stdout, stderr io.Writer, runtime config.Runtime) int {
	if len(args) != 0 {
		switch args[0] {
		case askVerb:
			return runAsk(args[1:], stdout, stderr, runtime)
		case downloadFileVerb:
			return runDownload(args[1:], stdout, stderr, runtime)
		case searchVerb:
			return runSearch(args[1:], stdout, stderr, runtime)
		case "download":
			fmt.Fprintf(
				stderr,
				"pfm harvest: unknown verb %q; a file's bytes come from `pfm harvest download-file <url>...`\n",
				args[0],
			)
			return 2
		}
	}
	if askArgs, ok := askAlias(args); ok {
		return runAsk(askArgs, stdout, stderr, runtime)
	}
	return runRead(args, stdout, stderr, runtime)
}

// harvesterRuntime resolves this machine's harvester config into the MCP
// adapter's runtime (harvestmcp.RuntimeFromConfig).
func harvesterRuntime(runtime config.Runtime) harvestmcp.Runtime {
	return harvestmcp.RuntimeFromConfig(runtime.Paths.Home, runtime.Config.Harvester)
}

// runRead is the universal read: a URL, local path or identifier routed the
// way the read tool routes it.
func runRead(args []string, stdout, stderr io.Writer, runtime config.Runtime) int {
	flags := cli.NewFlagSet("harvest", readUsage, stderr)
	refresh := flags.Bool("refresh", false, "bypass the cache and fetch fresh content")
	includeContent := flags.Bool(
		"include-content",
		true,
		"print each source's content; false reads and caches it and prints only its size and cache path",
	)
	ocrLanguageFlag := flags.String(
		"ocr-language",
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
	ocrLanguage, err := harvest.ParseOCRLang(*ocrLanguageFlag)
	if err != nil {
		fmt.Fprintf(stderr, "pfm harvest: --ocr-language: %v\n", err)
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
			harvest.FetchOptions{Refresh: *refresh, SizeOnly: !*includeContent, OCRLang: ocrLanguage},
		)
		results = append(results, result)
		if !*jsonOutput {
			fmt.Fprintln(stdout, renderRead(result, *includeContent))
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

// gapsSuffix names every known gap of an incomplete artifact in each receipt.
func gapsSuffix(gaps []string) string {
	if len(gaps) == 0 {
		return ""
	}
	return " / gaps: " + strings.Join(gaps, "; ")
}

func renderRead(result harvest.Result, includeContent bool) string {
	if result.Error != "" {
		return fmt.Sprintf("# %s\nERROR: %s", result.Source, result.Error)
	}
	gaps := gapsSuffix(harvest.PublicGaps(result.Partial)) // the size receipt says so too
	if !includeContent {
		return fmt.Sprintf(
			"source: %s\ntokens: %d / chars: %d / path: %s / cached: %t%s",
			result.Source,
			result.Tokens,
			result.Chars,
			result.Path,
			harvest.Cached(result),
			gaps,
		)
	}
	return fmt.Sprintf(
		"# %s\ncached: %t / bytes: %d / tokens: %d / path: %s%s\n\n%s",
		result.Source,
		harvest.Cached(result),
		result.Bytes,
		result.Tokens,
		result.Path,
		gaps,
		strings.TrimSpace(result.Content),
	)
}
