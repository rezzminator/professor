package harvest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
)

// Caller headers: readPage, download and readWork take an optional header set
// (name → value) that extends the headers sent to the TARGET. Only the
// target's origin receives them — the direct and Chrome-impersonation rungs
// (gatewayDo and the two transports re-apply them per hop, so a redirect to
// another origin drops them) and the browser (a route scoped to the origin).
// Reader services, Wayback and resolver APIs sit on other origins and never
// receive them: a header may carry a credential. A value never appears in a
// log, a receipt, an error or the output; names may.

const (
	maxCallerHeaders     = 32
	maxCallerHeaderBytes = 8 << 10
	// callerCacheDir partitions the caches of a call with headers by the
	// hash of its sorted header set: a page read with a credential is never
	// served to a call without it, nor the other way round.
	callerCacheDir = "caller-headers"
)

// ErrCallerHeader marks a caller header set refused at entry; no request
// was sent.
var ErrCallerHeader = errors.New("caller header refused")

// refusedCallerHeaders are the hop-by-hop and framing headers a caller may
// never set.
var refusedCallerHeaders = map[string]bool{
	"Host": true, "Content-Length": true, "Transfer-Encoding": true, "Connection": true,
	"Upgrade": true, "Te": true, "Trailer": true, "Keep-Alive": true,
}

// CallerHeaders is a validated caller header set, names canonical and sorted.
type CallerHeaders struct {
	names  []string
	values map[string]string
}

// Len is the number of headers in the set.
func (c CallerHeaders) Len() int { return len(c.names) }

// Names lists the header names (never the values), sorted.
func (c CallerHeaders) Names() []string { return append([]string(nil), c.names...) }

// ParseCallerHeaders validates a caller's headers: names are HTTP tokens,
// values carry no CR, LF or NUL, at most 32 headers and 8 KiB in total, no
// hop-by-hop or framing header. Each breach is a named ErrCallerHeader that
// names the header, never its value.
func ParseCallerHeaders(raw map[string]string) (CallerHeaders, error) {
	out := CallerHeaders{values: map[string]string{}}
	if len(raw) > maxCallerHeaders {
		return CallerHeaders{}, fmt.Errorf(
			"%w: %d headers given, at most %d",
			ErrCallerHeader,
			len(raw),
			maxCallerHeaders,
		)
	}
	total := 0
	for name, value := range raw {
		if !validHeaderName(name) {
			return CallerHeaders{}, fmt.Errorf(
				"%w: %q is not a valid header name (an HTTP token)",
				ErrCallerHeader,
				name,
			)
		}
		canonical := http.CanonicalHeaderKey(name)
		if refusedCallerHeaders[canonical] || strings.HasPrefix(canonical, "Proxy-") {
			return CallerHeaders{}, fmt.Errorf(
				"%w: %s is a hop-by-hop or framing header the harvester owns", ErrCallerHeader, canonical)
		}
		if strings.ContainsAny(value, "\r\n\x00") {
			return CallerHeaders{}, fmt.Errorf(
				"%w: the value of %s carries a CR, LF or NUL",
				ErrCallerHeader,
				canonical,
			)
		}
		if _, dup := out.values[canonical]; dup {
			return CallerHeaders{}, fmt.Errorf("%w: %s is given twice", ErrCallerHeader, canonical)
		}
		total += len(canonical) + len(value) + len(": \r\n")
		out.values[canonical] = value
		out.names = append(out.names, canonical)
	}
	if total > maxCallerHeaderBytes {
		return CallerHeaders{}, fmt.Errorf(
			"%w: the headers total %d bytes, at most %d", ErrCallerHeader, total, maxCallerHeaderBytes)
	}
	sort.Strings(out.names)
	return out, nil
}

// validHeaderName reports whether name is an RFC 9110 token.
func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("!#$%&'*+-.^_`|~", r):
		default:
			return false
		}
	}
	return true
}

// hash is the digest of the sorted header set that names its cache partition.
func (c CallerHeaders) hash() string {
	sum := sha256.New()
	for _, name := range c.names {
		fmt.Fprintf(sum, "%s\x00%s\n", name, c.values[name])
	}
	return hex.EncodeToString(sum.Sum(nil))[:24]
}

type callerScopeKey struct{}

type callerScope struct {
	headers CallerHeaders
	origin  string
}

// webOrigin is scheme://host[:port] of an http(s) URL, the default port
// dropped; "" when raw is no web URL.
func webOrigin(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	scheme := strings.ToLower(parsed.Scheme)
	if (scheme != schemeHTTP && scheme != schemeHTTPS) || parsed.Hostname() == "" {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	defaultPort := map[string]string{schemeHTTP: "80", schemeHTTPS: "443"}[scheme]
	if port := parsed.Port(); port != "" && port != defaultPort {
		host += ":" + port
	}
	return scheme + "://" + host
}

// ForCaller returns the harvester and context one target's read runs with:
// the headers scoped to target's origin, and caches partitioned by the
// header set's hash. An empty set returns h and ctx unchanged. A target with
// no web origin (an identifier) is a named error: its resolver APIs and
// mirrors never receive caller headers.
func (h *Harvester) ForCaller(
	ctx context.Context,
	headers CallerHeaders,
	target string,
) (*Harvester, context.Context, error) {
	if headers.Len() == 0 {
		return h, ctx, nil
	}
	origin := webOrigin(target)
	if origin == "" {
		return nil, ctx, fmt.Errorf(
			"%w: headers go only to a target's own origin, and %s names no web origin — pass the landing URL",
			ErrCallerHeader, safeURL(target))
	}
	hash := headers.hash()
	clone := &Harvester{
		options:      h.options,
		clock:        h.clock,
		client:       h.client,
		chrome:       h.chrome,
		binaryDirect: h.binaryDirect,
		binaryChrome: h.binaryChrome,
		jina:         h.jina,
		oa:           h.oa,
		userAgent:    h.userAgent,
		cache:        newCache(filepath.Join(h.cache.root, callerCacheDir, hash), h.cache.ttl, h.cache.clock),
		neg:          newNegativeCache(h.neg.ttl, h.neg.transient, h.neg.clock),
		flights:      make(map[string]*fetchFlight),
		settings:     h.settings,
	}
	return clone, context.WithValue(ctx, callerScopeKey{}, callerScope{headers: headers, origin: origin}), nil
}

// CallerHeadersFor is the header set and origin ctx scopes, for the browser
// adapter (its route adds them to that origin's requests only); nil when the
// call carries none.
func CallerHeadersFor(ctx context.Context) (map[string]string, string) {
	scope, ok := ctx.Value(callerScopeKey{}).(callerScope)
	if !ok || scope.headers.Len() == 0 {
		return nil, ""
	}
	out := make(map[string]string, len(scope.headers.values))
	for name, value := range scope.headers.values {
		out[name] = value
	}
	return out, scope.origin
}

// callerHeadersAt is the caller's headers for a request to target: the set
// when target is on the scoped origin, nil otherwise.
func callerHeadersAt(ctx context.Context, target *url.URL) map[string]string {
	scope, ok := ctx.Value(callerScopeKey{}).(callerScope)
	if !ok || target == nil || webOrigin(target.String()) != scope.origin {
		return nil
	}
	return scope.headers.values
}

// applyCallerHeaders sets the caller's headers on req when it goes to the
// scoped origin; a caller header overrides the harvester's default of the
// same name. The transports call it after stamping their defaults.
func applyCallerHeaders(req *http.Request) {
	for name, value := range callerHeadersAt(req.Context(), req.URL) {
		req.Header.Set(name, value)
	}
}

// scopeCallerRedirects wraps client so a redirect hop to another origin
// carries none of the caller's headers: net/http copies the first request's
// headers onto every hop, so each hop off the origin gets the defaults back.
func scopeCallerRedirects(ctx context.Context, client *http.Client, defaults http.Header) *http.Client {
	scope, ok := ctx.Value(callerScopeKey{}).(callerScope)
	if !ok || client == nil {
		return client
	}
	clone := *client
	next := client.CheckRedirect
	clone.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if webOrigin(req.URL.String()) != scope.origin {
			for _, name := range scope.headers.names {
				req.Header.Del(name)
				if values, ok := defaults[name]; ok {
					req.Header[name] = append([]string(nil), values...)
				}
			}
		}
		if next != nil {
			return next(req, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	return &clone
}

// MarkHeaderless names, in the result's partial reasons, a read the ladder
// reached through a rung that ran without the caller's headers (a reader
// service or an archive copy).
func (c CallerHeaders) MarkHeaderless(result Result) Result {
	if c.Len() == 0 || result.Error != "" {
		return result
	}
	method := strings.ToLower(result.Method)
	if !strings.HasPrefix(method, "jina") && !strings.HasPrefix(method, "defuddle") &&
		!strings.Contains(method, rungWayback) && !strings.Contains(method, "archive") {
		return result
	}
	reason := "read through " + result.Method + ", which ran without the caller's headers"
	if result.Partial != "" {
		reason = result.Partial + "; " + reason
	}
	result.Partial = reason
	return result
}

// HeaderFlag registers the repeatable `--header 'Name: value'` flag of the
// harvest CLI on flags.
func HeaderFlag(flags *flag.FlagSet) *HeaderLines {
	lines := &HeaderLines{}
	flags.Var(lines, "header", "a header sent to the target's origin, 'Name: value' (repeatable)")
	return lines
}

// HeaderLines is the CLI's repeated --header values.
type HeaderLines struct{ lines []string }

func (l *HeaderLines) String() string {
	if l == nil {
		return ""
	}
	return fmt.Sprintf("%d header(s)", len(l.lines))
}

// Set keeps one --header line; it is parsed with the rest in Fetch.
func (l *HeaderLines) Set(line string) error {
	l.lines = append(l.lines, line)
	return nil
}

// Parse turns the --header lines into a validated set.
func (l *HeaderLines) Parse() (CallerHeaders, error) {
	raw := map[string]string{}
	for _, line := range l.lines {
		name, value, found := strings.Cut(line, ":")
		if !found {
			return CallerHeaders{}, fmt.Errorf("%w: --header takes 'Name: value'; a line has no colon", ErrCallerHeader)
		}
		name = strings.TrimSpace(name)
		if _, dup := raw[name]; dup {
			return CallerHeaders{}, fmt.Errorf("%w: %s is given twice", ErrCallerHeader, http.CanonicalHeaderKey(name))
		}
		raw[name] = strings.TrimSpace(value)
	}
	return ParseCallerHeaders(raw)
}

// Fetch is the CLI's read of one source with the --header set: a refused
// set or a target with no origin is a failed result, and no request is sent.
func (l *HeaderLines) Fetch(ctx context.Context, h *Harvester, source string, options FetchOptions) Result {
	headers, err := l.Parse()
	if err != nil {
		return Result{Source: source, Error: err.Error(), ErrorKind: errorKindInvalid}
	}
	scoped, scopedCtx, err := h.ForCaller(ctx, headers, source)
	if err != nil {
		return Result{Source: source, Error: err.Error(), ErrorKind: errorKindInvalid}
	}
	return headers.MarkHeaderless(scoped.FetchPublic(scopedCtx, source, options))
}
