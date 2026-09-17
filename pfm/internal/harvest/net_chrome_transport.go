package harvest

// Chrome transport implementation. tls-client owns the wire-level Chrome
// profile (TLS ClientHello, HTTP/2 SETTINGS, pseudo-header order and stream
// priority). This adapter is deliberately narrow: callers continue to use
// net/http while the fingerprinting client uses its fhttp fork.

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tlsclient "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
	utls "github.com/bogdanfinn/utls"
	"golang.org/x/net/proxy"
)

// These are the browser request headers emitted by curl_cffi's Chrome desktop
// profile. Header-Order is required because a Go map otherwise sorts the
// fields lexicographically in fhttp. The order is part of the capture oracle,
// not cosmetic formatting.
var chromeHeaderOrder = []string{
	"sec-ch-ua",
	"sec-ch-ua-mobile",
	"sec-ch-ua-platform",
	"upgrade-insecure-requests",
	"user-agent",
	"accept",
	"sec-fetch-site",
	"sec-fetch-mode",
	"sec-fetch-user",
	"sec-fetch-dest",
	"accept-encoding",
	"accept-language",
	"priority",
}

const (
	chromeAccept          = "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"
	chromeAcceptEncoding  = "gzip, deflate, br, zstd"
	chromeAcceptLanguage  = "en-US,en;q=0.9"
	chromePriority        = "u=0, i"
	chromeSecCHUA         = `"Chromium";v="146", "Not-A.Brand";v="24", "Google Chrome";v="146"`
	chromeSecCHUAMobile   = "?0"
	chromeSecCHUAPlatform = `"macOS"`
)

// chromeTransport is a net/http RoundTripper backed by tls-client's
// CGO-free Chrome_146 profile. initErr is retained and returned from every
// request; it is never converted into a silent Chrome133 or plain-HTTP
// fallback.
type chromeTransport struct {
	mu       sync.RWMutex
	client   tlsclient.HttpClient
	resolve  func(context.Context, string) ([]net.IP, error)
	initErr  error
	proxyErr error
}

func newChromeTransport(resolve func(context.Context, string) ([]net.IP, error)) *chromeTransport {
	t := &chromeTransport{resolve: resolve}
	client, err := tlsclient.NewHttpClient(
		nil,
		tlsclient.WithClientProfile(chrome146OracleProfile()),
		tlsclient.WithNotFollowRedirects(),
		tlsclient.WithDisableHttp3(),
		tlsclient.WithRandomTLSExtensionOrder(),
		tlsclient.WithTimeoutMilliseconds(45_000),
		tlsclient.WithProxyDialerFactory(
			func(proxyURL string, timeout time.Duration, _ *net.TCPAddr, _ fhttp.Header, _ tlsclient.Logger) (proxy.ContextDialer, error) {
				return newChromeDialer(proxyURL, timeout, resolve)
			},
		),
		tlsclient.WithTransportOptions(&tlsclient.TransportOptions{
			MaxIdleConns:       32,
			IdleConnTimeout:    durationPtr(30 * time.Second),
			DisableCompression: true,
		}),
	)
	if err != nil {
		t.initErr = fmt.Errorf("create Chrome_146 transport: %w", err)
		return t
	}
	t.client = client
	return t
}

// chrome146OracleProfile is tls-client's Chrome_146 profile with one
// implementation-only trust-anchor extension removed. curl_cffi's
// chrome146 capture carries the standard Chrome extension set (including
// ECH/ALPS) but not the extra 0xca34 extension present in tls-client's
// profile. The remaining profile values are intentionally taken verbatim so
// the JA3N, JA4 and Akamai fingerprints stay tied to the captured oracle.
func chrome146OracleProfile() profiles.ClientProfile {
	base := profiles.Chrome_146
	id := base.GetClientHelloId()
	factory := id.SpecFactory
	id.SpecFactory = func() (utls.ClientHelloSpec, error) {
		spec, err := factory()
		if err != nil {
			return utls.ClientHelloSpec{}, err
		}
		filtered := make([]utls.TLSExtension, 0, len(spec.Extensions))
		for _, extension := range spec.Extensions {
			if generic, ok := extension.(*utls.GenericExtension); ok && generic.Id == 0xca34 {
				continue
			}
			filtered = append(filtered, extension)
		}
		spec.Extensions = filtered
		return spec, nil
	}
	return profiles.NewClientProfile(
		id,
		base.GetSettings(),
		base.GetSettingsOrder(),
		base.GetPseudoHeaderOrder(),
		base.GetConnectionFlow(),
		base.GetPriorities(),
		base.GetHeaderPriority(),
		base.GetStreamID(),
		base.GetAllowHTTP(),
		base.GetHttp3Settings(),
		base.GetHttp3SettingsOrder(),
		base.GetHttp3PriorityParam(),
		base.GetHttp3PseudoHeaderOrder(),
		base.GetHttp3SendGreaseFrames(),
	)
}

func durationPtr(d time.Duration) *time.Duration { return &d }

func (t *chromeTransport) setProxy(raw string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.client == nil {
		if t.initErr != nil {
			return t.initErr
		}
		return errors.New("Chrome_146 transport is not initialized")
	}
	if err := t.client.SetProxy(raw); err != nil {
		return fmt.Errorf("configure Chrome proxy: %w", err)
	}
	t.proxyErr = nil
	return nil
}

func (t *chromeTransport) setProxyError(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.proxyErr = err
}

func (t *chromeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil {
		return nil, errors.New("Chrome_146 transport received a nil request")
	}
	if err := validateFetchURL(req.URL.String(), false); err != nil {
		return nil, err
	}
	// Resolve before handing the request to the third-party transport. This
	// makes the caller's resolver authoritative and rejects every address in a
	// mixed public/private answer before any socket is opened.
	if _, err := publicIPs(req.Context(), req.URL.Hostname(), t.resolve); err != nil {
		return nil, err
	}

	t.mu.RLock()
	client, initErr, proxyErr := t.client, t.initErr, t.proxyErr
	t.mu.RUnlock()
	if initErr != nil {
		return nil, initErr
	}
	if proxyErr != nil {
		return nil, proxyErr
	}
	if client == nil {
		return nil, errors.New("Chrome_146 transport has no client")
	}

	freq := chromeFRequest(req)
	fresp, err := client.Do(freq)
	if err != nil {
		return nil, fmt.Errorf("Chrome_146 request %s: %w", req.URL, err)
	}
	return chromeResponse(req, fresp), nil
}

func chromeFRequest(req *http.Request) *fhttp.Request {
	u := *req.URL
	header := make(fhttp.Header, len(req.Header)+1)
	for key, values := range req.Header {
		header[key] = append([]string(nil), values...)
	}
	// The userAgentTransport already applies these values for production
	// clients. Reapply them here so a direct adapter use cannot accidentally
	// lose the Chrome surface.
	header.Set("User-Agent", chromeUA)
	header.Set(headerAccept, chromeAccept)
	header.Set("Accept-Encoding", chromeAcceptEncoding)
	header.Set("Accept-Language", chromeAcceptLanguage)
	header.Set("Priority", chromePriority)
	header.Set("Sec-CH-UA", chromeSecCHUA)
	header.Set("Sec-CH-UA-Mobile", chromeSecCHUAMobile)
	header.Set("Sec-CH-UA-Platform", chromeSecCHUAPlatform)
	header.Set("Sec-Fetch-Dest", "document")
	header.Set("Sec-Fetch-Mode", "navigate")
	header.Set("Sec-Fetch-Site", "none")
	header.Set("Sec-Fetch-User", "?1")
	header.Set("Upgrade-Insecure-Requests", "1")
	header[fhttp.HeaderOrderKey] = append([]string(nil), chromeHeaderOrder...)
	return (&fhttp.Request{
		Method:        req.Method,
		URL:           &u,
		Header:        header,
		Body:          req.Body,
		GetBody:       req.GetBody,
		ContentLength: req.ContentLength,
		Host:          req.Host,
	}).WithContext(req.Context())
}

func chromeResponse(req *http.Request, resp *fhttp.Response) *http.Response {
	if resp == nil {
		return nil
	}
	header := make(http.Header, len(resp.Header))
	for key, values := range resp.Header {
		header[key] = append([]string(nil), values...)
	}
	trailer := make(http.Header, len(resp.Trailer))
	for key, values := range resp.Trailer {
		trailer[key] = append([]string(nil), values...)
	}
	return &http.Response{
		Status:           resp.Status,
		StatusCode:       resp.StatusCode,
		Proto:            resp.Proto,
		ProtoMajor:       resp.ProtoMajor,
		ProtoMinor:       resp.ProtoMinor,
		Header:           header,
		Body:             resp.Body,
		ContentLength:    resp.ContentLength,
		TransferEncoding: append([]string(nil), resp.TransferEncoding...),
		Close:            resp.Close,
		Uncompressed:     resp.Uncompressed,
		Trailer:          trailer,
		Request:          req,
	}
}

// chromeDialer pins direct origin connections to the already-validated public
// DNS answer. When a proxy is configured it establishes a CONNECT tunnel, so
// the target remains in the TLS/SNI request while the proxy itself is dialed
// normally (local proxies are a supported deployment configuration).
type chromeDialer struct {
	proxyURL *url.URL
	timeout  time.Duration
	resolve  func(context.Context, string) ([]net.IP, error)
}

func newChromeDialer(
	raw string,
	timeout time.Duration,
	resolve func(context.Context, string) ([]net.IP, error),
) (proxy.ContextDialer, error) {
	if strings.TrimSpace(raw) == "" {
		return &chromeDialer{timeout: timeout, resolve: resolve}, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid proxy URL %q", raw)
	}
	switch strings.ToLower(u.Scheme) {
	case schemeHTTP, schemeHTTPS, "socks5", "socks5h":
		return &chromeDialer{proxyURL: u, timeout: timeout, resolve: resolve}, nil
	default:
		return nil, fmt.Errorf("unsupported proxy scheme %q", u.Scheme)
	}
}

func (d *chromeDialer) Dial(network, address string) (net.Conn, error) {
	return d.DialContext(context.Background(), network, address)
}

func (d *chromeDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if d.proxyURL == nil {
		return d.dialPinned(ctx, network, address)
	}
	if strings.HasPrefix(strings.ToLower(d.proxyURL.Scheme), "socks5") {
		return d.dialSOCKS(ctx, network, address)
	}
	return d.dialCONNECT(ctx, network, address)
}

func (d *chromeDialer) dialPinned(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("split origin address %q: %w", address, err)
	}
	ips, err := publicIPs(ctx, host, d.resolve)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: d.timeout}
	var last error
	for _, ip := range ips {
		conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if dialErr == nil {
			return conn, nil
		}
		last = dialErr
	}
	if last != nil {
		return nil, last
	}
	return nil, fmt.Errorf("no public address for %s", host)
}

func (d *chromeDialer) dialSOCKS(ctx context.Context, network, address string) (net.Conn, error) {
	var auth *proxy.Auth
	if d.proxyURL.User != nil {
		password, _ := d.proxyURL.User.Password()
		auth = &proxy.Auth{User: d.proxyURL.User.Username(), Password: password}
	}
	dialer, err := proxy.SOCKS5("tcp", d.proxyURL.Host, auth, &net.Dialer{Timeout: d.timeout})
	if err != nil {
		return nil, fmt.Errorf("create SOCKS proxy dialer: %w", err)
	}
	if contextDialer, ok := dialer.(proxy.ContextDialer); ok {
		return contextDialer.DialContext(ctx, network, address)
	}
	return dialer.Dial(network, address)
}

func (d *chromeDialer) dialCONNECT(ctx context.Context, _, address string) (net.Conn, error) {
	proxyDialer := &net.Dialer{Timeout: d.timeout}
	raw, err := proxyDialer.DialContext(ctx, "tcp", d.proxyURL.Host)
	if err != nil {
		return nil, fmt.Errorf("connect to proxy %s: %w", d.proxyURL.Host, err)
	}
	closeRaw := func(e error) (net.Conn, error) {
		_ = raw.Close()
		return nil, e
	}
	if strings.EqualFold(d.proxyURL.Scheme, "https") {
		proxyTLS := tls.Client(raw, &tls.Config{ServerName: d.proxyURL.Hostname(), MinVersion: tls.VersionTLS12})
		if err := proxyTLS.HandshakeContext(ctx); err != nil {
			return closeRaw(fmt.Errorf("TLS handshake with proxy %s: %w", d.proxyURL.Host, err))
		}
		raw = proxyTLS
	}
	request := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Host: address},
		Host:   address,
		Header: make(http.Header),
	}
	if d.proxyURL.User != nil {
		request.Header.Set("Proxy-Authorization", basicProxyAuth(d.proxyURL))
	}
	if err := request.Write(raw); err != nil {
		return closeRaw(fmt.Errorf("write proxy CONNECT %s: %w", address, err))
	}
	response, err := http.ReadResponse(bufio.NewReader(raw), request)
	if err != nil {
		return closeRaw(fmt.Errorf("read proxy CONNECT %s: %w", address, err))
	}
	if err := response.Body.Close(); err != nil {
		return closeRaw(fmt.Errorf("close proxy CONNECT response %s: %w", address, err))
	}
	if response.StatusCode != http.StatusOK {
		return closeRaw(fmt.Errorf("proxy CONNECT %s returned %s", address, response.Status))
	}
	return raw, nil
}

func basicProxyAuth(u *url.URL) string {
	password, _ := u.User.Password()
	return "Basic " + basicAuth(u.User.Username(), password)
}

func basicAuth(username, password string) string {
	return encodeBase64(username + ":" + password)
}

// Kept as a tiny helper to avoid coupling the transport to any proxy package's
// request implementation.
func encodeBase64(value string) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var out strings.Builder
	for i := 0; i < len(value); i += 3 {
		n := uint32(value[i]) << 16
		if i+1 < len(value) {
			n |= uint32(value[i+1]) << 8
		}
		if i+2 < len(value) {
			n |= uint32(value[i+2])
		}
		out.WriteByte(alphabet[(n>>18)&63])
		out.WriteByte(alphabet[(n>>12)&63])
		if i+1 < len(value) {
			out.WriteByte(alphabet[(n>>6)&63])
		} else {
			out.WriteByte('=')
		}
		if i+2 < len(value) {
			out.WriteByte(alphabet[n&63])
		} else {
			out.WriteByte('=')
		}
	}
	return out.String()
}

var (
	_ http.RoundTripper   = (*chromeTransport)(nil)
	_ proxy.ContextDialer = (*chromeDialer)(nil)
)
