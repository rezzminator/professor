package digest

import (
	"math"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/pyvalue"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// eventKinds are the kinds --only names, in the order a refusal lists them.
var eventKinds = []string{"prompt", "reply", "call", "error", "note", "final"}

var (
	relativeTime = regexp.MustCompile(`^([+-])(\d+)([smh])$`)
	clockTime    = regexp.MustCompile(`^\d{1,2}:\d{2}(:\d{2})?$`)
	lineSpan     = regexp.MustCompile(`^(\d+)(?:-(\d*))?$`)
)

// selectEvents applies every filter in order: --only, --tool, --lines,
// --since/--until, --grep, then --first and --last.
func selectEvents(p *parsed, req *request) ([]*event, error) {
	only := map[string]bool{}
	var unknown []string
	for _, kind := range strings.Split(req.only, ",") {
		kind = pyvalue.Strip(kind)
		if kind == "" || only[kind] {
			continue
		}
		only[kind] = true
		if !slices.Contains(eventKinds, kind) {
			unknown = append(unknown, kind)
		}
	}
	if len(unknown) != 0 {
		sort.Strings(unknown)
		return nil, usageFailure("--only: unknown kind %s (kinds: %s)", strings.Join(unknown, ", "),
			strings.Join(eventKinds, ", "))
	}
	var events []*event
	for _, ev := range p.events {
		keep := only[ev.kind]
		if ev.kind == kindCall {
			keep = only[kindCall] || (only["error"] && ev.err)
		}
		if keep && !ev.hidden {
			events = append(events, ev)
		}
	}
	if len(req.tools) != 0 {
		names := map[string]bool{}
		for _, name := range strings.Split(strings.Join(req.tools, ","), ",") {
			if name = pyvalue.Strip(name); name != "" {
				names[strings.ToLower(name)] = true
			}
		}
		events = keepEvents(
			events,
			func(ev *event) bool { return ev.kind != kindCall || names[strings.ToLower(ev.name)] },
		)
	}
	if req.lines != "" {
		low, high, err := lineBounds(req.lines)
		if err != nil {
			return nil, err
		}
		events = keepEvents(events, func(ev *event) bool { return low <= float64(ev.line) && float64(ev.line) <= high })
	}
	if req.since != "" || req.until != "" {
		var low, high *time.Time
		for _, bound := range []struct {
			spec   string
			target **time.Time
		}{{req.since, &low}, {req.until, &high}} {
			if bound.spec == "" {
				continue
			}
			at, err := windowTime(bound.spec, p)
			if err != nil {
				return nil, err
			}
			*bound.target = &at
		}
		events = keepEvents(events, func(ev *event) bool {
			at, ok := parseStamp(ev.ts)
			return ok && (low == nil || !at.Before(*low)) && (high == nil || !at.After(*high))
		})
	}
	if req.grep != "" {
		pattern := req.grep
		if req.ignoreCase {
			pattern = "(?i)" + pattern
		}
		matcher, err := regexp.Compile(pattern)
		if err != nil {
			return nil, usageFailure("--grep %s: %v", pyvalue.ReprString(req.grep), err)
		}
		events = keepEvents(events, func(ev *event) bool {
			ev.matches = nil
			for _, line := range pyvalue.SplitLines(ev.full()) {
				if matcher.MatchString(line) {
					ev.matches = append(ev.matches, line)
				}
			}
			return len(ev.matches) != 0
		})
	}
	if req.first != 0 {
		events = events[:sliceBound(req.first, len(events))]
	}
	if req.last != 0 {
		events = events[sliceBound(-req.last, len(events)):]
	}
	return events, nil
}

// sliceBound is a Python slice index over a list of n: a negative index
// counts from the end, both clamped into the list.
func sliceBound(index, n int) int {
	if index < 0 {
		index += n
	}
	return min(max(index, 0), n)
}

// keepEvents is the events keep admits, in order.
func keepEvents(events []*event, keep func(*event) bool) []*event {
	var kept []*event
	for _, ev := range events {
		if keep(ev) {
			kept = append(kept, ev)
		}
	}
	return kept
}

// lineBounds reads --lines: FROM-TO, FROM- (open) or one line.
func lineBounds(spec string) (low, high float64, err error) {
	match := lineSpan.FindStringSubmatch(spec)
	if match == nil {
		return 0, 0, usageFailure("--lines %s: FROM-TO, FROM- or one line number", pyvalue.ReprString(spec))
	}
	from, _ := strconv.ParseFloat(match[1], 64)
	low, high = from, from
	if strings.Contains(spec, "-") {
		high = math.Inf(1)
		if match[2] != "" {
			high, _ = strconv.ParseFloat(match[2], 64)
		}
	}
	if low > high {
		return 0, 0, usageFailure("--lines %s: FROM is after TO", spec)
	}
	return low, high, nil
}

// windowTime reads a --since or --until bound: +N[smh] from the first stamp,
// -N[smh] from the last, HH:MM[:SS] on the first stamp's day (UTC), or an ISO
// time.
func windowTime(spec string, p *parsed) (time.Time, error) {
	noStamps := usageFailure("--since/--until %s: the transcript carries no timestamps", spec)
	if match := relativeTime.FindStringSubmatch(spec); match != nil {
		if p.t0 == nil {
			return time.Time{}, noStamps
		}
		amount, err := strconv.ParseInt(match[2], 10, 64)
		if err != nil {
			return time.Time{}, usageFailure("--since/--until %s: %v", spec, err)
		}
		unit := map[string]time.Duration{"s": time.Second, "m": time.Minute, "h": time.Hour}[match[3]]
		if amount > math.MaxInt64/int64(unit) {
			return time.Time{}, usageFailure("--since/--until %s: a span past %v", spec, time.Duration(math.MaxInt64))
		}
		span := time.Duration(amount) * unit
		if match[1] == "+" {
			return p.t0.Add(span), nil
		}
		return p.t1.Add(-span), nil
	}
	if clockTime.MatchString(spec) {
		if p.t0 == nil {
			return time.Time{}, noStamps
		}
		parts := strings.Split(spec, ":")
		values := []int{0, 0, 0}
		for index, part := range parts {
			values[index], _ = strconv.Atoi(part)
		}
		if values[0] < 24 && values[1] < 60 && values[2] < 60 {
			return time.Date(p.t0.Year(), p.t0.Month(), p.t0.Day(), values[0], values[1], values[2], 0, time.UTC), nil
		}
	}
	// fromisoformat reads a whole date and never rolls one over: 2026-02-30,
	// T24:00 and a year alone are unreadable, as they were.
	at, ok := parseStamp(spec)
	if written, read := transcript.ParseTimestamp(spec, time.UTC); !read || len(spec) < len("2006-01-02") ||
		written.Format("2006-01-02") != spec[:len("2006-01-02")] {
		ok = false
	}
	if !ok {
		return time.Time{}, usageFailure("--since/--until: unreadable time %s (HH:MM[:SS] UTC, an ISO time, -15m from "+
			"the end, +5m from the start)", pyvalue.ReprString(spec))
	}
	return at, nil
}
