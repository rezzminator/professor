package harvestcli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/harvestmcp"
)

const (
	searchDefaultLimit = 8
	searchMaxLimit     = 25
)

// searchTypes are the values --type takes, the harvester_search_literature tool's type.
var searchTypes = []string{"any", "paper", "book"}

// newSearchService builds the harvester service whose literature search
// `pfm harvest search` runs; a test swaps it for one whose discovery sources
// reach a fixture.
var newSearchService = func(runtime harvestmcp.Runtime) (*harvestmcp.Service, error) {
	return harvestmcp.NewConfiguredHarvester("cli", runtime)
}

// runSearch is `pfm harvest search <query>...`: the harvester_search_literature
// tool's own search (Service.SearchLiterature) and text (RenderFind), so the
// CLI answers exactly what the tool answers — ranked candidates, each with its
// type and handle, and every discovery source's status.
func runSearch(args []string, stdout, stderr io.Writer, runtime config.Runtime) int {
	flags := cli.NewFlagSet(
		"harvest search",
		"usage: pfm harvest search [--type any|paper|book] [--limit N] [--json] <query>...",
		stderr,
	)
	workType := flags.String("type", "any", "the kind of work to find: any, paper or book")
	limit := flags.Int("limit", searchDefaultLimit, fmt.Sprintf("maximum candidates, 1 to %d", searchMaxLimit))
	jsonOutput := flags.Bool(jsonFlag, false, "print the machine-readable candidates and sources")
	words, code, ok := cli.ParseFlagsAnywhere(flags, args)
	if !ok {
		return code
	}
	query := strings.TrimSpace(strings.Join(words, " "))
	if query == "" {
		flags.Usage()
		return 2
	}
	if !slices.Contains(searchTypes, *workType) {
		fmt.Fprintf(
			stderr,
			"pfm harvest search: --type must be %s, got %q\n",
			strings.Join(searchTypes, ", "),
			*workType,
		)
		return 2
	}
	if *limit < 1 || *limit > searchMaxLimit {
		fmt.Fprintf(stderr, "pfm harvest search: --limit must be between 1 and %d, got %d\n", searchMaxLimit, *limit)
		return 2
	}
	service, err := newSearchService(harvesterRuntime(runtime))
	if err != nil {
		fmt.Fprintf(stderr, "pfm harvest search: configure: %v\n", err)
		return 1
	}
	found, err := service.SearchLiterature(
		context.Background(),
		harvestmcp.FindInput{Query: query, Limit: *limit, Type: *workType},
	)
	if closeErr := service.Close(); closeErr != nil {
		fmt.Fprintf(stderr, "pfm harvest search: close converter: %v\n", closeErr)
	}
	if err != nil {
		fmt.Fprintf(stderr, "pfm harvest search: %v\n", err)
		return 1
	}
	if *jsonOutput {
		encoded, err := json.MarshalIndent(found, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "pfm harvest search: encode result: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, string(encoded))
		return 0
	}
	fmt.Fprintln(stdout, harvestmcp.RenderFind(query, found))
	return 0
}
