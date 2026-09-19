package harvest

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

type doiMetadataFailure struct {
	provider string
	err      error
}

// doiMetadataError aggregates every provider's own failure for one lookup
// that came back with nothing usable. subject names what was being looked up
// ("DOI metadata" when unset, for ResolveDOI's original caller; "title
// lookup" for ResolveTitle's F13 fix; "book" for ResolveBook's F14 fix) so
// the rendered message never claims a DOI lookup failed when the query was
// actually a book title.
type doiMetadataError struct {
	subject  string
	failures []doiMetadataFailure
	kind     string
}

func (e *doiMetadataError) Error() string {
	subject := e.subject
	if subject == "" {
		subject = "DOI metadata"
	}
	details := make([]string, 0, len(e.failures))
	for _, failure := range e.failures {
		details = append(details, failure.provider+":"+doiMetadataFailureKind(failure.err))
	}
	return fmt.Sprintf("%s lookup failed; %s: providers %s", subject, e.kind, strings.Join(details, ", "))
}

func (e *doiMetadataError) Unwrap() error {
	causes := make([]error, 0, len(e.failures))
	for _, failure := range e.failures {
		if failure.err != nil {
			causes = append(causes, failure.err)
		}
	}
	return errors.Join(causes...)
}

func doiMetadataHTTPStatus(err error) int {
	if err == nil {
		return 0
	}
	match := doiHTTPStatusPattern.FindStringSubmatch(err.Error())
	if len(match) != 2 {
		return 0
	}
	var status int
	if _, scanErr := fmt.Sscanf(match[1], "%d", &status); scanErr != nil {
		return 0
	}
	return status
}

func doiMetadataFailureKind(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return errorKindCancelled
	}
	status := doiMetadataHTTPStatus(err)
	switch {
	case status == http.StatusRequestTimeout || status == http.StatusGatewayTimeout:
		return errorKindTimeout
	case status == http.StatusTooManyRequests:
		return errorKindConnect
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return errorKindBlocked
	case status >= 400 && status < 500:
		return errorKindInvalid
	case status >= 500:
		return errorKindConnect
	default:
		return errorKind(err)
	}
}

func doiMetadataAbsence(err error) bool {
	status := doiMetadataHTTPStatus(err)
	return status == http.StatusNotFound || status == http.StatusGone
}

func doiResolverErrorKind(err error) string {
	if err == nil {
		return ""
	}
	var metadataErr *doiMetadataError
	if errors.As(err, &metadataErr) {
		return metadataErr.kind
	}
	return errorKind(err)
}

func mergeResolverFailure(failure, fallback Result) Result {
	if fallback.Error == "" {
		return failure
	}
	failure.Error += "; " + fallback.Error
	// A missing fallback cannot establish absence while metadata lookup failed.
	// Keep this precedence identical for direct identifiers and publisher pivots.
	missing := fallback.ErrorKind == errorKindMissing || fallback.ErrorKind == errorKindMissingPDF ||
		fallback.HTTPStatus == http.StatusNotFound || fallback.HTTPStatus == http.StatusGone
	if fallback.ErrorKind != "" && !missing {
		failure.ErrorKind = fallback.ErrorKind
		failure.Challenge = fallback.Challenge
		failure.HTTPStatus = fallback.HTTPStatus
	}
	return failure
}
