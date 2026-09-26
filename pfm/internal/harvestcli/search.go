package harvestcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/harvestmcp"
)

const (
	searchDefaultLimit = 8
	searchMaxLimit     = 25
	// searchToolName is the MCP tool `pfm harvest search` calls in process.
	searchToolName = "harvester_search_literature"
)

// searchTypes are the values --type takes, the harvester_search_literature tool's type.
var searchTypes = []string{"any", "paper", "book"}

// newSearchService builds the harvester service whose harvester_search_literature tool
// `pfm harvest search` calls; a test swaps it for one whose discovery sources
// reach a fixture.
var newSearchService = func(runtime harvestmcp.Runtime) (*harvestmcp.Service, error) {
	return harvestmcp.NewConfiguredHarvester("cli", runtime)
}

// runSearch is `pfm harvest search <query>...`: the harvester_search_literature tool,
// called in process over the MCP SDK so the CLI answers exactly what the tool
// answers — ranked candidates, each with its type and handle, and every
// discovery source's status.
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
	found, err := callSearch(context.Background(), service, query, *limit, *workType)
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
	fmt.Fprint(stdout, searchReceipt(query, found))
	return 0
}

// callSearch connects an in-memory MCP client to service and calls its
// harvester_search_literature tool; a tool error comes back as an error naming its text.
func callSearch(
	ctx context.Context,
	service *harvestmcp.Service,
	query string,
	limit int,
	workType string,
) (found harvestmcp.FindOutput, returnErr error) {
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := service.Server().Connect(ctx, serverTransport, nil)
	if err != nil {
		return found, fmt.Errorf("connect to the harvester: %w", err)
	}
	defer func() {
		if err := serverSession.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close the harvester session: %w", err))
		}
	}()
	client := mcp.NewClient(&mcp.Implementation{Name: "pfm harvest search", Version: "cli"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		return found, fmt.Errorf("connect a client to the harvester: %w", err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close the client session: %w", err))
		}
	}()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      searchToolName,
		Arguments: map[string]any{"query": query, "limit": limit, "type": workType},
	})
	if err != nil {
		return found, fmt.Errorf("call %s: %w", searchToolName, err)
	}
	if result.IsError {
		return found, errors.New(toolText(result))
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return found, fmt.Errorf("encode the %s result: %w", searchToolName, err)
	}
	if err := json.Unmarshal(encoded, &found); err != nil {
		return found, fmt.Errorf("decode the %s result %s: %w", searchToolName, encoded, err)
	}
	return found, nil
}

// toolText joins a tool result's text content, the words of a tool error.
func toolText(result *mcp.CallToolResult) string {
	parts := make([]string, 0, len(result.Content))
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	if len(parts) == 0 {
		return searchToolName + " failed without naming why"
	}
	return strings.Join(parts, "\n")
}

// searchReceipt prints each candidate — title, type, authors, year, open
// access, and the handle `pfm harvest` reads — then every source's status, so
// a failed source never reads as an empty answer.
func searchReceipt(query string, found harvestmcp.FindOutput) string {
	var out strings.Builder
	fmt.Fprintf(&out, "%d candidate(s) for %q\n", len(found.Candidates), query)
	for index, candidate := range found.Candidates {
		values := []string{"type: " + candidate.Type}
		if candidate.Authors != "" {
			values = append(values, "authors: "+candidate.Authors)
		}
		if candidate.Year != 0 {
			values = append(values, fmt.Sprintf("year: %d", candidate.Year))
		}
		values = append(values, fmt.Sprintf("open_access: %t", candidate.OpenAccess))
		title := candidate.Title
		if title == "" {
			title = "(untitled)"
		}
		fmt.Fprintf(
			&out,
			"%d. %s\n   %s\n   handle: %s\n",
			index+1,
			title,
			strings.Join(values, " / "),
			candidate.Handle,
		)
	}
	out.WriteString("sources:\n")
	for _, source := range found.Sources {
		fmt.Fprintf(&out, "  %s: %s, %d result(s)", source.Source, source.Status, source.Results)
		if source.Error != "" {
			fmt.Fprintf(&out, " — %s", source.Error)
		}
		out.WriteString("\n")
	}
	return out.String()
}
