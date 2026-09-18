package obs

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
)

// compHTTPOut is the component every outbound HTTP record belongs to.
const compHTTPOut = "http.out"

// retryKey carries the ordinal a caller set with Retry.
type retryKey struct{}

// Retry marks ctx as the ordinal-th retry of a request (1 = the first retry),
// so the http.out record its round trip writes carries `retries`. A request
// whose context carries no ordinal records none: it is the first attempt.
func Retry(ctx context.Context, ordinal int) context.Context {
	return context.WithValue(ctx, retryKey{}, ordinal)
}

// outboundTripper is the http.out middleware: one record per round trip,
// written after next answered, with the result exactly as next returned it.
type outboundTripper struct {
	next http.RoundTripper
}

// RoundTripper wraps next in the http.out middleware (spec § Middleware). A
// nil next means http.DefaultTransport, read at each call exactly as
// http.Client does. An already wrapped transport is returned as it is, so a
// client that is wrapped twice — a base client copied and re-wrapped — still
// logs each request once.
//
// The record carries method, host, path (never the query), status, the
// response's declared ContentLength as bytes (-1 when the server did not
// declare one), dur_ms, the Retry ordinal when the caller set one, and err.
// Headers and bodies never reach it. A transport error logs at ERROR, a 4xx or
// 5xx response at WARN, everything else at INFO.
func RoundTripper(next http.RoundTripper) http.RoundTripper {
	if wrapped, already := next.(*outboundTripper); already {
		return wrapped
	}
	return &outboundTripper{next: next}
}

// WrapClient installs the http.out middleware on client in place — its
// Transport is wrapped, and Timeout, Jar and CheckRedirect are untouched — and
// returns the same client, so a constructor reads
// `obs.WrapClient(&http.Client{...})`. A nil client becomes a wrapped
// zero-value client (the default transport, no timeout), which is what
// http.DefaultClient is.
func WrapClient(client *http.Client) *http.Client {
	if client == nil {
		client = &http.Client{}
	}
	client.Transport = RoundTripper(client.Transport)
	return client
}

func (tripper *outboundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	next := tripper.next
	if next == nil {
		next = http.DefaultTransport
	}
	ctx := request.Context()
	timing := current(ctx).timing
	started := timing.Now()
	response, err := next.RoundTrip(request)
	elapsed := timing.Now().Sub(started).Milliseconds()

	attrs := []slog.Attr{
		slog.String("op", "request"),
		slog.String("method", request.Method),
		slog.Int64(FieldDur, elapsed),
	}
	if request.URL != nil {
		attrs = append(attrs, slog.String("host", request.URL.Host), slog.String("path", request.URL.Path))
	}
	if ordinal, set := ctx.Value(retryKey{}).(int); set && ordinal > 0 {
		attrs = append(attrs, slog.Int("retries", ordinal))
	}
	level := slog.LevelInfo
	switch {
	case err != nil:
		level = slog.LevelError
		attrs = append(attrs, slog.String(FieldErr, requestErrorText(err)))
	case response != nil:
		attrs = append(attrs, slog.Int("status", response.StatusCode), slog.Int64("bytes", response.ContentLength))
		if response.StatusCode >= http.StatusBadRequest {
			level = slog.LevelWarn
		}
	}
	Logger(Component(ctx, compHTTPOut)).LogAttrs(ctx, level, "http.out.request", attrs...)
	return response, err
}

// requestErrorText renders a round-trip error without the URL a *url.Error
// carries: that URL holds the query string, and a query string can hold a key.
func requestErrorText(err error) string {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		return urlErr.Op + ": " + urlErr.Err.Error()
	}
	return err.Error()
}
