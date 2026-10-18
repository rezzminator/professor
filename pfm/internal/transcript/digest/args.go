package digest

import (
	"regexp"
	"slices"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/pyvalue"
)

// defaultOnly is --only's default: every kind but error.
const defaultOnly = "prompt,reply,call,note,final"

// verbChoices are the verbs, in the order a refusal lists them.
var verbChoices = []string{"show", "locate", "agents"}

const usageText = `usage: pfm transcript {show,locate,agents} TARGET [options]

Mechanical reader of Claude Code, Codex and OpenCode transcripts.

TARGET is a transcript path, a session id or its prefix, a Claude agent id, a
Codex agent path (/root/{name}), a Claude chat name (its last title), an
OpenCode session's title, or an OpenCode locator {store}#{session id}.
An OpenCode session is read from its store: its line numbers are record numbers
(each message, then its parts) and its records carry no byte size.

options:
  --root DIR        an extra directory searched for the id or chat name (repeatable)
  --only KINDS      event kinds, comma-separated: prompt, reply, call, error, note, final;
                    'error' alone keeps only failed calls (default prompt,reply,call,note,final)
  --tool NAMES      keep only calls of these tool names (comma or repeated)
  --since TIME      HH:MM[:SS] UTC, an ISO time, +5m from the start, -15m from the end
  --until TIME
  --grep RE         keep events whose full text (input, result, prose) matches; the matching
                    lines print under each (RE2 syntax)
  -i, --ignore-case
  --first N
  --last N          the last N events after every other filter
  --results MODE    brief (size, a command's last line, a failure's tail) | none | tail:N | full
  --err-lines N     lines of a failed call's output tail (default 12)
  --text N          characters of a prompt or reply, 0 = whole (default 500)
  --final N         characters of the final reply, 0 = whole (the default)
  --lines RANGE     transcript line range FROM-TO, FROM- or one line: the events those records produced
                    (an OpenCode session: record numbers)
  --width N         characters of a call's target (default 180)
  --out FILE        write the digest here and print its path and size
`

// request is one parsed argument list.
type request struct {
	verb, target             string
	roots, tools             []string
	only, since, until, grep string
	ignoreCase               bool
	first, last              int
	results                  string
	errLines, text, final    int
	lines, out               string
	width                    int
	help                     bool
}

// optionSpec is one option: its spellings as a refusal names them (empty: the
// option's own name), whether it takes no value, and how a value lands in the
// request.
type optionSpec struct {
	display string
	flag    bool
	number  func(*request) *int
	str     func(*request) *string
	list    func(*request) *[]string
}

// The time-window options, whose value may begin with a dash.
const (
	optSince = "--since"
	optUntil = "--until"
)

var options = map[string]*optionSpec{
	"--root":        {list: func(r *request) *[]string { return &r.roots }},
	"--only":        {str: func(r *request) *string { return &r.only }},
	"--tool":        {list: func(r *request) *[]string { return &r.tools }},
	optSince:        {str: func(r *request) *string { return &r.since }},
	optUntil:        {str: func(r *request) *string { return &r.until }},
	"--grep":        {str: func(r *request) *string { return &r.grep }},
	"-i":            {display: "-i/--ignore-case", flag: true},
	"--ignore-case": {display: "-i/--ignore-case", flag: true},
	"--first":       {number: func(r *request) *int { return &r.first }},
	"--last":        {number: func(r *request) *int { return &r.last }},
	"--results":     {str: func(r *request) *string { return &r.results }},
	"--err-lines":   {number: func(r *request) *int { return &r.errLines }},
	"--text":        {number: func(r *request) *int { return &r.text }},
	"--final":       {number: func(r *request) *int { return &r.final }},
	"--lines":       {str: func(r *request) *string { return &r.lines }},
	"--width":       {number: func(r *request) *int { return &r.width }},
	"--out":         {str: func(r *request) *string { return &r.out }},
	"-h":            {display: "-h/--help", flag: true},
	"--help":        {display: "-h/--help", flag: true},
}

var (
	negativeNumber = regexp.MustCompile(`^-\d+$|^-\d*\.\d+$`)
	resultsMode    = regexp.MustCompile(`^(?:brief|none|full|tail:\d+)$`)
)

// isOptionArg reports an argument read as an option, as argparse reads one: a
// leading dash, unless it is a lone dash, a negative number or holds a space
// without naming a known option.
func isOptionArg(arg string) bool {
	if !strings.HasPrefix(arg, "-") || arg == "-" {
		return false
	}
	name, _, _ := strings.Cut(arg, "=")
	if _, known := options[name]; known {
		return true
	}
	return !negativeNumber.MatchString(arg) && !strings.Contains(arg, " ")
}

// parseDigestArgs reads args the way the Python reader's argparse did: options
// anywhere, `--name=value` or `--name value`, the last value winning (--root
// and --tool append), `--since -15m` a value. Option names are never
// abbreviated.
func parseDigestArgs(args []string) (request, error) {
	req := request{only: defaultOnly, results: "brief", errLines: 12, text: 500, width: 180}
	var argv []string
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if (arg == optSince || arg == optUntil) && index+1 < len(args) && strings.HasPrefix(args[index+1], "-") {
			arg += "=" + args[index+1]
			index++
		}
		argv = append(argv, arg)
	}
	var positionals, unknown []string
	for index := 0; index < len(argv); index++ {
		arg := argv[index]
		if arg == "--" {
			for _, rest := range argv[index+1:] {
				if err := addPositional(&positionals, rest); err != nil {
					return req, err
				}
			}
			break
		}
		if !isOptionArg(arg) {
			if err := addPositional(&positionals, arg); err != nil {
				return req, err
			}
			continue
		}
		name, value, hasValue := strings.Cut(arg, "=")
		if !strings.HasPrefix(arg, "--") {
			name, hasValue = arg, false
		}
		spec, known := options[name]
		if !known {
			unknown = append(unknown, arg)
			continue
		}
		display := spec.display
		if display == "" {
			display = name
		}
		if spec.flag {
			if hasValue {
				return req, usageFailure("argument %s: ignored explicit argument %s", display,
					pyvalue.ReprString(value))
			}
			if name == "-h" || name == "--help" {
				req.help = true
				return req, nil
			}
			req.ignoreCase = true
			continue
		}
		if !hasValue {
			if index+1 >= len(argv) || isOptionArg(argv[index+1]) {
				return req, usageFailure("argument %s: expected one argument", display)
			}
			index++
			value = argv[index]
		}
		switch {
		case spec.number != nil:
			number, ok := pyvalue.ParseInt(value)
			if !ok {
				return req, usageFailure("argument %s: invalid int value: %s", display, pyvalue.ReprString(value))
			}
			*spec.number(&req) = number
		case spec.list != nil:
			*spec.list(&req) = append(*spec.list(&req), value)
		default:
			*spec.str(&req) = value
		}
	}
	switch len(positionals) {
	case 0:
		return req, usageFailure("the following arguments are required: verb, target")
	case 1:
		return req, usageFailure("the following arguments are required: target")
	}
	req.verb, req.target = positionals[0], positionals[1]
	unknown = append(unknown, positionals[2:]...)
	if len(unknown) != 0 {
		return req, usageFailure("unrecognized arguments: %s", strings.Join(unknown, " "))
	}
	if !resultsMode.MatchString(req.results) {
		return req, usageFailure("--results %s: brief, none, full or tail:N", pyvalue.ReprString(req.results))
	}
	return req, nil
}

// addPositional appends arg to positionals; the first must be a verb, before
// or after "--".
func addPositional(positionals *[]string, arg string) error {
	if len(*positionals) == 0 && !slices.Contains(verbChoices, arg) {
		return usageFailure("argument verb: invalid choice: %s (choose from %s)",
			pyvalue.ReprString(arg), quotedChoices())
	}
	*positionals = append(*positionals, arg)
	return nil
}

// quotedChoices is the verbs as argparse lists them in a refusal.
func quotedChoices() string {
	quoted := make([]string, len(verbChoices))
	for index, verb := range verbChoices {
		quoted[index] = pyvalue.ReprString(verb)
	}
	return strings.Join(quoted, ", ")
}
