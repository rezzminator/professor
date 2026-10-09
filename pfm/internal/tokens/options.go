package tokens

import (
	"bytes"
	"errors"
	"flag"
	"regexp"
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/pricing"
)

const usage = `usage: pfm tokens [--since 24h|3d] [--root DIR]... [--project SUBSTR] [--family SUBSTR]
                  [--family-rx RE] [--session SID] [--top N] [--out FILE] [--view FILE] [--briefs FILE]
                  [--host-label NAME] [--prices FILE]
       pfm tokens --codex [--since 24h|3d] [--project SUBSTR] [--codex-root DIR] [--top N] [--prices FILE]
       pfm tokens --flight DIR [--metrics-out FILE] [--out FILE] [--prices FILE]
       pfm tokens --timeline FILE [--timeline FILE]... [--root DIR]... [--prices FILE]

Audits agent transcripts: where did the tokens go? Claude Code transcripts and
Codex rollouts, priced from the one table pfm model-cost serves (or a saved
` + "`pfm model-cost --json --all`" + ` document / prices.json via --prices). A text report on
stdout; --out, --view and --briefs write JSON. Exit 2 on a usage error, 1 when
the table cannot load or anything could not be read.`

// flags are the parsed command line.
type flags struct {
	hours          float64
	top            int
	project        string
	family         string
	session        string
	out            string
	view           string
	briefs         string
	familyRx       *regexp.Regexp
	familyRxSource string
	flight         string
	metricsOut     string
	codex          bool
	codexHome      string
	hostLabel      string
	roots          []string
	timeline       []string
	prices         string
}

var sinceForm = regexp.MustCompile(`^(\d+(?:\.\d+)?)([hd])$`)

// listFlag collects a repeated flag in command-line order.
type listFlag []string

func (list *listFlag) String() string { return strings.Join(*list, ",") }

func (list *listFlag) Set(value string) error {
	*list = append(*list, value)
	return nil
}

// errHelp is help, -h or --help: the usage goes to stdout and the run exits 0,
// as `pfm model-cost help` does.
var errHelp = errors.New("help requested")

// readFlags reads the command line; every refusal is a usage error.
func readFlags(args []string, host string) (flags, error) {
	if len(args) == 1 && args[0] == "help" {
		return flags{}, errHelp
	}
	if n := len(args); n != 0 && args[n-1] == "--timeline" {
		return flags{}, usageError("--timeline wants a transcript path")
	}
	var stderr bytes.Buffer
	set := cli.NewFlagSet("tokens", usage, &stderr)
	since := set.String("since", "24h", "the window: a number plus h or d")
	top := set.Int("top", 12, "rows per ranked section")
	parsed := flags{}
	set.StringVar(&parsed.project, "project", "", "only runs whose project path contains SUBSTR")
	set.StringVar(&parsed.family, "family", "", "drill into the family a title, session or agent matches")
	familyRx := set.String("family-rx", ".", "--briefs: the families whose titles match RE (case-insensitive)")
	set.StringVar(&parsed.session, "session", "", "only the session whose id starts with SID")
	set.StringVar(&parsed.out, "out", "", "write the full data as JSON")
	set.StringVar(&parsed.view, "view", "", "write the per-project page dataset as JSON")
	set.StringVar(&parsed.briefs, "briefs", "", "write every brief and what its agent did as JSON")
	set.StringVar(&parsed.flight, "flight", "", "per-agent metrics for one flight directory")
	set.StringVar(&parsed.metricsOut, "metrics-out", "", "--flight: where metrics.md goes")
	set.BoolVar(&parsed.codex, pricing.EngineCodex, false, "one row per Codex thread")
	set.StringVar(&parsed.codexHome, "codex-root", "", "the Codex home to read")
	set.StringVar(&parsed.hostLabel, "host-label", host, "--view: the host name the page shows")
	set.StringVar(&parsed.prices, "prices", "", "a saved price document or prices.json instead of the served table")
	set.Var((*listFlag)(&parsed.roots), "root", "a transcript root (repeatable)")
	set.Var((*listFlag)(&parsed.timeline), "timeline", "one transcript, one row per model call (repeatable)")
	positional, code, ok := cli.ParseFlagsAnywhere(set, args)
	if !ok {
		if code == 0 {
			return flags{}, errHelp
		}
		return flags{}, usageError("%s", usageLine(stderr.String()))
	}
	if len(positional) != 0 {
		return flags{}, usageError("unexpected argument %q", positional[0])
	}
	match := sinceForm.FindStringSubmatch(*since)
	if match == nil {
		return flags{}, usageError("--since wants a number plus h or d, e.g. 24h or 3d")
	}
	parsed.hours, _ = strconv.ParseFloat(match[1], 64)
	if match[2] == "d" {
		parsed.hours *= 24
	}
	if *top < 0 {
		return flags{}, usageError("--top wants a whole number of rows, 0 or more")
	}
	parsed.top = *top
	rx, err := regexp.Compile("(?i)" + *familyRx)
	if err != nil {
		return flags{}, usageError("--family-rx %s: %v", *familyRx, err)
	}
	parsed.familyRx, parsed.familyRxSource = rx, *familyRx
	if len(parsed.timeline) != 0 {
		sinceGiven := false
		set.Visit(func(f *flag.Flag) { sinceGiven = sinceGiven || f.Name == "since" })
		if parsed.flight != "" || parsed.codex || parsed.project != "" || sinceGiven {
			return flags{}, usageError(
				"--timeline reads whole files: it takes no --flight, --codex, --project or --since",
			)
		}
	}
	return parsed, nil
}

func usageLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	return line
}
