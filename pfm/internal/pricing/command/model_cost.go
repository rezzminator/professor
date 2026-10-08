// Package command owns `pfm model-cost`: the one price table, refreshed from
// the publishers' pages at most once a day, printed whole or for one model.
package command

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/pricing"
	"github.com/rezzminator/professor/pfm/internal/pricing/modelcost"
)

const (
	usage = "usage: pfm model-cost [--json] [--force] MODEL_ID | --all | --check"
	help  = usage + `
Prints the one price table pfm owns, USD per million tokens: the clone's
pfm/internal/pricing/prices.json, refreshed from the official Claude and OpenAI
pricing pages at most once a day, with pfm.prices.json merged in.
  MODEL_ID  one model's row; a provider prefix or snapshot date resolves to its key
  --all     every row
  --check   the summary line only; exit 1 when the table cannot load
  --force   refresh now, whatever the table's age; exit 1 when the refresh fails
  --json    the table document instead of text
` + paths.EnvPricesOffline + `=1 never fetches.`
)

// ModelCost runs `pfm model-cost` for the machine.
func ModelCost(args []string, stdout, stderr io.Writer, runtime config.Runtime) int {
	return runModelCost(args, stdout, stderr, modelcost.Options{
		Home:       runtime.Paths.Home,
		ConfigPath: runtime.Config.Path,
		Offline:    paths.PricesOffline(),
		Clock:      clock.Real,
		Fetch:      modelcost.NewFetcher(nil, nil).Fetch,
	})
}

func runModelCost(args []string, stdout, stderr io.Writer, options modelcost.Options) int {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		fmt.Fprintln(stdout, help)
		return 0
	}
	flags := cli.NewFlagSet("model-cost", usage, stderr)
	jsonOutput := flags.Bool("json", false, "the table document instead of text")
	all := flags.Bool("all", false, "every row")
	check := flags.Bool("check", false, "the summary line only")
	force := flags.Bool("force", false, "refresh now, whatever the table's age")
	positional, code, ok := cli.ParseFlagsAnywhere(flags, args)
	if !ok {
		return code
	}
	modes := len(positional)
	for _, mode := range []bool{*all, *check} {
		if mode {
			modes++
		}
	}
	if len(positional) > 1 || modes != 1 || (*check && *jsonOutput) {
		flags.Usage()
		return 2
	}
	options.Force = *force
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	prices, result, err := modelcost.Refresh(ctx, options)
	if err != nil {
		if result.Err != nil {
			fmt.Fprintf(stderr, "pfm model-cost: warning: refresh failed: %v\n", result.Err)
		}
		fmt.Fprintf(stderr, "pfm model-cost: %v\n", err)
		return 1
	}
	warnRefresh(stderr, prices, result)
	if *force && result.Status == modelcost.StatusFailed {
		return 1
	}
	rows, model := prices.Rows, ""
	if len(positional) == 1 {
		model = positional[0]
		row, found := prices.Resolve(model)
		if !found {
			fmt.Fprintf(
				stderr,
				"pfm model-cost: %s is not priced in the table fetched %s; pfm model-cost --all lists every priced key\n",
				model,
				prices.FetchedAt,
			)
			return 1
		}
		rows = []pricing.Row{row}
	}
	if *jsonOutput {
		return writeDocument(stdout, stderr, prices, result, model, rows)
	}
	fmt.Fprintln(stdout, summary(prices, result))
	if *check {
		return 0
	}
	if err := writeTable(stdout, rows); err != nil {
		fmt.Fprintf(stderr, "pfm model-cost: write output: %v\n", err)
		return 1
	}
	return 0
}

// warnRefresh names every refresh or source problem on stderr: none is an absence.
func warnRefresh(stderr io.Writer, prices pricing.Prices, result modelcost.Result) {
	if result.Err != nil {
		fmt.Fprintf(stderr, "pfm model-cost: warning: refresh failed, serving the table fetched %s: %v\n",
			prices.FetchedAt, result.Err)
	}
	if result.PersistErr != nil {
		fmt.Fprintf(stderr, "pfm model-cost: warning: could not persist the refreshed prices: %v\n", result.PersistErr)
	}
	if result.StampErr != nil {
		fmt.Fprintf(stderr, "pfm model-cost: warning: price check stamp: %v\n", result.StampErr)
	}
	if prices.FileError != nil {
		fmt.Fprintf(stderr, "pfm model-cost: warning: the clone's prices file is unusable, serving the %s table: %v\n",
			prices.From, prices.FileError)
	}
}

func summary(prices pricing.Prices, result modelcost.Result) string {
	line := fmt.Sprintf("prices: %d rows · fetched %s", len(prices.Rows), prices.FetchedAt)
	if !result.CheckedAt.IsZero() {
		line += " · checked " + result.CheckedAt.UTC().Format(time.RFC3339)
	}
	from := prices.From
	if from == pricing.FromFile {
		from += " " + prices.File
	}
	line += " · from " + from + " · refresh " + result.Status
	if result.Status == modelcost.StatusRefreshed {
		detail := "unchanged"
		if result.Changed {
			detail = "changed"
		}
		if !result.Persisted {
			detail += ", not persisted"
		}
		line += " (" + detail + ")"
	}
	if prices.Override == nil {
		return line + " · override: none"
	}
	return line + fmt.Sprintf(" · override: %s (%d rows)", prices.Override.Path, prices.Override.Rows)
}

func rateCell(rate *float64) string {
	if rate == nil {
		return "-"
	}
	return strconv.FormatFloat(*rate, 'g', -1, 64)
}

func writeTable(stdout io.Writer, rows []pricing.Row) error {
	table := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "KEY\tENGINE\tIN\tOUT\tHIT\tCACHED\tW5M\tW1H\tLONG\tSOURCE")
	for _, row := range rows {
		long := "-"
		if row.Long != nil {
			long = ">" + strconv.FormatInt(row.Long.Above, 10)
		}
		fmt.Fprintln(table, strings.Join([]string{
			row.Key, row.Engine, rateCell(row.In), rateCell(row.Out), rateCell(row.Hit), rateCell(row.Cached),
			rateCell(row.W5m), rateCell(row.W1h), long, row.Source,
		}, "\t"))
	}
	return table.Flush()
}

type refreshDocument struct {
	Status       string `json:"status"`
	Error        string `json:"error,omitempty"`
	Changed      *bool  `json:"changed,omitempty"`
	Persisted    *bool  `json:"persisted,omitempty"`
	PersistError string `json:"persist_error,omitempty"`
	StampError   string `json:"stamp_error,omitempty"`
}

// document is the `pfm model-cost --json` document.
type document struct {
	Version    int               `json:"version"`
	FetchedAt  string            `json:"fetched_at"`
	CheckedAt  string            `json:"checked_at,omitempty"`
	Sources    []pricing.Source  `json:"sources"`
	ServedFrom string            `json:"served_from"`
	File       string            `json:"file,omitempty"`
	FileError  string            `json:"file_error,omitempty"`
	Refresh    refreshDocument   `json:"refresh"`
	Override   *pricing.Override `json:"override"`
	Model      string            `json:"model,omitempty"`
	Rows       []pricing.Row     `json:"rows"`
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func writeDocument(
	stdout, stderr io.Writer,
	prices pricing.Prices,
	result modelcost.Result,
	model string,
	rows []pricing.Row,
) int {
	doc := document{
		Version: prices.Version, FetchedAt: prices.FetchedAt, Sources: prices.Sources, ServedFrom: prices.From,
		File: prices.File, FileError: errorText(prices.FileError), Override: prices.Override, Model: model, Rows: rows,
		Refresh: refreshDocument{
			Status: result.Status, Error: errorText(result.Err), StampError: errorText(result.StampErr),
		},
	}
	if !result.CheckedAt.IsZero() {
		doc.CheckedAt = result.CheckedAt.UTC().Format(time.RFC3339Nano)
	}
	if result.Status == modelcost.StatusRefreshed {
		changed, persisted := result.Changed, result.Persisted
		doc.Refresh.Changed, doc.Refresh.Persisted = &changed, &persisted
		doc.Refresh.PersistError = errorText(result.PersistErr)
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(doc); err != nil {
		fmt.Fprintf(stderr, "pfm model-cost: write output: %v\n", err)
		return 1
	}
	return 0
}
