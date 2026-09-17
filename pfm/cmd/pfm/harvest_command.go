package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"hostops/pfm/internal/ask"
	"hostops/pfm/internal/cli"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/harvest"
	"hostops/pfm/internal/harvestmcp"
)

// retiredHarvesterServeFlags maps every flag the pre-config `pfm mcp harvester
// serve` accepted to where that setting lives now. A stale registration that
// still passes one fails loudly with the exact key, never silently.
var retiredHarvesterServeFlags = map[string]string{
	"user-agent":            "fetch.userAgent in harvester.config.json",
	"proxy-url":             "fetch.proxyURL in harvester.config.json",
	"host":                  "external.host in harvester.config.json (served by `pfm mcp serve`)",
	"port":                  "external.port in harvester.config.json (served by `pfm mcp serve`)",
	"public-url":            "external.publicURL in harvester.config.json (served by `pfm mcp serve`)",
	"internal-host":         "retired: the daemon's loopback port (mcp.http.port) is the internal gateway",
	"internal-port":         "retired: the daemon's loopback port (mcp.http.port) is the internal gateway",
	"allow-unauthenticated": "retired: the external gateway always authenticates (external.auth.*)",
	"ignore-robots-txt":     "retired: Harvester never consulted robots.txt",
}

// runHarvesterMCP serves the harvester over stdio for a client that launches
// it as a command. Both HTTP gateways — loopback and the authenticated
// external one — belong to the one daemon process, `pfm mcp serve`.
func runHarvesterMCP(args []string, _, stderr io.Writer, runtime commandRuntime) int {
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			continue
		}
		name, _, _ := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		if where, retired := retiredHarvesterServeFlags[name]; retired {
			fmt.Fprintf(stderr, "pfm mcp harvester serve: --%s is retired; %s\n", name, where)
			return 2
		}
	}
	flags := cli.NewFlagSet("mcp harvester serve", "usage: pfm mcp harvester serve [--transport stdio]", stderr)
	transport := flags.String(
		"transport",
		"stdio",
		"MCP transport (stdio only; HTTP gateways are served by `pfm mcp serve`)",
	)
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 0 {
		flags.Usage()
		return 2
	}
	if *transport != "stdio" {
		fmt.Fprintf(
			stderr,
			"pfm mcp harvester: --transport %q is retired; the loopback and authenticated external HTTP gateways are served by `pfm mcp serve` (harvester.config.json external.*)\n",
			*transport,
		)
		return 2
	}
	service, err := harvestmcp.NewConfiguredHarvester(version, harvestRuntime(runtime))
	if err != nil {
		fmt.Fprintf(stderr, "pfm mcp harvester: %v\n", err)
		return 1
	}
	defer func() {
		if closeErr := service.Close(); closeErr != nil {
			fmt.Fprintf(stderr, "pfm mcp harvester: close converter: %v\n", closeErr)
		}
	}()
	if err := service.RunStdio(context.Background(), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(stderr, "pfm mcp harvester: %v\n", err)
		return 1
	}
	return 0
}

// runHarvest is the command-line face of the same Harvester core served over
// MCP. Sources remain ordered: each result is printed in the order supplied.
func runHarvest(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) != 0 && args[0] == askAction {
		return runHarvestAsk(args[1:], stdout, stderr, runtime)
	}
	if askArgs, ok := harvestAskAlias(args); ok {
		return runHarvestAsk(askArgs, stdout, stderr, runtime)
	}
	flags := cli.NewFlagSet(
		"harvest",
		"usage: pfm harvest [--refresh] [--size-only] [--json] <url|doi|path>...",
		stderr,
	)
	refresh := flags.Bool("refresh", false, "bypass the cache and fetch fresh content")
	sizeOnly := flags.Bool("size-only", false, "fetch and cache content but print only size and cache path")
	jsonOutput := flags.Bool(jsonFormat, false, "print machine-readable result objects")
	sources, code, ok := cli.ParseFlagsAnywhere(flags, args)
	if !ok {
		return code
	}
	if len(sources) == 0 || len(sources) > 50 {
		flags.Usage()
		return 2
	}
	configured := harvestRuntime(runtime)
	harvester, err := harvestmcp.NewHarvester(configured)
	if err != nil {
		fmt.Fprintf(stderr, "pfm harvest: configure: %v\n", err)
		return 1
	}
	results := make([]harvest.Result, 0, len(sources))
	for _, source := range sources {
		result := harvester.FetchPublic(
			context.Background(),
			source,
			harvest.FetchOptions{Refresh: *refresh, SizeOnly: *sizeOnly},
		)
		results = append(results, result)
		if !*jsonOutput {
			fmt.Fprintln(stdout, renderHarvestCLI(result, *sizeOnly))
		}
	}
	if *jsonOutput {
		encoded, err := json.MarshalIndent(results, "", "  ")
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

// harvestAskAlias keeps the originally attempted `pfm harvest --ask -p ...`
// spelling useful while `pfm harvest ask -p ...` remains the canonical form.
func harvestAskAlias(args []string) ([]string, bool) {
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

func runHarvestAsk(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
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
	if strings.TrimSpace(*prompt) == "" || len(sources) == 0 || len(sources) > 50 {
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
	runner, err := ask.ResolveEngine(engineID, runtime.Config)
	if err != nil {
		fmt.Fprintf(stderr, "pfm harvest ask: resolve %s engine: %v\n", engineID, err)
		return 1
	}

	configured := harvestRuntime(runtime)
	harvester, err := harvestmcp.NewHarvester(configured)
	if err != nil {
		fmt.Fprintf(stderr, "pfm harvest ask: configure harvester: %v\n", err)
		return 1
	}
	files := make([]string, 0, len(sources))
	labels := make([]string, 0, len(sources))
	receiptDir := ""
	for index, source := range sources {
		result := harvester.FetchPublic(
			context.Background(),
			source,
			harvest.FetchOptions{Refresh: *refresh, SizeOnly: true},
		)
		path := result.Path
		if result.Error == "" && path != "" {
			path, err = filepath.Abs(path)
			if err != nil {
				result.Error = fmt.Sprintf("resolve cache path %s: %v", result.Path, err)
				result.ErrorKind = "cache_path"
			}
		} else if result.Error == "" {
			result.Error = "harvester returned success without a full cache path"
			result.ErrorKind = "cache_path"
		}
		if result.Error != "" {
			path, receiptDir, err = writeHarvestAskReceipt(runtime.Paths.Home, receiptDir, index, source, result)
			if err != nil {
				fmt.Fprintf(stderr, "pfm harvest ask: prepare receipt for %q: %v\n", source, err)
				if receiptDir != "" {
					if cleanupErr := os.RemoveAll(receiptDir); cleanupErr != nil {
						fmt.Fprintf(stderr, "pfm harvest ask: cleanup receipts %s: %v\n", receiptDir, cleanupErr)
					}
				}
				return 1
			}
		}
		files = append(files, path)
		labels = append(labels, source)
	}

	input, err := ask.ResolveInput(ask.AskInput{
		ContentFiles: files,
		SourceLabels: labels,
		Prompt:       *prompt,
		Engine:       engineID,
		Model:        *model,
		Effort:       *effort,
	}, runtime.Config)
	if err != nil {
		fmt.Fprintf(stderr, "pfm harvest ask: prepare model input: %v\n", err)
		if receiptDir != "" {
			if cleanupErr := os.RemoveAll(receiptDir); cleanupErr != nil {
				fmt.Fprintf(stderr, "pfm harvest ask: cleanup receipts %s: %v\n", receiptDir, cleanupErr)
			}
		}
		return 1
	}
	answer, runErr := runner.Run(context.Background(), input)
	cleanupErr := error(nil)
	if receiptDir != "" {
		cleanupErr = os.RemoveAll(receiptDir)
	}
	if runErr != nil {
		fmt.Fprintf(stderr, "pfm harvest ask: %v\n", runErr)
		if cleanupErr != nil {
			fmt.Fprintf(stderr, "pfm harvest ask: cleanup receipts %s: %v\n", receiptDir, cleanupErr)
		}
		return 1
	}
	if cleanupErr != nil {
		fmt.Fprintf(stderr, "pfm harvest ask: cleanup receipts %s: %v\n", receiptDir, cleanupErr)
		return 1
	}
	fmt.Fprintln(stdout, answer.Answer)
	if answer.Usage != nil {
		fmt.Fprintf(
			stderr,
			"pfm harvest ask: usage input=%d cached_input=%d output=%d\n",
			answer.Usage.Input,
			answer.Usage.CachedInput,
			answer.Usage.Output,
		)
	}
	return 0
}

func writeHarvestAskReceipt(
	home, receiptDir string,
	index int,
	source string,
	result harvest.Result,
) (string, string, error) {
	if receiptDir == "" {
		root := filepath.Join(home, ".local", "state", "pfm", "harvest-ask")
		if err := os.MkdirAll(root, 0o700); err != nil {
			return "", "", fmt.Errorf("create receipt root %s: %w", root, err)
		}
		var err error
		receiptDir, err = os.MkdirTemp(root, "run-")
		if err != nil {
			return "", "", fmt.Errorf("create receipt directory under %s: %w", root, err)
		}
	}
	payload, err := json.MarshalIndent(struct {
		Status string         `json:"status"`
		Input  string         `json:"input"`
		Result harvest.Result `json:"result"`
	}{Status: unavailableState, Input: source, Result: harvest.PublicFailure(source, result)}, "", "  ")
	if err != nil {
		return "", receiptDir, fmt.Errorf("encode receipt: %w", err)
	}
	path := filepath.Join(receiptDir, fmt.Sprintf("source-%03d.json", index+1))
	if err := os.WriteFile(path, append(payload, '\n'), 0o600); err != nil {
		return "", receiptDir, fmt.Errorf("write receipt %s: %w", path, err)
	}
	return path, receiptDir, nil
}

// harvestRuntime is harvester.config.json resolved into the MCP adapter's
// runtime — the ONE bridge between machine config and the harvester. Local
// callers (CLI, stdio, loopback daemon) keep the unconfined local-read surface
// subject to harvest.DenyLocalPath; the external gateway confines to exported artifacts.
func harvestRuntime(runtime commandRuntime) harvestmcp.Runtime {
	harvester := runtime.Config.Harvester
	return harvestmcp.Runtime{
		Home:                  runtime.Paths.Home,
		CacheDir:              harvester.Cache.Dir,
		UserAgent:             harvester.Fetch.UserAgent,
		ProxyURL:              harvester.Fetch.ProxyURL,
		SearXNGURL:            harvester.Search.SearXNGURL,
		BraveAPIKey:           harvester.Search.BraveAPIKey,
		DisableSearch:         !harvester.Search.Enabled,
		ContactEmail:          harvester.Scholarly.ContactEmail,
		GoogleBooksAPIKey:     harvester.Scholarly.GoogleBooksAPIKey,
		CoreAPIKey:            harvester.Scholarly.CoreAPIKey,
		SemanticScholarAPIKey: harvester.Scholarly.SemanticScholarAPIKey,
		DOIMirrorURL:          harvester.Scholarly.DOIMirrorURL,
		IPFSCatalogURL:        harvester.Scholarly.IPFSCatalogURL,
		DOIViewerURL:          harvester.Scholarly.DOIViewerURL,
		MD5CatalogURL:         harvester.Scholarly.MD5CatalogURL,
		GoogleScholarURL:      harvester.Scholarly.GoogleScholarURL,
		Browser:               harvester.Fetch.Browser,
		PDFOCR:                harvester.Convert.PDFOCR,
		PDFLayout:             harvester.Convert.PDFLayout,
		CacheTTL:              harvester.Cache.TTL,
		NegativeTTL:           harvester.Cache.NegativeTTL,
		NegativeTransientTTL:  harvester.Cache.NegativeTransientTTL,
		TTLsConfigured:        true,
		MaxInlineChars:        harvester.Output.MaxInlineChars,
	}
}

func renderHarvestCLI(result harvest.Result, sizeOnly bool) string {
	if result.Error != "" {
		return fmt.Sprintf("# %s\nERROR: %s", result.Source, result.Error)
	}
	if sizeOnly {
		return fmt.Sprintf(
			"source: %s\nsize: %d tokens / chars: %d / path: %s / cache_status: %s",
			result.Source,
			result.Tokens,
			result.Chars,
			result.Path,
			result.CacheStatus,
		)
	}
	return fmt.Sprintf(
		"# %s\ncache_status: %s / bytes: %d / tokens: %d / path: %s\n\n%s",
		result.Source,
		result.CacheStatus,
		result.Bytes,
		result.Tokens,
		result.Path,
		strings.TrimSpace(result.Content),
	)
}
