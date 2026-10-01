// Package command owns `pfm price`: the effective model price table, printed
// as text, as the --json document token-audit reads, or validated by --check;
// cmd/pfm only hands it argv and the runtime it resolved.
package command

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

	pfmcli "github.com/rezzminator/professor/pfm/internal/cli"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/pricing"
)

const usage = "usage: pfm price [--json | --check]"

// Price is `pfm price {args}`: it reads the price table and writes nothing. It
// returns 0, 1 when the table fails to load or validate, and 2 on usage.
func Price(args []string, stdout, stderr io.Writer, runtime pfmconfig.Runtime) int {
	if len(args) == 1 {
		switch args[0] {
		case "help", "-h", "--help":
			fmt.Fprintln(stdout, usage)
			return 0
		}
	}
	flags := pfmcli.NewFlagSet("price", usage, stderr)
	asJSON := flags.Bool("json", false, "print the table document token-audit reads")
	check := flags.Bool("check", false, "validate the table")
	if code, ok := pfmcli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "pfm price: unexpected argument %q\n", flags.Arg(0))
		fmt.Fprintln(stderr, usage)
		return 2
	}
	if *asJSON && *check {
		fmt.Fprintln(stderr, "pfm price: --json and --check are exclusive")
		fmt.Fprintln(stderr, usage)
		return 2
	}
	table, err := pricing.Effective(runtime.Config.Path)
	if err != nil {
		fmt.Fprintf(stderr, "pfm price: %v\n", err)
		return 1
	}
	var out []byte
	switch {
	case *asJSON:
		out, err = table.JSON()
	case *check:
		out = fmt.Appendf(nil, "price table: ok · %d rows · override: %s\n",
			len(table.Rows), overrideLabel(table.Override, ""))
	default:
		out, err = textTable(table, runtime.Config.Path)
	}
	if err != nil {
		fmt.Fprintf(stderr, "pfm price: %v\n", err)
		return 1
	}
	if _, err := stdout.Write(out); err != nil {
		fmt.Fprintf(stderr, "pfm price: %v\n", err)
		return 1
	}
	return 0
}

// overrideLabel is `none`, or `none (no {prices path})` when the path is
// known, or `{abs path} ({o} rows)` for an active override.
func overrideLabel(override *pricing.Override, missingPath string) string {
	if override != nil {
		return fmt.Sprintf("%s (%d rows)", override.Path, override.Rows)
	}
	if missingPath == "" {
		return "none"
	}
	return fmt.Sprintf("none (no %s)", missingPath)
}

// textTable is the header line and the column-aligned rows, in table order.
func textTable(table pricing.Table, pfmConfigPath string) ([]byte, error) {
	missingPath := ""
	if pfmConfigPath != "" {
		missingPath = pfmconfig.PricesPath(pfmConfigPath)
	}
	var out bytes.Buffer
	fmt.Fprintf(&out, "price table: %d rows · override: %s\n",
		len(table.Rows), overrideLabel(table.Override, missingPath))
	writer := tabwriter.NewWriter(&out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "KEY\tENGINE\tMATCH\tIN\tOUT\tHIT\tCACHED\tW5M\tW1H\tLONG\tSOURCE")
	for i := range table.Rows {
		row := &table.Rows[i]
		claude := row.Engine == pricing.EngineClaude
		fmt.Fprintln(writer, strings.Join([]string{
			row.Key, row.Engine, strings.Join(row.Match, ","),
			rateCell(row.In, true), rateCell(row.Out, true),
			rateCell(row.Hit, claude), rateCell(row.Cached, !claude),
			rateCell(row.W5m, claude), rateCell(row.W1h, claude),
			rateCell(row.LongIn, true) + "x/" + rateCell(row.LongOut, true) + "x",
			row.Source,
		}, "\t"))
	}
	if err := writer.Flush(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// rateCell prints a rate of the row's engine, and `-` for a foreign column.
func rateCell(value float64, owned bool) string {
	if !owned {
		return "-"
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
}
