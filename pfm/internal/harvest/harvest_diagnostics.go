package harvest

import "fmt"

// titleGuessResult builds the "that looks like a title, not a fetchable
// identifier" answer fetchUnshared returns for a bare title.Result — the same
// wording whether the title arrived as an explicit `title:` prefix (echoed
// value) or as a bare non-URL string (echoed source). Shared here instead of
// duplicated at both call sites (fetchUnshared, harvest.go).
func titleGuessResult(source, echoed string) Result {
	return Result{
		Source: source,
		Error: fmt.Sprintf(
			"%q is a title — use the `findWorks` tool to list candidate works (it returns a handle for each), then read the one you pick with `readWork`. `readPage` reads web pages and `readWork` reads UNAMBIGUOUS identifiers (DOI, arXiv id, PMID, PMCID, ISBN), never a title.",
			echoed,
		),
	}
}

// noteRungOutcome is a reader rung's failure bookkeeping (Jina, defuddle.md):
// a transport error names its kind, a genuine answer keeps the prior error.
// A reader never reports the target's HTTP status — its own 429 or 502 is
// the reader's answer, not the site's — so lastStatus is never its to set.
func noteRungOutcome(err, lastErr error, lastErrorKind string) (string, error) {
	if err != nil {
		return errorKind(err), err
	}
	return lastErrorKind, lastErr
}

// convertOutageNote appends a tool-outage addendum to message, and fills
// lastErrorKind when nothing more specific already claimed it, when a static
// rung's converter failed (F12) and no MORE SPECIFIC diagnostic already
// covers it: an empty-PDF conversion, a wrong-kind PDF, and an app-shell
// terminal each already carry their own tailored message, and a real
// conversion failure on an earlier rung must never be masked by — or
// duplicate — one of those.
func convertOutageNote(message, kind string, outage, emptyPDFConvert, wrongPDF, appShellFailure bool) (string, string) {
	if !outage || emptyPDFConvert || wrongPDF || appShellFailure {
		return message, kind
	}
	message += " A conversion step failed on an earlier rung — that is a tool outage on this server, not proof the page has nothing to extract."
	if kind == "" {
		kind = errorKindConvert
	}
	return message, kind
}
