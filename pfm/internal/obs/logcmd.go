package obs

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"hostops/pfm/internal/cli"
	"hostops/pfm/internal/clock"
)

// LogUsage is `pfm log`'s one-line usage.
const LogUsage = "usage: pfm log [--since 10m] [--level warn] [--chat X] [--cmd reload] [--comp mcp] [--follow]"

// followPoll is how often --follow looks for records appended since the last
// read. The reader holds no lock: the writer only ever appends.
const followPoll = 200 * time.Millisecond

// logFilter is the question `pfm log` asks of each record.
type logFilter struct {
	after time.Time
	level slog.Level
	chat  string
	cmd   string
	comp  string
}

// ReadActivity is `pfm log`: a filter over the home's activity file that
// prints the matching records as they were written, one JSON object per line,
// so the output pipes into jq unchanged.
func ReadActivity(
	ctx context.Context,
	args []string,
	stdout, stderr io.Writer,
	path string,
	timing clock.Clock,
) int {
	flags := cli.NewFlagSet("log", LogUsage, stderr)
	since := flags.Duration("since", 0, "only records written within this duration")
	level := flags.String("level", "", "only records at this level or above ("+strings.Join(LevelNames, ", ")+")")
	chat := flags.String(FieldChat, "", "only records scoped to this chat")
	command := flags.String(FieldCmd, "", "only records written by this pfm verb")
	comp := flags.String(FieldComp, "", "only records of this component ("+strings.Join(Components, ", ")+")")
	follow := flags.Bool("follow", false, "keep reading as records are appended")
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 0 {
		flags.Usage()
		return 2
	}
	if timing == nil {
		timing = clock.Real
	}
	filter := logFilter{level: slog.LevelDebug, chat: *chat, cmd: *command, comp: *comp}
	if *since > 0 {
		filter.after = timing.Now().Add(-*since)
	}
	if strings.TrimSpace(*level) != "" {
		parsed, err := ParseLevel(*level)
		if err != nil {
			fmt.Fprintf(stderr, "pfm log: --level: %v\n", err)
			return 2
		}
		filter.level = parsed
	}
	if *comp != "" && !KnownComponent(*comp) {
		fmt.Fprintf(stderr, "pfm log: --comp %q is not one of %s\n", *comp, strings.Join(Components, ", "))
		return 2
	}
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(stderr, "pfm log: no activity log at %s yet\n", path)
			return 0
		}
		fmt.Fprintf(stderr, "pfm log: read %s: %v\n", path, err)
		return 1
	}
	defer func() { cli.CloseResource(file, "pfm log: close "+path, stderr, new(int)) }()
	return streamLog(ctx, file, stdout, stderr, filter, *follow, timing)
}

// streamLog prints every matching record, then — with follow — keeps reading
// what is appended until ctx is done.
func streamLog(
	ctx context.Context,
	file io.Reader,
	stdout, stderr io.Writer,
	filter logFilter,
	follow bool,
	timing clock.Clock,
) int {
	reader := bufio.NewReader(file)
	unreadable := 0
	for {
		line, err := reader.ReadString('\n')
		if strings.HasSuffix(line, "\n") {
			printed, readable := matchLog(line, filter)
			if !readable {
				unreadable++
			}
			if printed {
				fmt.Fprint(stdout, line)
			}
			continue
		}
		if err != nil && !errors.Is(err, io.EOF) {
			fmt.Fprintf(stderr, "pfm log: read: %v\n", err)
			return 1
		}
		if !follow {
			break
		}
		if waitErr := timing.Sleep(ctx, followPoll); waitErr != nil {
			break
		}
	}
	if unreadable != 0 {
		fmt.Fprintf(stderr, "pfm log: %d records could not be decoded and were not filtered\n", unreadable)
	}
	return 0
}

// matchLog reports whether line passes filter, and whether it could be read at
// all. An undecodable line is PRINTED — a reader filtering records must never
// make a damaged one disappear — and counted for the caller to report.
func matchLog(line string, filter logFilter) (printed, readable bool) {
	fields := map[string]any{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &fields); err != nil {
		return true, false
	}
	if filter.chat != "" && fieldText(fields[FieldChat]) != filter.chat {
		return false, true
	}
	if filter.cmd != "" && fieldText(fields[FieldCmd]) != filter.cmd {
		return false, true
	}
	if filter.comp != "" && fieldText(fields[FieldComp]) != filter.comp {
		return false, true
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(fieldText(fields[slog.LevelKey]))); err != nil {
		return true, false
	}
	if level < filter.level {
		return false, true
	}
	if !filter.after.IsZero() {
		stamp, err := time.Parse(time.RFC3339Nano, fieldText(fields[FieldTime]))
		if err != nil {
			return true, false
		}
		if stamp.Before(filter.after) {
			return false, true
		}
	}
	return true, true
}

// fieldText renders a decoded JSON value as the string a filter compares against.
func fieldText(value any) string {
	if value == nil {
		return ""
	}
	if rendered, ok := value.(string); ok {
		return rendered
	}
	return fmt.Sprint(value)
}
