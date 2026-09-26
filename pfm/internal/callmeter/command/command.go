// Package command owns `pfm callmeter`: the report action over the call
// store, its flags, the config-dir resolution and the chat-name
// lookup; cmd/pfm only hands it argv and the runtime it resolved.
package command

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/callmeter"
	"github.com/rezzminator/professor/pfm/internal/callmeter/report"
	pfmcli "github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

const usage = `usage: pfm callmeter report {files|writes|commands|context|sequences|faults} [--since D] [--project P]
                     [--agent-type T] [--session S] [--config-dir DIR] [--account N] [--limit N]
  --since D         a duration (7d, 24h) or a date (2026-09-01); default and floor: the 30-day retention window
  --config-dir DIR  one configured Claude config dir; default: every one the machine config names
  --account N       calls one configured account ran (its id); default: every call, those with no account too`

type topicFunc func(context.Context, *callmeter.Store, report.Filter, report.NameOf) (*report.Table, error)

var topics = map[string]topicFunc{
	"files": report.Files, "writes": report.Writes, "commands": report.Commands,
	"context": report.Context, "sequences": report.Sequences, "faults": report.Faults,
}

// CLI is `pfm callmeter {args}`: the reports over the call store the hook
// fills, over the home, transcript index and Claude config dirs runtime names. It returns the process exit code.
func CLI(args []string, stdout, stderr io.Writer, runtime pfmconfig.Runtime) int {
	ctx := context.Background()
	if len(args) > 0 {
		switch args[0] {
		case "report":
			return reportAction(ctx, args[1:], stdout, stderr, runtime)
		case "help", "-h", "--help":
			fmt.Fprintln(stdout, usage)
			return 0
		}
		fmt.Fprintf(stderr, "pfm callmeter: unknown action %q\n", args[0])
	}
	fmt.Fprintln(stderr, usage)
	return 2
}

type flagValues struct {
	since, configDir, account, project, agentType, session string
	limit                                                  int
}

func newFlags(name string, stderr io.Writer) (*flag.FlagSet, *flagValues) {
	flags := pfmcli.NewFlagSet(name, usage, stderr)
	values := &flagValues{}
	flags.StringVar(&values.since, "since", "", "a duration (7d, 24h) or a date (2026-09-01)")
	flags.StringVar(&values.configDir, "config-dir", "", "one configured Claude config dir")
	flags.StringVar(&values.account, "account", "", "calls this configured account id ran")
	flags.StringVar(&values.project, "project", "", "calls whose cwd is this dir or under it")
	flags.StringVar(&values.agentType, "agent-type", "", "calls made by this agent type")
	flags.StringVar(&values.session, "session", "", "calls in this session")
	flags.IntVar(&values.limit, "limit", report.DefaultLimit, "rows per table")
	return flags, values
}

func reportAction(
	ctx context.Context,
	args []string,
	stdout, stderr io.Writer,
	runtime pfmconfig.Runtime,
) (exitCode int) {
	flags, values := newFlags("callmeter report", stderr)
	positional, code, ok := pfmcli.ParseFlagsAnywhere(flags, args)
	if !ok {
		return code
	}
	var topic topicFunc
	if len(positional) == 1 {
		topic = topics[positional[0]]
	}
	if topic == nil {
		fmt.Fprintf(stderr, "pfm callmeter report: want one topic, got %q\n", positional)
		flags.Usage()
		return 2
	}
	now := clock.Real.Now()
	filter, code, ok := buildFilter(values, runtime.Config, now, stderr)
	if !ok {
		return code
	}
	path := callmeter.DefaultPath(runtime.Paths.Home)
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(stdout, "callmeter: no store at %s: nothing recorded yet\n", path)
		return 0
	}
	db, err := callmeter.OpenDB(ctx, path)
	if err != nil {
		fmt.Fprintf(stderr, "callmeter: cannot open store %s: %v\n", path, err)
		return 1
	}
	defer pfmcli.CloseResource(db, "callmeter: close store", stderr, &exitCode)
	names := &chatIndexNames{ctx: ctx, path: runtime.Paths.DB}
	defer names.close(stderr, &exitCode)
	if _, err := report.PruneExpired(ctx, db, now); err != nil {
		fmt.Fprintf(stderr, "callmeter: %v\n", err)
		return 1
	}
	if _, err := report.EnsureParsed(ctx, db, runtime.Paths.Home, nil); err != nil {
		fmt.Fprintf(stderr, "callmeter: parse commands: %v\n", err)
		return 1
	}
	table, err := topic(ctx, db, filter, names.nameOf)
	if err == nil {
		err = table.Render(stdout)
	}
	if err != nil {
		fmt.Fprintf(stderr, "callmeter: report %s: %v\n", positional[0], err)
		return 1
	}
	return 0
}

// buildFilter turns the flags into a report.Filter: --since clamped to the
// retention window (with one note), the config dirs resolved, --account checked, --project absolute.
func buildFilter(
	values *flagValues,
	config pfmconfig.Config,
	now time.Time,
	stderr io.Writer,
) (report.Filter, int, bool) {
	since, err := report.ParseSince(values.since, now)
	if err != nil {
		fmt.Fprintf(stderr, "pfm %v\n", err)
		return report.Filter{}, 2, false
	}
	if floor := now.Add(-report.Retention); since.Before(floor) {
		fmt.Fprintf(stderr, "callmeter: --since %s is older than the %d-day retention window; clamped to %s\n",
			values.since, int(report.Retention.Hours()/24), floor.UTC().Format(time.RFC3339))
		since = floor
	}
	dirs, err := configuredDirs(config, values.configDir)
	if err != nil {
		fmt.Fprintf(stderr, "callmeter: %v\n", err)
		return report.Filter{}, 2, false
	}
	account, err := accountFilter(config, values.account)
	if err != nil {
		fmt.Fprintf(stderr, "callmeter: %v\n", err)
		return report.Filter{}, 2, false
	}
	project := values.project
	if project != "" {
		if project, err = filepath.Abs(project); err != nil {
			fmt.Fprintf(stderr, "callmeter: --project %s: %v\n", values.project, err)
			return report.Filter{}, 2, false
		}
	}
	return report.Filter{
		Since: since, Project: project, AgentType: values.agentType, Session: values.session,
		ConfigDirs: dirs, Account: account, Limit: values.limit,
	}, 0, true
}

// configuredDirs lists every Claude account's config dir as a physical
// path, de-duplicated; narrow keeps only that one, and must be one of them.
func configuredDirs(config pfmconfig.Config, narrow string) ([]string, error) {
	dirs := []string{}
	seen := map[string]bool{}
	for _, account := range config.Accounts {
		dir, err := physicalDir(account.ConfigDir)
		if err != nil {
			return nil, err
		}
		if dir != "" && !seen[dir] {
			seen[dir] = true
			dirs = append(dirs, dir)
		}
	}
	if narrow == "" {
		return dirs, nil
	}
	dir, err := physicalDir(narrow)
	if err != nil {
		return nil, err
	}
	if !seen[dir] {
		return nil, fmt.Errorf("--config-dir %s is not a configured Claude config dir (configured: %s)",
			narrow, strings.Join(dirs, ", "))
	}
	return []string{dir}, nil
}

// accountFilter is --account as a report filter: nil when not given, and
// otherwise an id the machine config names.
func accountFilter(config pfmconfig.Config, value string) (*int, error) {
	if value == "" {
		return nil, nil
	}
	ids := make([]string, 0, len(config.Accounts))
	for _, account := range config.Accounts {
		ids = append(ids, strconv.Itoa(account.ID))
	}
	id, err := strconv.Atoi(value)
	if err == nil {
		for _, account := range config.Accounts {
			if account.ID == id {
				return &id, nil
			}
		}
	}
	return nil, fmt.Errorf("--account %s is not a configured account (configured: %s)", value, strings.Join(ids, ", "))
}

// physicalDir is the config dir the store names for dir
// (callmeter.ProjectsHome): accounts sharing one projects/ are one dir, so
// --config-dir with either account shows the same history the hook recorded.
// A dir not created yet keeps its absolute path.
func physicalDir(dir string) (string, error) {
	if dir == "" {
		return "", nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve config dir %s: %w", dir, err)
	}
	return callmeter.ProjectsHome(abs), nil
}
