package digest

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/rezzminator/professor/pfm/internal/pyvalue"
)

// noTimestamps is the span of a transcript or agent whose records carry no time.
const noTimestamps = "no timestamps"

const unfinishedLine = "UNFINISHED — the last turn has no final reply: the run was cut off or is still running"

// capText is text cut to limit characters with the cut counted; limit <= 0
// keeps it whole.
func capText(text string, limit int) string {
	runes := []rune(text)
	if limit <= 0 || len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…[+" + strconv.Itoa(len(runes)-limit) + "]"
}

// sizeText is a character count as B, K or M.
func sizeText(n int) string {
	switch {
	case n < 1000:
		return strconv.Itoa(n) + "B"
	case n < 1_000_000:
		return strconv.FormatFloat(float64(n)/1000, 'f', 1, 64) + "K"
	}
	return strconv.FormatFloat(float64(n)/1_000_000, 'f', 1, 64) + "M"
}

// clockText is a stamp's UTC time of day, "--:--:--" when it has none.
func clockText(ts string) string {
	if at, ok := parseStamp(ts); ok {
		return at.Format("15:04:05")
	}
	return "--:--:--"
}

// lastTextLines is the last count non-blank lines of text, each cut to width.
func lastTextLines(text string, count, width int) []string {
	if count <= 0 {
		return nil
	}
	var lines []string
	for _, line := range pyvalue.SplitLines(text) {
		if pyvalue.Strip(line) != "" {
			lines = append(lines, pyvalue.RStrip(line))
		}
	}
	lines = lines[max(0, len(lines)-count):]
	for index, line := range lines {
		lines[index] = capText(line, width)
	}
	return lines
}

func renderCall(ev *event, req *request) []string {
	head := fmt.Sprintf(
		"L%d %s %s %s",
		ev.line,
		clockText(ev.ts),
		ev.name,
		capText(pyvalue.OneLine(ev.text), req.width),
	)
	if ev.result == nil {
		return []string{head + " → NO RESULT"}
	}
	result := *ev.result
	status := "ok"
	switch {
	case ev.sub != "" && ev.err:
		status = "ERR " + ev.sub
	case ev.sub != "":
		status = ev.sub
	case ev.err:
		status = "ERR"
	}
	head += " → " + status + " " + sizeText(utf8.RuneCountInString(result))
	if ev.dur != nil && *ev.dur >= 5 {
		head += " · " + strconv.FormatFloat(*ev.dur, 'f', 0, 64) + "s"
	}
	var body []string
	switch mode := req.results; {
	case mode == "full":
		body = pyvalue.SplitLines(result)
	case strings.HasPrefix(mode, "tail:"):
		count, _ := strconv.Atoi(mode[len("tail:"):])
		body = lastTextLines(result, count, 200)
	case ev.err && mode != noneWord:
		body = lastTextLines(result, req.errLines, 200)
	case mode == "brief" && ev.cmd:
		if last := lastTextLines(result, 1, 140); len(last) != 0 {
			head += " ‹" + last[0] + "›"
		}
	}
	lines := []string{head}
	for _, line := range body {
		lines = append(lines, "    | "+line)
	}
	return lines
}

func renderEvent(ev *event, req *request) []string {
	var lines []string
	switch ev.kind {
	case kindCall:
		lines = renderCall(ev, req)
	case kindFinal:
		lines = []string{fmt.Sprintf("L%d %s FINAL", ev.line, clockText(ev.ts))}
		for _, line := range pyvalue.SplitLines(capText(ev.text, req.final)) {
			lines = append(lines, "  "+line)
		}
	default:
		tag := map[string]string{kindPrompt: "PROMPT", kindReply: "SAY", kindNote: "NOTE"}[ev.kind]
		if ev.sub != "" {
			tag += "(" + ev.sub + ")"
		}
		limit := req.text
		if ev.kind == kindNote && req.text > 0 {
			limit = min(req.text, 300)
		}
		lines = []string{
			fmt.Sprintf("L%d %s %s %s", ev.line, clockText(ev.ts), tag, capText(pyvalue.OneLine(ev.text), limit)),
		}
	}
	for _, match := range ev.matches[:min(3, len(ev.matches))] {
		lines = append(lines, "    ~ "+capText(pyvalue.OneLine(match), 200))
	}
	return lines
}

// header is the digest's head; shown names how many events the filters kept,
// -1 for none (counts).
func header(p *parsed, req *request, shown int) []string {
	meta := func(key, def string) string {
		if value, set := p.meta[key]; set {
			return value
		}
		return def
	}
	who := meta("session", "?")
	if agent := p.meta["agent"]; agent != "" {
		who = "agent " + agent + " of session " + who
	}
	role := joinNonEmpty(" · ", p.meta["agent_type"], p.meta["description"])
	model := joinNonEmpty(" ", p.truthyMeta("model"), p.truthyMeta("effort"))
	if model == "" {
		model = "model ?"
	}
	first := "TRANSCRIPT " + p.engine + " · " + who + " · " + model
	if role != "" {
		first += " · " + role
	}
	source := fmt.Sprintf("FILE %s · %d bytes · %d records · cwd %s (shown as ./)", p.path, p.bytes, p.records,
		meta("cwd", "?"))
	if p.engine == engineOpenCode {
		source = fmt.Sprintf("STORE %s · %d records: %d messages, %d parts (L = record number in store order; no "+
			"file lines or bytes) · cwd %s (shown as ./)", p.path, p.records, p.messages, p.parts, meta("cwd", "?"))
	}
	lines := []string{first, source}
	if p.title != "" {
		lines = append(lines, "NAME "+p.title)
	}
	span := noTimestamps
	if p.t0 != nil {
		span = fmt.Sprintf("%sZ → %sZ · %ds wall", p.t0.Format("2006-01-02 15:04:05"), p.t1.Format("15:04:05"),
			int(p.t1.Sub(*p.t0).Seconds()))
	}
	lines = append(lines, "SPAN "+span)
	per, errs := newTally(), newTally()
	calls, unanswered := 0, 0
	for _, ev := range p.events {
		if ev.kind != kindCall || ev.hidden {
			continue
		}
		calls++
		per.add(ev.name)
		if ev.err {
			errs.add(ev.name)
		}
		if ev.result == nil {
			unanswered++
		}
	}
	var tally []string
	for _, name := range per.mostCommon() {
		entry := name + " " + strconv.Itoa(per.counts[name])
		if errs.counts[name] != 0 {
			entry += " (" + strconv.Itoa(errs.counts[name]) + " err)"
		}
		tally = append(tally, entry)
	}
	if len(tally) == 0 {
		tally = []string{noneWord}
	}
	lines = append(lines, fmt.Sprintf("CALLS %d, %d err, %d without result: %s", calls, errs.total(), unanswered,
		strings.Join(tally, " · ")))
	if metaError := p.meta["meta_error"]; metaError != "" {
		lines = append(lines, "META UNREADABLE "+metaError)
	}
	if shown < 0 {
		return lines
	}
	total := 0
	for _, ev := range p.events {
		if !ev.hidden {
			total++
		}
	}
	filter := "FILTER only " + req.only
	for _, part := range []struct{ name, value string }{
		{"tool", strings.Join(req.tools, ",")},
		{"since", req.since},
		{"until", req.until},
		{"grep", reprIfSet(req.grep)},
		{"lines", req.lines},
		{"first", intIfSet(req.first)},
		{"last", intIfSet(req.last)},
	} {
		if part.value != "" {
			filter += " · " + part.name + " " + part.value
		}
	}
	filter += fmt.Sprintf(" · results %s · text %d · shown %d of %d events", req.results, req.text, shown, total)
	return append(lines, filter)
}

func reprIfSet(text string) string {
	if text == "" {
		return ""
	}
	return pyvalue.ReprString(text)
}

func intIfSet(value int) string {
	if value == 0 {
		return ""
	}
	return strconv.Itoa(value)
}

// joinNonEmpty joins the non-empty parts.
func joinNonEmpty(sep string, parts ...string) string {
	return strings.Join(slices.DeleteFunc(parts, func(part string) bool { return part == "" }), sep)
}

// skippedLine counts every skipped and header record by label.
func skippedLine(p *parsed) string {
	skipped, head := newTally(), newTally()
	for _, line := range p.order {
		switch mark := p.disp[line]; mark.state {
		case dispSkipped:
			skipped.add(mark.label)
		case dispHeader:
			head.add(mark.label)
		}
	}
	out := "SKIPPED " + strconv.Itoa(skipped.total()) + " records"
	if skipped.total() != 0 {
		out += ": " + tallyText(skipped)
	}
	if head.total() != 0 {
		out += " | HEADER " + strconv.Itoa(head.total()) + ": " + tallyText(head)
	}
	return out
}

// tallyText is "label count" for each label, most common first.
func tallyText(counter *tally) string {
	parts := make([]string, 0, len(counter.keys))
	for _, key := range counter.mostCommon() {
		parts = append(parts, key+" "+strconv.Itoa(counter.counts[key]))
	}
	return strings.Join(parts, " · ")
}

// shorten writes the working directory as ./ and the home as ~/.
func shorten(text string, p *parsed, home string) string {
	cwd := strings.TrimRight(strings.ReplaceAll(p.truthyMeta("cwd"), "file://", ""), "/")
	if len(cwd) > 1 {
		text = strings.ReplaceAll(text, cwd+"/", "./")
	}
	if len(home) > 1 {
		text = strings.ReplaceAll(text, home+"/", "~/")
	}
	return text
}

func showDigest(p *parsed, req *request, home string) (string, error) {
	events, err := selectEvents(p, req)
	if err != nil {
		return "", err
	}
	lines := header(p, req, len(events))
	for _, ev := range events {
		lines = append(lines, renderEvent(ev, req)...)
	}
	for index := len(p.events) - 1; index >= 0; index-- {
		kind := p.events[index].kind
		if kind == kindFinal {
			break
		}
		if kind == kindPrompt || kind == kindReply || kind == kindCall {
			lines = append(lines, unfinishedLine)
			break
		}
	}
	lines = append(lines, skippedLine(p))
	for index := 2; index < len(lines); index++ {
		lines[index] = shorten(lines[index], p, home)
	}
	return strings.Join(lines, "\n") + "\n", nil
}

func countsDigest(p *parsed, req *request) (string, error) {
	events, err := selectEvents(p, req)
	if err != nil {
		return "", err
	}
	type row struct{ calls, errs, total, largest, unanswered int }
	rows := map[string]*row{}
	var names []string
	kinds := newTally()
	for _, ev := range events {
		kinds.add(ev.kind)
		if ev.kind != kindCall {
			continue
		}
		r := rows[ev.name]
		if r == nil {
			r = &row{}
			rows[ev.name] = r
			names = append(names, ev.name)
		}
		r.calls++
		size := 0
		if ev.result != nil {
			size = utf8.RuneCountInString(*ev.result)
		} else {
			r.unanswered++
		}
		if ev.err {
			r.errs++
		}
		r.total += size
		r.largest = max(r.largest, size)
	}
	slices.SortStableFunc(names, func(left, right string) int { return rows[right].calls - rows[left].calls })
	lines := append(header(p, req, -1), "TOOL | CALLS | ERR | RESULT TOTAL | RESULT MAX | NO RESULT")
	for _, name := range names {
		r := rows[name]
		lines = append(lines, fmt.Sprintf("%s | %d | %d | %s | %s | %d", name, r.calls, r.errs, sizeText(r.total),
			sizeText(r.largest), r.unanswered))
	}
	lines = append(lines, "EVENTS "+tallyText(kinds), skippedLine(p))
	return strings.Join(lines, "\n") + "\n", nil
}

func typesDigest(p *parsed, req *request) (string, error) {
	for _, flag := range []struct {
		name  string
		given bool
	}{
		{optOnly, req.only != defaultOnly},
		{optTool, len(req.tools) != 0},
		{optGrep, req.grep != ""},
		{optSince, req.since != ""},
		{optUntil, req.until != ""},
		{optFirst, req.first != 0},
		{optLast, req.last != 0},
	} {
		if flag.given {
			return "", usageFailure("types counts records: %s does not apply (only --lines does)", flag.name)
		}
	}
	low, high := 0.0, float64(1<<62)
	if req.lines != "" {
		var err error
		if low, high, err = lineBounds(req.lines); err != nil {
			return "", err
		}
	}
	groups := map[string]*tally{dispRendered: newTally(), dispHeader: newTally(), dispSkipped: newTally()}
	total := 0
	for _, line := range p.order {
		if low <= float64(line) && float64(line) <= high {
			mark := p.disp[line]
			groups[mark.state].add(mark.label)
			total++
		}
	}
	lines := []string{fmt.Sprintf("RECORDS %d in %s (%s)", total, p.path, p.engine)}
	for _, state := range []string{dispRendered, dispHeader, dispSkipped} {
		text := tallyText(groups[state])
		if text == "" {
			text = noneWord
		}
		lines = append(lines, fmt.Sprintf("%s %d: %s", state, groups[state].total(), text))
	}
	return strings.Join(lines, "\n") + "\n", nil
}
