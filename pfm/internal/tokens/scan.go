package tokens

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// copyOnly is a transcript holding only calls copied from another one.
type copyOnly struct{ file, sid string }

// scanState counts everything a run dropped, could not price or could not
// read: the data-gaps line and the JSON's scan member.
type scanState struct {
	roots                  []string
	files                  int
	skippedOld             int
	dupFiles               int
	badLines               int
	noTimestamp            int
	unpricedCalls          int
	unpricedModels         *object
	unknownIterations      *object // usage iterations of a type pfm does not know, by type
	tierUnknownCalls       int
	syntheticCalls         int
	identityUnknownRecords int
	copiedCalls            int
	copiesOnly             []copyOnly
	readErrors             []string
	notes                  []string
}

func newScanState() *scanState {
	return &scanState{roots: []string{}, unpricedModels: newObject(), unknownIterations: newObject()}
}

func (scan *scanState) json() *object {
	copies := make([]*object, len(scan.copiesOnly))
	for i, item := range scan.copiesOnly {
		copies[i] = newObject().set("file", item.file).set("sid", item.sid)
	}
	out := newObject().set("roots", append([]string{}, scan.roots...)).set("files", scan.files).
		set("skippedOld", scan.skippedOld).set("dupFiles", scan.dupFiles).set("badLines", scan.badLines).
		set("noTimestamp", scan.noTimestamp).set("unpricedCalls", scan.unpricedCalls).
		set("unpricedModels", scan.unpricedModels).set("tierUnknownCalls", scan.tierUnknownCalls).
		set("syntheticCalls", scan.syntheticCalls).set("identityUnknownRecords", scan.identityUnknownRecords).
		set("copiedCalls", scan.copiedCalls).set("copiesOnly", copies).
		set("readErrors", append([]string{}, scan.readErrors...)).set("notes", append([]string{}, scan.notes...))
	if len(scan.unknownIterations.ordered()) != 0 {
		out.set("unknownIterations", scan.unknownIterations)
	}
	return out
}

// noteOnce adds a note the scan does not already carry.
func (scan *scanState) noteOnce(note string) {
	for _, existing := range scan.notes {
		if existing == note {
			return
		}
	}
	scan.notes = append(scan.notes, note)
}

const billingGap = "Claude billing route/region not configured; default direct/global list prices may not " +
	"match cloud billing — set billing in pfm.prices.json or override rates"

const backgroundGap = "transcript totals exclude background API calls (auto_mode, prompt_suggestion, hook_prompt, " +
	"generate_session_title, agent_summary); captured telemetry is reported separately by pfm tokens (without --tree);" +
	" missing telemetry and metadata-filtered background sessions cannot be quantified"

// gapsLine puts every dropped, unpriced or unreadable thing on ONE line, after
// the pricing provenance line. A count that exists only in the JSON is a gap
// the text report claims not to have.
func (a *auditor) gapsLine() string {
	scan, book := a.scan, a.prices
	var loud []string
	if book.refresh != nil && book.refresh.Status == "failed" {
		cause := book.refresh.Error
		if cause == "" {
			cause = "no cause given"
		}
		loud = append(loud, "price refresh failed ("+cause+") — served the table fetched "+book.table.FetchedAt)
	}
	if book.override != nil {
		loud = append(loud, "price override active: "+num(float64(book.override.Rows))+" rows from "+book.override.Path)
	}
	add := func(count int, text string) {
		if count != 0 {
			loud = append(loud, num(float64(count))+text)
		}
	}
	add(scan.badLines, " malformed lines")
	add(scan.noTimestamp, " calls without a timestamp (dropped)")
	add(scan.syntheticCalls, " synthetic/zero-usage calls (dropped: the harness billed nothing for them)")
	if scan.unpricedCalls != 0 {
		loud = append(loud, num(float64(scan.unpricedCalls))+" UNPRICED calls "+stringify(scan.unpricedModels, 0)+
			` — tokens counted, dollars "n/a"; check exact IDs and available rates with pfm model-cost --json --all`)
	}
	if unknown := scan.unknownIterations.ordered(); len(unknown) != 0 {
		count := 0.0
		for _, kind := range unknown {
			count += scan.unknownIterations.number(kind)
		}
		loud = append(loud, num(count)+" usage iterations of a type pfm does not price apart "+
			stringify(scan.unknownIterations, 0)+" — the response's own counts stand")
	}
	add(scan.tierUnknownCalls, " cache writes with no 5m/1h split (priced as 5m)")
	add(scan.identityUnknownRecords,
		" assistant records without message.id or requestId — per-record billing may include streamed copies")
	add(scan.copiedCalls, " calls copied from another transcript (forked/resumed session) — billed once within the "+
		"ownership scan (named origin, else first unmarked holder in path order)")
	if len(scan.copiesOnly) != 0 {
		var sids []string
		for _, item := range scan.copiesOnly[:min(3, len(scan.copiesOnly))] {
			sids = append(sids, utf16Slice(item.sid, 0, 12))
		}
		loud = append(
			loud,
			num(float64(len(scan.copiesOnly)))+" transcripts hold only copied calls: "+strings.Join(sids, ", "),
		)
	}
	add(scan.dupFiles, " duplicate files skipped")
	// notes are bounded: a per-row note on a 200-agent flight must not become the report
	loud = append(loud, scan.notes[:min(4, len(scan.notes))]...)
	if len(scan.notes) > 4 {
		loud = append(loud, num(float64(len(scan.notes)-4))+" further notes (see --out JSON scan.notes)")
	}
	if len(scan.readErrors) != 0 {
		loud = append(loud, num(float64(len(scan.readErrors)))+" READ ERRORS: "+
			strings.Join(scan.readErrors[:min(3, len(scan.readErrors))], " | "))
	}
	if !a.opts.codex && !a.opts.opencode {
		loud = append(loud, backgroundGap)
		if book.billing == nil || !book.billing.Configured {
			loud = append(loud, billingGap)
		}
	}
	providers := make([]string, len(book.table.Sources))
	for i, source := range book.table.Sources {
		providers[i] = source.Provider
	}
	saved := "current API list prices"
	if book.saved != "" {
		saved = "saved table " + book.saved
	}
	pricingLine := "pricing: " + saved + "; Standard service, published prompt/context tiers (Codex aggregates use " +
		"the run peak tier); table fetched " + book.table.FetchedAt + " from " + strings.Join(providers, ", ") +
		"; not historical billing"
	if book.billing != nil {
		pricingLine += "; Claude " + book.billing.Route + "/" + book.billing.Region
	}
	pricingLine += "\n"
	if len(loud) == 0 {
		return pricingLine + "data gaps: none"
	}
	return pricingLine + "data gaps: " + strings.Join(loud, " · ")
}

// walk collects dir's transcripts modified inside the window, depth first in
// name order; a directory that cannot be read is a read error, never empty.
func (a *auditor) walk(dir string, out *[]string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		a.scan.readErrors = append(a.scan.readErrors, dir+": "+describe(err))
		return
	}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		if entry.IsDir() {
			a.walk(path, out)
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			a.scan.readErrors = append(a.scan.readErrors, path+": "+describe(err))
			continue
		}
		// Ledger joins need the whole session, even when replay skips this file.
		a.transcripts = append(a.transcripts, path)
		if float64(info.ModTime().UnixMilli()) >= a.since {
			*out = append(*out, path)
		} else {
			a.scan.skippedOld++
		}
	}
}

// eachLine hands every non-empty line of file to handle, the unterminated
// last one included.
func eachLine(file string, handle func(line []byte)) error {
	_, _, err := transcript.ReadLines(file, 0, func(line []byte, _ bool) {
		if len(line) != 0 {
			handle(line)
		}
	})
	return err
}

var (
	worktreeTail  = regexp.MustCompile(`/\.worktrees/.*$`)
	trailingSlash = regexp.MustCompile(`/+$`)
	homeDirectory = regexp.MustCompile(`^(/home/[^/]+|/Users/[^/]+|/root|/)$`)
)

// foldCwd is a cwd's project: a worktree folds onto its repository.
func foldCwd(cwd string) string {
	return trailingSlash.ReplaceAllString(worktreeTail.ReplaceAllString(cwd, ""), "")
}

// isHome reports a bare home directory, which no chat folds onto.
func isHome(path string) bool { return homeDirectory.MatchString(path) }

// describe is an error as a read-error entry states it after its path: the
// operating system's own words for a path error.
func describe(err error) string {
	if pathErr, ok := pathError(err); ok {
		return pathErr.Error()
	}
	return err.Error()
}

// pathError is the operating system's path error err carries, if any.
func pathError(err error) (*fs.PathError, bool) {
	var pathErr *fs.PathError
	ok := errors.As(err, &pathErr)
	return pathErr, ok
}
