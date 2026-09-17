package harvest

import "net/http"

// userAgentTransport is the http.RoundTripper wrapper installed into every
// gateway client's http.Client.Transport (safeHTTPClientTimeoutWithResolver
// in net.go builds it; it lives here, not there, because its RoundTrip
// forwards to a wrapped transport and that forwarding call is
// transport-internal, below the fetch gateway entirely — the same category as
// net_chrome_transport.go's redirect-following. It is not a second
// application call site issuing a NEW request that bypasses the gateway: it
// is the wire-send mechanism FOR a request gatewayAttempt already built and
// validated, invoked automatically by http.Client.Do itself.
type userAgentTransport struct {
	base   http.RoundTripper
	ua     string
	chrome bool
}

func (t *userAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := validateFetchURL(req.URL.String(), false); err != nil {
		return nil, err
	}
	clone := req.Clone(req.Context())
	clone.Header.Set("User-Agent", t.ua)
	if t.chrome {
		clone.Header.Set(
			headerAccept,
			"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7",
		)
		clone.Header.Set("Accept-Encoding", "gzip, deflate, br, zstd")
		clone.Header.Set("Accept-Language", "en-US,en;q=0.9")
		clone.Header.Set("Priority", "u=0, i")
		clone.Header.Set("Sec-Fetch-Dest", "document")
		clone.Header.Set("Sec-Fetch-Mode", "navigate")
		clone.Header.Set("Sec-Fetch-Site", "none")
		clone.Header.Set("Sec-Fetch-User", "?1")
		clone.Header.Set("Upgrade-Insecure-Requests", "1")
		clone.Header.Set("Sec-CH-UA", `"Chromium";v="146", "Not-A.Brand";v="24", "Google Chrome";v="146"`)
		clone.Header.Set("Sec-CH-UA-Mobile", "?0")
		clone.Header.Set("Sec-CH-UA-Platform", `"macOS"`)
	}
	return t.base.RoundTrip(clone)
}
