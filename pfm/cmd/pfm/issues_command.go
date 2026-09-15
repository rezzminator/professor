package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"hostops/pfm/internal/shared"
)

// runIssues lists the servicedesk complaint box agents file through the
// issue_servicedesk MCP tool. It is the only read surface for that ledger: a
// write-only complaint box nobody can open is a black hole.
//
// The three states an operator can hit are kept visibly distinct:
//   - the store opened and has nothing pending -> "no open issues" on stdout,
//     exit 0.
//   - the store could not be opened or read -> an error on stderr, exit 1.
//     This never renders as "no open issues"; a failed look is not an empty
//     one.
//   - --json always emits a JSON array, even when it is empty, since a script
//     reading structured output needs `[]` rather than a prose sentence.
func runIssues(args []string, stdout, stderr io.Writer, runtime commandRuntime) (exitCode int) {
	flags := newFlagSet("issues", "usage: pfm issues [--all] [--json]", stderr)
	all := flags.Bool("all", false, "include closed issues, not only open ones")
	asJSON := flags.Bool(jsonFormat, false, "print issues as a JSON array")
	if code, ok := parseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 0 {
		flags.Usage()
		return 2
	}
	ctx := context.Background()
	state := shared.Open(ctx, runtime.Paths)
	defer func() { closeCommandResource(state, "pfm issues: close state", stderr, &exitCode) }()

	issues, err := state.Issues(ctx, *all)
	if err != nil {
		fmt.Fprintf(stderr, "pfm issues: %v\n", err)
		return 1
	}

	if *asJSON {
		encoded, err := json.MarshalIndent(issues, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "pfm issues: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "%s\n", encoded)
		return 0
	}

	if len(issues) == 0 {
		if *all {
			fmt.Fprintln(stdout, "no issues")
		} else {
			fmt.Fprintln(stdout, "no open issues")
		}
		return 0
	}

	for index := range issues {
		issue := &issues[index]
		reporter := issue.ReporterLabel
		if reporter == "" {
			reporter = issue.ReporterSession
		}
		fmt.Fprintf(
			stdout,
			"%d\t%s\t%s\t%s\t%s\t%s\t%s\n",
			issue.ID,
			time.Unix(0, issue.AtNS).UTC().Format(time.RFC3339),
			issue.Status,
			issue.Severity,
			issue.Area,
			reporter,
			issue.Title,
		)
	}
	return 0
}
