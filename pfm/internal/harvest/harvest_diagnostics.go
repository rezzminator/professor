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
			"%q is a title — use the `findWorks` tool to list candidate works (it returns a fetch handle for each), then fetch the one you pick. `fetch` retrieves locations and UNAMBIGUOUS identifiers (URL, file path, DOI, ISBN), never a title.",
			echoed,
		),
	}
}

// noteRungOutcome mirrors the Jina rung's own failure bookkeeping
// (fetchURLWithPolicy, harvest.go) for a sibling ladder rung that previously
// updated neither lastErr nor lastErrorKind on a transport failure (F11):
// getBody returns status=0 on every transport-error path, so only a genuine
// HTTP response updates lastStatus — letting a later transport error clobber
// it would make the terminal receipt report HTTPStatus 0 for what was really
// an earlier walled 403.
func noteRungOutcome(err error, status int, lastErr error, lastErrorKind string, lastStatus int) (string, int, error) {
	if err != nil {
		return errorKind(err), lastStatus, err
	}
	return lastErrorKind, status, lastErr
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
