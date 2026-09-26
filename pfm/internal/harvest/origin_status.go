package harvest

import (
	"net/http"
	"regexp"
	"strconv"
)

// originMissing reports that the origin itself answered the page does not
// exist (404) or is gone (410). A reader rung (jina, defuddle) re-fetches that
// same dead origin and wraps its error page in the reader's own 200, and a
// Wayback copy is not the page the caller asked for, so none of them may stand
// in for it: the ladder fails naming the origin's status. A wall is not an
// answer about the page, so a challenge keeps every rung open; so does a DOI
// source, whose scholarly pivot resolves the work elsewhere.
func originMissing(status int, challenge bool, source string) bool {
	return !challenge && (status == http.StatusNotFound || status == http.StatusGone) && DOIFrom(source) == ""
}

// jinaTargetErrorPattern matches the envelope line Jina Reader writes when the
// page it fetched answered an HTTP error, while Jina's own answer stays 200.
var jinaTargetErrorPattern = regexp.MustCompile(`(?m)^Warning: Target URL returned error (\d{3})`)

// jinaTargetError reports the origin's HTTP error status a Jina envelope names
// (0 when it names none): the body under it is the origin's error page.
func jinaTargetError(body []byte) int {
	match := jinaTargetErrorPattern.FindSubmatch(body)
	if match == nil {
		return 0
	}
	status, err := strconv.Atoi(string(match[1]))
	if err != nil || status < 400 {
		return 0
	}
	return status
}

// deliveredStatus is the HTTP status of what delivered the stored page, the
// result's http_status: the fetched page's own, but where an extractor that
// readsSiteAPI rendered it from its site's API while the origin answered an
// error or a wall, the API's: 200, since every record it kept was answered
// below 400 (loaders.go keeps no answer of status 400 or above).
func (page convertedPage) deliveredStatus(status int) int {
	if page.siteAPI && status >= http.StatusBadRequest {
		return http.StatusOK
	}
	return status
}
