package harvest

import (
	"errors"
	"fmt"
	"net/url"
)

// safeURL renders raw as scheme://host/path for an error or a log line: no
// query string, no userinfo, no fragment. Several providers this package
// calls carry a credential in their request URL's query (books.go's Google
// Books lookup builds "...&key=<api key>"), and the stdlib wraps a failed
// request's full URL — query included — into the *url.Error it hands back
// from http.Client.Do. Every site that formats a URL into a diagnostic goes
// through here so that credential never reaches an error string or a log
// line. A URL that fails to parse renders as a fixed placeholder rather than
// echoing the unparsed text, which could itself carry a partial query.
func safeURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "<invalid-url>"
	}
	return parsed.Scheme + "://" + parsed.Host + parsed.Path
}

// sanitizeTransportError strips a *url.Error's embedded request URL —
// which carries the full query string, and with it any credential a
// provider puts there — replacing it with safeURL(raw) while keeping the
// wrapped cause intact so errors.Is/As (context.DeadlineExceeded, a
// *net.DNSError, a timeout net.Error) still see through to it. An error
// that is not a *url.Error is returned unchanged: only the stdlib
// http.Client wraps the request URL into the error text, and every gateway
// egress in this package runs through gatewayAttempt's client.Do call,
// which is the one place that wrapping happens.
func sanitizeTransportError(err error, raw string) error {
	if err == nil {
		return nil
	}
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		return err
	}
	return fmt.Errorf("%s %s: %w", urlErr.Op, safeURL(raw), urlErr.Err)
}
