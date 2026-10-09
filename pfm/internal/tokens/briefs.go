package tokens

import (
	"math"
	"regexp"
	"strings"
)

var (
	briefItem   = jsRegexp(`(?m)^\s*(\d+[.)]|[-*•]|#{1,4})\s+\S`)
	briefPath   = regexp.MustCompile(`[\w.@-]+(?:/[\w.@\[\]-]+)+\.\w{1,5}\b`)
	briefFence  = regexp.MustCompile("```")
	briefAccept = regexp.MustCompile(`(?i)accept|done when|definition of done|must pass|verify`)
	testPath    = regexp.MustCompile(`(?i)test|spec`)
)

// briefRow is one sub-agent run of a matching family: its brief, and what
// the agent then did with it.
type briefRow struct {
	fam, famSID, project, ty, m, ti, at                 string
	depth                                               float64
	unpriced                                            bool
	usd, calls, pkK, wall                               float64
	briefChars, briefItems, briefPaths, briefFences     float64
	briefAccept, briefNoBrief                           bool
	distinctRead, readsBeforeEdit, firstEditCall, edits float64
	filesEdited, maxEditsOneFile, testFilesEdited       float64
	tests, testFails, rereads, errs, small, poll, growK float64
	growBy                                              *object
	testCmds                                            []any
	brief                                               string
}

func (row *briefRow) json() *object {
	var usd any
	if !row.unpriced {
		usd = row.usd
	}
	return newObject().set("fam", row.fam).set("famSid", row.famSID).set("project", row.project).set("ty", row.ty).
		set("m", row.m).set("depth", row.depth).set("ti", row.ti).set("at", row.at).set("unpriced", row.unpriced).
		set("usd", usd).set("calls", row.calls).set("pkK", row.pkK).set("wall", row.wall).
		set("briefChars", row.briefChars).set("briefItems", row.briefItems).set("briefPaths", row.briefPaths).
		set("briefFences", row.briefFences).set("briefAccept", row.briefAccept).set("briefNoBrief", row.briefNoBrief).
		set("distinctRead", row.distinctRead).set("readsBeforeEdit", row.readsBeforeEdit).
		set("firstEditCall", row.firstEditCall).set("edits", row.edits).set("filesEdited", row.filesEdited).
		set("maxEditsOneFile", row.maxEditsOneFile).set("testFilesEdited", row.testFilesEdited).
		set("tests", row.tests).set("testFails", row.testFails).set("rereads", row.rereads).set("errs", row.errs).
		set("small", row.small).set("poll", row.poll).set("growK", row.growK).set("growBy", row.growBy).
		set("testCmds", orEmpty(row.testCmds)).set("brief", row.brief)
}

// lineStarts turns every line start JavaScript's multiline ^ honours beyond
// \n (after \r, U+2028 and U+2029) into a \n.
var lineStarts = strings.NewReplacer("\r", "\n", "\u2028", "\n", "\u2029", "\n")

// briefItems counts a brief's list items and headings; JavaScript's
// multiline ^ also starts a line after \r, U+2028 and U+2029.
func briefItems(text string) float64 {
	return float64(len(briefItem.FindAllStringIndex(lineStarts.Replace(text), -1)))
}

func briefPaths(text string) float64 {
	seen := map[string]bool{}
	for _, path := range briefPath.FindAllString(text, -1) {
		seen[path] = true
	}
	return float64(len(seen))
}

// regexpSource is RegExp.prototype.source: an empty pattern reads "(?:)", and
// an unescaped "/" outside a class and every line terminator are escaped.
func regexpSource(pattern string) string {
	if pattern == "" {
		return "(?:)"
	}
	var out strings.Builder
	escaped, inClass := false, false
	for _, r := range pattern {
		switch {
		case escaped:
			escaped = false
		case r == '\\':
			escaped = true
		case r == '[':
			inClass = true
		case r == ']':
			inClass = false
		case r == '/' && !inClass:
			out.WriteString(`\/`)
			continue
		}
		switch r {
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case ' ':
			out.WriteString(` `)
		case ' ':
			out.WriteString(` `)
		default:
			out.WriteRune(r)
		}
	}
	return out.String()
}

func (a *auditor) briefRow(f *family, r *run) *briefRow {
	text := ""
	if r.brief != nil {
		text = *r.brief
	}
	land := r.land
	grow := 0.0
	for _, value := range land {
		grow += value
	}
	grow -= land[catPrefix]
	type share struct {
		name  string
		value float64
	}
	var shares []share
	for i, name := range categories {
		short, _, _ := strings.Cut(name, " (")
		if value := round(land[i] / 1000); i != catPrefix && value > 0 {
			shares = append(shares, share{short, value})
		}
	}
	stableSort(shares, func(left, right share) float64 { return right.value - left.value })
	growBy := newObject()
	for _, s := range firstN(shares, 6) {
		growBy.set(s.name, s.value)
	}
	commands := r.stats.testCmd.entries()
	stableSort(commands, func(left, right entry) float64 { return right.value.(float64) - left.value.(float64) })
	var testCmds []any
	for _, item := range firstN(commands, 3) {
		testCmds = append(testCmds, []any{item.key, item.value})
	}
	editFiles := r.stats.editFiles.entries()
	maxEdits, testFiles := 0.0, 0.0
	for _, item := range editFiles {
		maxEdits = math.Max(maxEdits, item.value.(float64))
		if testPath.MatchString(item.key) {
			testFiles++
		}
	}
	return &briefRow{
		fam:        f.title,
		famSID:     utf16Slice(f.sid, 0, 8),
		project:    jsBasename(r.project),
		ty:         firstText(r.agentType, "(untyped)"),
		m:          modelStem(r.model),
		depth:      r.depth,
		ti:         r.title,
		at:         utf16Slice(isoTime(r.t0), 0, 16),
		unpriced:   r.unpriced,
		usd:        fixedNum(r.usd, 2),
		calls:      r.calls,
		pkK:        round(r.ctxPeak / 1000),
		wall:       round(wallMinutes(r)),
		briefChars: float64(utf16Len(text)),
		briefItems: briefItems(text),
		briefPaths: briefPaths(text),
		briefFences: float64(
			len(briefFence.FindAllStringIndex(text, -1)),
		) / 2,
		briefAccept:     briefAccept.MatchString(text),
		briefNoBrief:    r.brief == nil,
		distinctRead:    r.distinctRead,
		readsBeforeEdit: r.stats.readsBeforeEdit,
		firstEditCall:   r.stats.firstEditCall,
		edits:           r.stats.edits,
		filesEdited:     float64(len(editFiles)),
		maxEditsOneFile: maxEdits,
		testFilesEdited: testFiles,
		tests:           r.stats.tests,
		testFails:       r.stats.testFails,
		rereads:         r.rereadN,
		errs:            r.errs,
		small:           fixedNum(r.smallUSD, 2),
		poll:            fixedNum(r.pollUSD, 2),
		growK:           round(grow / 1000),
		growBy:          growBy,
		testCmds:        testCmds,
		brief:           utf16Slice(text, 0, 6000),
	}
}

// writeBriefs writes --briefs: every brief the matching chat families wrote,
// and what each agent then did with it, plus its text section.
func (a *auditor) writeBriefs(t *totals) error {
	var families []*family
	var rows []*briefRow
	for _, f := range t.famList {
		if a.opts.familyRx.MatchString(f.title) {
			families = append(families, f)
			for _, r := range f.agentRuns {
				rows = append(rows, a.briefRow(f, r))
			}
		}
	}
	noBrief := 0
	groups, members := newObject(), map[string][]*briefRow{}
	for _, row := range rows {
		if row.briefNoBrief {
			noBrief++
		}
		key := row.fam + "[" + row.famSID + "] · " + row.ty + " · " + row.m
		groups.set(key, true)
		members[key] = append(members[key], row)
	}
	title := "14 · BRIEFS AND BEHAVIOUR · families matching /" + regexpSource(a.opts.familyRxSource) + "/ · " +
		num(float64(len(families))) + " families · " + num(float64(len(rows))) + " agent runs"
	if noBrief != 0 {
		title += " · " + num(float64(noBrief)) + " runs with NO BRIEF FOUND"
	}
	a.print("\n== " + title)
	a.print(
		"  " + padStart("total", 8) + " " + padStart("n", 3) + " " + padStart("med$", 7) + " " + padStart("calls", 5) +
			" " + padStart("peakK", 5) + " | brief: " + padStart("chars", 6) + " " + padStart("items", 5) + " " +
			padStart(
				"paths",
				5,
			) + " " + padStart("code", 4) + " | " + padStart("filesRd", 7) + " " + padStart("rdB4edit", 8) +
			" " + padStart("1stEdit", 7) + " " + padStart("edits", 5) + " " + padStart("files", 5) + " " +
			padStart(
				"maxOne",
				6,
			) + " " + padStart("tests", 5) + " " + padStart("fail%", 5) + "  group (medians per run)",
	)
	usdOf := func(row *briefRow) float64 { return row.usd }
	keys := groups.ordered()
	stableSort(
		keys,
		func(left, right string) float64 { return sumOf(members[right], usdOf) - sumOf(members[left], usdOf) },
	)
	for _, key := range firstN(keys, 22) {
		xs := members[key]
		median := func(value func(*briefRow) float64) string { return num(medianOf(xs, value)) }
		tests := sumOf(xs, func(row *briefRow) float64 { return row.tests })
		fails := sumOf(xs, func(row *briefRow) float64 { return row.testFails })
		a.print("  " + padStart(dollars(sumOf(xs, usdOf)), 8) + " " + padStart(num(float64(len(xs))), 3) + " " +
			padStart(dollars(medianOf(xs, usdOf)), 7) + " " +
			padStart(median(func(row *briefRow) float64 { return row.calls }), 5) + " " +
			padStart(median(func(row *briefRow) float64 { return row.pkK }), 5) + " |        " +
			padStart(median(func(row *briefRow) float64 { return row.briefChars }), 6) + " " +
			padStart(median(func(row *briefRow) float64 { return row.briefItems }), 5) + " " +
			padStart(median(func(row *briefRow) float64 { return row.briefPaths }), 5) + " " +
			padStart(median(func(row *briefRow) float64 { return row.briefFences }), 4) + " | " +
			padStart(median(func(row *briefRow) float64 { return row.distinctRead }), 7) + " " +
			padStart(median(func(row *briefRow) float64 { return row.readsBeforeEdit }), 8) + " " +
			padStart(median(func(row *briefRow) float64 { return row.firstEditCall }), 7) + " " +
			padStart(median(func(row *briefRow) float64 { return row.edits }), 5) + " " +
			padStart(median(func(row *briefRow) float64 { return row.filesEdited }), 5) + " " +
			padStart(median(func(row *briefRow) float64 { return row.maxEditsOneFile }), 6) + " " +
			padStart(median(func(row *briefRow) float64 { return row.tests }), 5) + " " +
			padStart(pctText(fails, tests), 5) + "  " + key)
	}
	a.printGrowth(rows)
	a.printBriefSizes(rows)
	sorted := append([]*briefRow(nil), rows...)
	stableSort(sorted, func(left, right *briefRow) float64 { return right.usd - left.usd })
	out := make([]*object, len(sorted))
	for i, row := range sorted {
		out[i] = row.json()
	}
	size, err := writeOutput(a.opts.briefs, stringify(out, 0))
	if err != nil {
		return err
	}
	a.print(
		"briefs → " + a.opts.briefs + " (" + fixed(
			float64(size)/1e6,
			1,
		) + " MB · " + num(
			float64(len(rows)),
		) + " runs)",
	)
	return nil
}

func (a *auditor) printGrowth(rows []*briefRow) {
	a.print("  where the context growth came from (tokens landed after the fixed prefix, all matching runs, by model):")
	models := newObject()
	for _, row := range rows {
		models.set(row.m, true)
	}
	for _, model := range models.insertion() {
		var xs []*briefRow
		total := newObject()
		all := 0.0
		for _, row := range rows {
			if row.m != model.key {
				continue
			}
			xs = append(xs, row)
			for _, item := range row.growBy.entries() {
				total.bump(item.key, item.value.(float64))
				all += item.value.(float64)
			}
		}
		shares := total.entries()
		stableSort(shares, func(left, right entry) float64 { return right.value.(float64) - left.value.(float64) })
		var parts []string
		for _, item := range firstN(shares, 6) {
			parts = append(parts, item.key+" "+pctText(item.value.(float64), all))
		}
		a.print(
			"    " + cut(
				model.key,
				10,
			) + " " + padStart(
				num(float64(len(xs))),
				4,
			) + " runs · " + strings.Join(
				parts,
				" · ",
			),
		)
	}
}

func (a *auditor) printBriefSizes(rows []*briefRow) {
	a.print("  does a bigger brief make a dearer run? (all matching runs, by number of list items in the brief):")
	for _, size := range []struct {
		lo, hi float64
		name   string
	}{{0, 3, "0–3 items"}, {4, 9, "4–9"}, {10, 19, "10–19"}, {20, 39, "20–39"}, {40, 1e9, "40+"}} {
		var xs []*briefRow
		for _, row := range rows {
			if row.briefItems >= size.lo && row.briefItems <= size.hi {
				xs = append(xs, row)
			}
		}
		if len(xs) == 0 {
			continue
		}
		a.print("    " + padStart(size.name, 10) + " " + padStart(num(float64(len(xs))), 4) + " runs · median " +
			padStart(dollars(medianOf(xs, func(row *briefRow) float64 { return row.usd })), 7) + " · median calls " +
			padStart(num(medianOf(xs, func(row *briefRow) float64 { return row.calls })), 4) + " · median peak " +
			padStart(num(medianOf(xs, func(row *briefRow) float64 { return row.pkK })), 4) + "K · median brief " +
			padStart(num(medianOf(xs, func(row *briefRow) float64 { return row.briefChars })), 6) + " chars")
	}
}
