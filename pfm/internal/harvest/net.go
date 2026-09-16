package harvest

import (
	"compress/flate"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

const defaultUA = "Mozilla/5.0 (compatible; harvester/1.0)"

var errResponseTooLarge = errors.New("response exceeds byte limit")

// The Python oracle's curl_cffi DEFAULT_CHROME is chrome146. Keep the UA and
// client-hint values coupled to the exact tls-client Chrome_146 profile below.
const chromeUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36"

// errorKind is the stable diagnostic class used by describe/negative-cache
// callers. Keep it independent of the human-facing wrapped error string.
func errorKind(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, errResponseTooLarge) {
		return errorKindTooLarge
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errorKindTimeout
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return errorKindDNS
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return errorKindTimeout
	}
	low := strings.ToLower(err.Error())
	for _, marker := range []string{"no such host", "name or service not known", "temporary failure in name resolution", "nodename nor servname", "no address associated"} {
		if strings.Contains(low, marker) {
			return errorKindDNS
		}
	}
	for _, marker := range []string{"connection refused", "connection reset", "connect:", "host is down", "network is unreachable"} {
		if strings.Contains(low, marker) {
			return errorKindConnect
		}
	}
	if strings.Contains(low, "unsupported protocol") || strings.Contains(low, "unsupported scheme") ||
		strings.Contains(low, "invalid url") {
		return errorKindInvalid
	}
	if strings.Contains(low, "private") || strings.Contains(low, "internal") || strings.Contains(low, "not allowed") {
		return errorKindBlocked
	}
	return errorKindConnect
}

func failureMessage(item string, status int, kind string, challenge, searchAvailable bool) string {
	if kind == errorKindInvalid {
		return fmt.Sprintf("Invalid URL: %s — %s", item, SearchHint(searchAvailable,
			"check it for typos, or use `search` to find the source.",
			"check it for typos, or find the source via another URL.",
		))
	}
	if kind == errorKindBlocked {
		return fmt.Sprintf(
			"refusing to fetch a private or internal host: %s — harvester only fetches public internet resources; use the resource's public URL instead.",
			item,
		)
	}
	if kind == errorKindTimeout {
		return fmt.Sprintf(
			"Could not reach %s: the server did not respond in time (connection timed out). %s",
			item,
			SearchHint(searchAvailable,
				"Retry later, or use `search` to find an alternative copy.",
				"Retry later, or find an alternative copy with findWorks or another URL.",
			),
		)
	}
	if kind == errorKindDNS {
		return fmt.Sprintf(
			"Could not reach %s: DNS resolution failed (host not found). %s",
			item,
			SearchHint(searchAvailable,
				"Retry later, or use `search` to find an alternative copy.",
				"Retry later, or find an alternative copy with findWorks or another URL.",
			),
		)
	}
	if kind == errorKindConnect {
		return fmt.Sprintf(
			"Could not reach %s: the connection failed (refused or host unreachable). %s",
			item,
			SearchHint(searchAvailable,
				"Retry later, or use `search` to find an alternative copy.",
				"Retry later, or find an alternative copy with findWorks or another URL.",
			),
		)
	}
	if challenge {
		return fmt.Sprintf(
			"%s is behind a bot/Cloudflare challenge — content not retrievable from this datacenter server. %s",
			item,
			SearchHint(searchAvailable,
				"Use `search` to find a mirror or alternative copy.",
				"Find a mirror or alternative copy with findWorks or another URL.",
			),
		)
	}
	if status >= 400 {
		meaning := map[int]string{400: "bad request", 401: "unauthorized", 403: "forbidden", 404: "page not found", 405: "method not allowed", 408: "request timeout", 410: "gone", 429: "too many requests", 500: "internal server error", 502: "bad gateway", 503: "service unavailable", 504: "gateway timeout"}[status]
		if meaning == "" {
			meaning = "request failed"
		}
		note := ""
		if status == 403 || status == 429 || status == 503 {
			note = " — likely a bot-block or rate limit"
		}
		return fmt.Sprintf("%s returned HTTP %d (%s)%s. %s", item, status, meaning, note, SearchHint(searchAvailable,
			"Use `search` to find an alternative copy, or `findWorks` if it is a scholarly title.",
			"Use `findWorks` if it is a scholarly title, or find an alternative copy at another URL.",
		))
	}
	return fmt.Sprintf("Could not download %s — %s", item, SearchHint(searchAvailable,
		"try `search` for an alternative source.",
		"try `findWorks` or another URL for an alternative source.",
	))
}

// FailureMessage exposes the transport core's canonical terminal diagnostic
// to protocol adapters. Keeping one renderer prevents MCP receipts from
// drifting away from negative-cache and direct-fetch errors.
func FailureMessage(item string, status int, kind string, challenge, searchAvailable bool) string {
	return failureMessage(item, status, kind, challenge, searchAvailable)
}

func safeHTTPClient(chrome bool, resolve ...func(context.Context, string) ([]net.IP, error)) *http.Client {
	timeout := 30 * time.Second
	if chrome {
		timeout = 45 * time.Second
	}
	var resolver func(context.Context, string) ([]net.IP, error)
	if len(resolve) > 0 {
		resolver = resolve[0]
	}
	return safeHTTPClientTimeoutWithResolver(chrome, timeout, resolver)
}

func safeHTTPClientTimeout(
	chrome bool,
	timeout time.Duration,
	resolve ...func(context.Context, string) ([]net.IP, error),
) *http.Client {
	var resolver func(context.Context, string) ([]net.IP, error)
	if len(resolve) > 0 {
		resolver = resolve[0]
	}
	return safeHTTPClientTimeoutWithResolver(chrome, timeout, resolver)
}

func safeHTTPClientTimeoutWithResolver(
	chrome bool,
	timeout time.Duration,
	resolver func(context.Context, string) ([]net.IP, error),
) *http.Client {
	var transport http.RoundTripper = &http.Transport{
		Proxy:             http.ProxyFromEnvironment,
		ForceAttemptHTTP2: !chrome, MaxIdleConns: 32, IdleConnTimeout: 30 * time.Second,
	}
	if chrome {
		transport = newChromeTransport(resolver)
	} else {
		transport.(*http.Transport).DialContext = pinnedDialContext(resolver)
	}
	ua := defaultUA
	if chrome {
		ua = chromeUA
	}
	client := &http.Client{Transport: &userAgentTransport{base: transport, ua: ua, chrome: chrome}, Timeout: timeout}
	client.CheckRedirect = func(req *http.Request, _ []*http.Request) error { return assertFetchable(req.URL.String(), false) }
	return client
}

// PinnedDialContext resolves once and dials only the validated public address;
// the original hostname is retained for TLS SNI by the caller.
func pinnedDialContext(
	resolve func(context.Context, string) ([]net.IP, error),
) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := publicIPs(ctx, host, resolve)
		if err != nil {
			return nil, err
		}
		d := &net.Dialer{Timeout: 20 * time.Second}
		var last error
		for _, ip := range ips {
			conn, e := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if e == nil {
				return conn, nil
			}
			last = e
		}
		if last != nil {
			return nil, last
		}
		return nil, fmt.Errorf("no public address for %s", host)
	}
}

func publicIPs(
	ctx context.Context,
	host string,
	resolve func(context.Context, string) ([]net.IP, error),
) ([]net.IP, error) {
	if ip := literalIP(host); ip != nil {
		if privateIP(ip) {
			return nil, fmt.Errorf("refusing private/internal host %s", host)
		}
		return []net.IP{ip}, nil
	}
	if host == localhostName || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return nil, fmt.Errorf("refusing private/internal host %s", host)
	}
	var ips []net.IP
	var err error
	if resolve != nil {
		ips, err = resolve(ctx, host)
	} else {
		ips, err = net.DefaultResolver.LookupIP(ctx, "ip", host)
	}
	if err != nil {
		return nil, fmt.Errorf("DNS resolution failed for %s: %w", host, err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("DNS returned no addresses for %s", host)
	}
	for _, ip := range ips {
		if privateIP(ip) {
			return nil, fmt.Errorf("refusing private/internal host %s", host)
		}
	}
	return ips, nil
}

// ChromeTransport reports the production transport identity for diagnostics
// and tests without exposing the internal net/http wiring.
func ChromeTransport(client *http.Client) bool {
	if client == nil {
		return false
	}
	if wrapped, ok := client.Transport.(*userAgentTransport); ok {
		if !wrapped.chrome {
			return false
		}
		_, ok := wrapped.base.(*chromeTransport)
		return ok
	}
	return false
}

func configureProxy(client *http.Client, raw string) {
	if client == nil || strings.TrimSpace(raw) == "" {
		return
	}
	proxyURL, err := url.Parse(raw)
	if err != nil || proxyURL.Scheme == "" || proxyURL.Host == "" {
		return
	}
	base := client.Transport
	if wrapped, ok := base.(*userAgentTransport); ok {
		base = wrapped.base
	}
	if transport, ok := base.(*chromeTransport); ok {
		if err := transport.setProxy(raw); err != nil {
			// The caller cannot surface an initialization error through this legacy
			// helper. Keep the transport's previous proxy and leave a diagnostic in
			// the request path rather than silently switching to an unsafe fallback.
			transport.setProxyError(err)
		}
		return
	}
	if transport, ok := base.(*http.Transport); ok {
		transport.Proxy = http.ProxyURL(proxyURL)
	}
}

func setUserAgent(client *http.Client, ua string) {
	if client == nil || ua == "" {
		return
	}
	if wrapped, ok := client.Transport.(*userAgentTransport); ok && !wrapped.chrome {
		wrapped.ua = ua
	}
}

// userAgentTransport's type and RoundTrip method live in
// net_ua_transport.go — transport-internal, below the fetch gateway, like
// net_chrome_transport.go. This file only builds and reads it.

func assertFetchable(raw string, strictDNS bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("invalid URL: %s", raw)
	}
	if u.Scheme != schemeHTTP && u.Scheme != schemeHTTPS {
		return fmt.Errorf("unsupported URL scheme %q", u.Scheme)
	}
	if u.User != nil {
		return errors.New("URL userinfo is not allowed")
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" {
		return errors.New("URL has no host")
	}
	if ip := literalIP(host); ip != nil {
		if privateIP(ip) {
			return fmt.Errorf("refusing private/internal host %s", host)
		}
		return nil
	}
	if host == localhostName || strings.HasSuffix(host, ".localhost") || host == localLabel ||
		strings.HasSuffix(host, ".local") ||
		host == "metadata.google.internal" ||
		strings.HasSuffix(host, ".internal") ||
		strings.HasSuffix(host, ".ts.net") {
		return fmt.Errorf("refusing private/internal host %s", host)
	}
	// DNS rebind defense. Strict mode FAILS CLOSED: Chrome performs its own
	// resolution with no pinning hop, so a resolver failure there must
	// refuse — allowing on SERVFAIL/timeout would let a TTL-0 record pass
	// validation and re-resolve to a loopback address inside the browser.
	// The HTTP-client rungs stay lenient on RESOLVER FAILURE because their
	// dialer pins the address it validated (pinnedDialContext); any address
	// that DOES resolve is checked here in both modes.
	ips, lookupErr := lookupIP(host)
	if lookupErr != nil {
		if strictDNS {
			return fmt.Errorf("DNS resolution failed for %s: %w", host, lookupErr)
		}
		return nil
	}
	if len(ips) == 0 {
		if strictDNS {
			return fmt.Errorf("DNS returned no addresses for %s", host)
		}
		return nil
	}
	for _, ip := range ips {
		if privateIP(ip) {
			return fmt.Errorf("refusing private/internal host %s", host)
		}
	}
	return nil
}

// lookupIP is the resolver seam behind assertFetchable. Its default is the
// same DNS-over-HTTPS resolver every dial pins to (ResolvePublicHost): a
// system resolver the network rewrites must not decide what the pre-check
// refuses. query (doh.go) bounds its own timeout, so context.Background()
// here never hangs the pre-check. Tests stub it to simulate SERVFAIL and
// rebind records without touching the network.
var lookupIP = func(host string) ([]net.IP, error) {
	return ResolvePublicHost(context.Background(), host)
}

// AssertFetchable is the public SSRF/scheme chokepoint for adapters whose
// transport dials only the address it validated itself.
func AssertFetchable(raw string) error { return assertFetchable(raw, false) }

// AssertFetchableStrict additionally FAILS CLOSED on resolver failure. It is
// the authority for the browser worker: Chrome re-resolves every URL with no
// pinning hop, so an unverifiable address must be refused outright. Two
// immediate resolutions narrow the TTL-0 rebind window and catch a record
// changing between policy checks. Chrome still resolves independently after
// approval, so a DNS change after the second check remains a documented
// residual risk until the browser transport can pin a validated address.
func AssertFetchableStrict(raw string) error {
	if err := assertFetchable(raw, true); err != nil {
		return err
	}
	return assertFetchable(raw, true)
}

func IsPrivateHost(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Hostname() == "" {
		return true
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if ip := literalIP(host); ip != nil {
		return privateIP(ip)
	}
	return host == localhostName || strings.HasSuffix(host, ".localhost") || host == localLabel ||
		strings.HasSuffix(host, ".local") ||
		strings.HasSuffix(host, ".internal") ||
		strings.HasSuffix(host, ".ts.net")
}

// RobotsURL is retained for callers that display the source's policy URL.
// Harvester intentionally does not enforce robots.txt (matching the Python
// server's documented --ignore-robots-txt compatibility flag).
func RobotsURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	u.Path = "/robots.txt"
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func privateIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() ||
		ip.IsMulticast() {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		n := uint32(v4[0])<<24 | uint32(v4[1])<<16 | uint32(v4[2])<<8 | uint32(v4[3])
		if n >= 0x64400000 && n <= 0x647fffff {
			return true
		}
		if n >= 0xc0000000 && n <= 0xc00000ff {
			return true
		}
		if n >= 0xc6120000 && n <= 0xc613ffff {
			return true
		}
		if n >= 0xf0000000 {
			return true
		}
	}
	return false
}

func literalIP(host string) net.IP {
	if ip := net.ParseIP(host); ip != nil {
		return ip
	}
	if strings.HasPrefix(host, "0x") {
		var value uint64
		if _, err := fmt.Sscanf(host, "0x%x", &value); err == nil && value <= 0xffffffff {
			return net.IPv4(byte(value>>24), byte(value>>16), byte(value>>8), byte(value))
		}
	}
	if host != "" && strings.Trim(host, "0123456789") == "" {
		var value uint64
		if _, err := fmt.Sscanf(host, "%d", &value); err == nil && value <= 0xffffffff {
			return net.IPv4(byte(value>>24), byte(value>>16), byte(value>>8), byte(value))
		}
	}
	if strings.HasPrefix(host, "0") && strings.ContainsAny(host, "1234567") {
		var value uint64
		if _, err := fmt.Sscanf(host, "%o", &value); err == nil && value <= 0xffffffff {
			return net.IPv4(byte(value>>24), byte(value>>16), byte(value>>8), byte(value))
		}
	}
	return nil
}

func getBody(ctx context.Context, client *http.Client, rawURL, ua string, maxBytes int64) ([]byte, int, string, error) {
	return getBodyWithHeaders(ctx, client, rawURL, ua, nil, maxBytes)
}

// getBodyWithHeaders is the generic web ladder's egress. It runs through the
// SAME gateway as every scholarly provider call (gateway.go), with escalation
// OFF: this ladder owns an explicit rung sequence of its own (direct, chrome,
// jina, defuddle, browser), so the gateway must perform exactly the one request
// it was asked for and leave the sequencing to the caller. Oversize truncates
// here — the oracle's streaming cap keeps the permitted prefix and lets the
// converter judge whether it is usable.
func getBodyWithHeaders(
	ctx context.Context,
	client *http.Client,
	rawURL, ua string,
	headers map[string]string,
	maxBytes int64,
) ([]byte, int, string, error) {
	header := make(http.Header, len(headers))
	for key, value := range headers {
		header.Set(key, value)
	}
	response, err := gatewayAttempt(ctx, gatewayRequest{
		url:              rawURL,
		client:           client,
		ua:               ua,
		headers:          header,
		max:              maxBytes,
		oversizeTruncate: true,
	})
	return response.body, response.status, response.contentType, err
}

// decodedResponseBody keeps the Chrome fingerprint's advertised encodings
// useful to the converter. net/http only auto-decompresses gzip when it adds
// that header itself; Chrome sends the complete gzip/deflate/br/zstd list, so
// decode the response chain explicitly (outermost encoding is last).
func decodedResponseBody(resp *http.Response) (io.Reader, func() error, error) {
	readers := []io.Reader{resp.Body}
	closers := []io.Closer{resp.Body}
	encodings := strings.Split(strings.ToLower(resp.Header.Get("Content-Encoding")), ",")
	for i := len(encodings) - 1; i >= 0; i-- {
		encoding := strings.TrimSpace(encodings[i])
		if encoding == "" || encoding == "identity" {
			continue
		}
		current := readers[len(readers)-1]
		var next io.Reader
		var closer io.Closer
		var err error
		switch encoding {
		case "gzip", "x-gzip":
			var zr *gzip.Reader
			zr, err = gzip.NewReader(current)
			next, closer = zr, zr
		case "deflate":
			fr := flate.NewReader(current)
			closer, next = fr, fr
		case "br":
			next = brotli.NewReader(current)
		case "zstd":
			var decoder *zstd.Decoder
			decoder, err = zstd.NewReader(current)
			next, closer = decoder, noErrorCloser{closeFn: decoder.Close}
		default:
			return nil, func() error { return resp.Body.Close() }, fmt.Errorf(
				"unsupported content encoding %q",
				encoding,
			)
		}
		if err != nil {
			for j := len(closers) - 1; j >= 0; j-- {
				_ = closers[j].Close()
			}
			return nil, func() error { return nil }, fmt.Errorf("decode %s response: %w", encoding, err)
		}
		readers = append(readers, next)
		if closer != nil {
			closers = append(closers, closer)
		}
	}
	closeFn := func() error {
		var first error
		for i := len(closers) - 1; i >= 0; i-- {
			if err := closers[i].Close(); err != nil && first == nil {
				first = err
			}
		}
		return first
	}
	return readers[len(readers)-1], closeFn, nil
}

type noErrorCloser struct{ closeFn func() }

func (c noErrorCloser) Close() error {
	c.closeFn()
	return nil
}

func classifyKind(source, contentType string, body []byte) string {
	ct := baseContentType(contentType)
	switch ct {
	case mediaTypePDF:
		return kindPDF
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return kindDOCX
	case "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		return kindXLSX
	case "application/vnd.openxmlformats-officedocument.presentationml.presentation":
		return kindPPTX
	case "application/zip", "application/x-zip-compressed":
		return kindZIP
	case "application/x-zip":
		return kindZIP
	case "application/x-7z-compressed":
		return "7z"
	case "application/x-rar-compressed", "application/vnd.rar":
		return kindRAR
	case "application/x-tar", "application/gzip", "application/x-gzip", "application/x-bzip2", "application/x-xz":
		return kindTAR
	case mediaTypeJSON, "text/json", "application/ld+json":
		return kindJSON
	case "text/csv", "application/csv":
		return kindCSV
	case "image/jpeg":
		return kindJPG
	case "image/png":
		return kindPNG
	case "image/gif":
		return kindGIF
	case "image/webp":
		return kindWebP
	case "image/bmp":
		return kindBMP
	case "image/tiff":
		return kindTIFF
	case "image/svg+xml":
		return kindSVG
	case mediaTypePlain, mediaTypeMarkdown:
		return kindTXT
	case mediaTypeHTML, mediaTypeXHTML, mediaTypeXML, mediaTypeTextXML:
		return kindHTML
	}
	if strings.Contains(ct, "openxmlformats-officedocument") {
		switch {
		case strings.Contains(ct, "wordprocessingml"):
			return kindDOCX
		case strings.Contains(ct, "spreadsheetml"):
			return kindXLSX
		case strings.Contains(ct, "presentationml"):
			return kindPPTX
		default:
			return kindZIP
		}
	}
	if strings.HasPrefix(ct, "image/") {
		return kindImage
	}
	// Every OOXML document (.docx/.xlsx/.pptx) IS a zip container, so its body
	// always matches the "PK\x03\x04" magic sniff below. The extension must
	// win before any magic-byte sniff runs — mirroring
	// the retired Python dispatch.py's local-file rule, where
	// detect.detect_kind(base) (extension) is consulted first and
	// detect.sniff_magic only ever refines an ambiguous ("html") verdict.
	if kind, ok := ooxmlExtensionKind(source); ok {
		return kind
	}
	if strings.HasPrefix(string(body), "%PDF-") {
		return kindPDF
	}
	if len(body) >= 4 && string(body[:4]) == "PK\x03\x04" {
		return kindZIP
	}
	if len(body) >= 6 && string(body[:6]) == "7z\xbc\xaf\x27\x1c" {
		return "7z"
	}
	if len(body) >= 7 && string(body[:7]) == "Rar!\x1a\x07" {
		return kindRAR
	}
	if len(body) >= 2 && body[0] == 0x1f && body[1] == 0x8b {
		return kindTAR
	}
	if len(body) >= 3 && string(body[:3]) == "BZh" {
		return kindTAR
	}
	if len(body) >= 6 && string(body[:6]) == "\xfd7zXZ\x00" {
		return kindTAR
	}
	if len(body) >= 8 && string(body[:8]) == "\x89PNG\r\n\x1a\n" {
		return kindPNG
	}
	if len(body) >= 3 && body[0] == 0xff && body[1] == 0xd8 && body[2] == 0xff {
		return kindJPG
	}
	if len(body) >= 6 && (string(body[:6]) == "GIF87a" || string(body[:6]) == "GIF89a") {
		return kindGIF
	}
	if len(body) >= 12 && string(body[:4]) == "RIFF" && string(body[8:12]) == "WEBP" {
		return kindWebP
	}
	if len(body) >= 2 && string(body[:2]) == "BM" {
		return kindBMP
	}
	if len(body) >= 4 && (string(body[:4]) == "II*\x00" || string(body[:4]) == "MM\x00*") {
		return kindTIFF
	}
	if strings.HasPrefix(strings.TrimSpace(string(body)), "<svg") ||
		strings.Contains(strings.ToLower(string(body[:min(len(body), 512)])), "<svg") {
		return kindSVG
	}
	if strings.HasSuffix(strings.ToLower(strings.Split(strings.Split(source, "?")[0], "#")[0]), ".pdf") {
		return kindPDF
	}
	lowSource := strings.ToLower(strings.Split(strings.Split(source, "?")[0], "#")[0])
	for ext, kind := range map[string]string{
		extensionJPG: kindJPG, extensionJPEG: kindJPG, extensionPNG: kindPNG, extensionGIF: kindGIF, extensionWebP: kindWebP, extensionBMP: kindBMP, extensionTIF: kindTIFF, extensionTIFF: kindTIFF, extensionSVG: kindSVG,
		extensionZIP: kindZIP, extensionTAR: kindTAR, ".tar.gz": kindTAR, ".tgz": kindTAR, ".tar.bz2": kindTAR, ".tbz2": kindTAR, ".tar.xz": kindTAR, ".txz": kindTAR, ".gz": kindTAR, ".bz2": kindTAR, ".xz": kindTAR,
		extension7Z: kind7Z, extensionRAR: kindRAR, extensionPDF: kindPDF, ".csv": kindCSV,
		// .docx/.xlsx/.pptx are handled by ooxmlExtensionKind above, before the
		// zip magic sniff — listing them again here would never be reached.
	} {
		if strings.HasSuffix(lowSource, ext) {
			return kind
		}
	}
	if strings.HasSuffix(strings.ToLower(source), ".json") {
		return kindJSON
	}
	if strings.HasSuffix(strings.ToLower(source), ".csv") {
		return kindCSV
	}
	lowSource = strings.ToLower(strings.Split(strings.Split(source, "?")[0], "#")[0])
	for _, ext := range []string{extensionTXT, ".text", extensionMD, ".markdown", ".rst", ".log", ".tex", ".org"} {
		if strings.HasSuffix(lowSource, ext) {
			return kindTXT
		}
	}
	if strings.HasSuffix(strings.ToLower(source), ".txt") || strings.HasSuffix(strings.ToLower(source), ".md") {
		return kindTXT
	}
	return kindHTML
}

// ooxmlExtensionKind reports the OOXML kind implied by source's file
// extension (.docx/.xlsx/.pptx). Every OOXML document is itself a zip
// container, so its body always matches classifyKind's "PK\x03\x04" magic
// sniff; callers must check this BEFORE that sniff so a real .docx/.xlsx/.pptx
// is never misclassified as a generic zip archive.
func ooxmlExtensionKind(source string) (string, bool) {
	name := strings.ToLower(strings.Split(strings.Split(source, "?")[0], "#")[0])
	switch {
	case strings.HasSuffix(name, ".docx"):
		return kindDOCX, true
	case strings.HasSuffix(name, ".xlsx"):
		return kindXLSX, true
	case strings.HasSuffix(name, ".pptx"):
		return kindPPTX, true
	}
	return "", false
}

func isChallenge(body []byte, status int) bool {
	low := strings.ToLower(string(body))
	// Strong, specific bot-wall phrases (mirrors the retired Python net.py's
	// _CHALLENGE_PHRASES) flag at any body length — plus the real Cloudflare
	// "Sorry, you have been blocked" (error 1020) block-page copy, which the
	// Python oracle's list also lacks.
	for _, marker := range []string{"just a moment", "checking your browser", "checking your browser before", "cf-browser-verification", "cf-chl-", "are you a robot", "confirm you are a human", "enable javascript and cookies", "captcha challenge", "completing the captcha", "verify you are human", "verifying you are human", "sorry, you have been blocked", "why have i been blocked", "attention required! | cloudflare"} {
		if strings.Contains(low, marker) {
			return true
		}
	}
	weakMarkers := []string{challengeMarkerCaptcha, challengeMarkerCloudflare, "turnstile", "attention required"}
	if contentChars(string(body)) <= 4000 {
		for _, marker := range weakMarkers {
			if strings.Contains(low, marker) {
				return true
			}
		}
	} else if status == http.StatusForbidden || status == http.StatusServiceUnavailable {
		// A real Cloudflare interstitial ships several KB of inline CSS, well
		// past the short-body heuristic above — but a 403/503 carrying ANY
		// Cloudflare marker is never a legitimate long article, so the length
		// gate does not apply for these two status codes.
		for _, marker := range weakMarkers {
			if strings.Contains(low, marker) {
				return true
			}
		}
	}
	return false
}

// stripDefuddleEnvelope drops the YAML block defuddle.md prefixes
// (`title:/source:/word_count:`) so it cannot masquerade as the artifact's own
// frontmatter when the body is cached.
func stripDefuddleEnvelope(text string) string {
	if !strings.HasPrefix(text, "---") {
		return text
	}
	if end := strings.Index(text[3:], "\n---"); end >= 0 {
		return strings.TrimLeft(text[3+end+4:], "\r\n")
	}
	return text
}

func stripJinaEnvelope(text string) string {
	if idx := strings.Index(text, "Markdown Content:"); idx >= 0 {
		return strings.TrimLeft(text[idx+len("Markdown Content:"):], "\r\n")
	}
	lines := strings.Split(text, "\n")
	i := 0
	for i < len(lines) {
		key, _, ok := strings.Cut(lines[i], ":")
		if !ok {
			break
		}
		switch key {
		case "Title", "URL Source", "Published Time", "Markdown Content", "Warning":
			i++
		default:
			return text
		}
	}
	return strings.TrimLeft(strings.Join(lines[i:], "\n"), "\r\n")
}
