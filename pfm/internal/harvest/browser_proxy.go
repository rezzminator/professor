package harvest

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// The browser rung's DIAL half (L2-F7). Every URL Chrome touches is validated
// by Go through the worker's ask protocol (AssertFetchableStrict), but the
// connection that follows is Chrome's own: it resolves the host a SECOND time,
// so a TTL-0 rebind record reaches 127.0.0.1 or 169.254.169.254 with a public
// address on the validation record. StartBrowserProxy closes that gap by
// owning the dial — Chrome is launched with this loopback proxy as its only
// way out (browser.py PROXY_REQUIRED), and every byte it sends leaves through
// pinnedDialContext, the same pinned dialer every other client in this package
// uses. A private, loopback or link-local target is refused by publicIPs, the
// one rule the package applies everywhere else.

// browserProxyRefused is the reason phrase the proxy answers a target its own
// SSRF policy refuses. It is a POLICY refusal, distinct from an outage: a
// target that could not be REACHED gets browserProxyUnreachable instead, so a
// blocked rebind and a dead upstream never read as one thing.
const (
	browserProxyRefused     = "harvester refused a private or internal host"
	browserProxyUnreachable = "harvester could not reach the target"
	browserProxyBadRequest  = "harvester proxy could not parse the request"
)

// browserProxyDialer is the seam the proxy's outbound dial goes through:
// pinnedDialContext, unchanged. It is a var for exactly ONE reason — a unit
// test cannot host a fixture server on a public address, so the pass-through
// test swaps it for a dialer that reaches its own loopback server. It is
// unexported and reachable from neither config nor env, the same relaxation
// shape lookupIP (net.go) carries for the pre-check; the refusal tests keep
// the production value so what they prove is the production policy.
var browserProxyDialer = pinnedDialContext

// StartBrowserProxy runs the browser rung's loopback HTTP proxy until stop is
// called or ctx is cancelled, whichever comes first. It returns the proxy URL
// to hand Chrome (http://127.0.0.1:<ephemeral port>) and the stop function
// that closes the listener and every connection it accepted — no goroutine
// this function starts outlives stop. A nil resolve installs the
// DNS-over-HTTPS resolver (ResolvePublicHost), matching NewDirectClient.
//
// The proxy is loopback-only and lives for one fetch: nothing off the box can
// reach it, and the caller's context (harvestmcp's browserHardDeadline) bounds
// how long a stalled client can hold a connection open, because stop closes
// the connection out from under it.
func StartBrowserProxy(
	ctx context.Context,
	resolve func(context.Context, string) ([]net.IP, error),
) (string, func(), error) {
	if resolve == nil {
		resolve = ResolvePublicHost
	}
	listener, err := listenLoopback()
	if err != nil {
		return "", nil, fmt.Errorf("listen on loopback for the browser proxy: %w", err)
	}
	proxyCtx, cancel := context.WithCancel(ctx)
	proxy := &browserProxy{
		listener: listener,
		dial:     browserProxyDialer(resolve),
		conns:    make(map[net.Conn]struct{}),
		ctx:      proxyCtx,
		cancel:   cancel,
	}
	proxy.wg.Add(2)
	go func() {
		defer proxy.wg.Done()
		proxy.accept()
	}()
	go func() {
		defer proxy.wg.Done()
		<-proxyCtx.Done()
		proxy.shutdown()
	}()
	return "http://" + listener.Addr().String(), proxy.stop, nil
}

// browserProxy is one running loopback proxy. Every connection it accepts —
// client side and upstream side both — is tracked so shutdown can close them
// all; that is what makes stop's "no goroutine outlives me" guarantee true
// even for a peer that has gone silent mid-body.
type browserProxy struct {
	listener loopbackListener
	dial     func(context.Context, string, string) (net.Conn, error)
	ctx      context.Context
	cancel   context.CancelFunc

	wg      sync.WaitGroup
	once    sync.Once
	mu      sync.Mutex
	conns   map[net.Conn]struct{}
	stopped bool
}

func (p *browserProxy) stop() {
	p.shutdown()
	p.wg.Wait()
}

// shutdown is idempotent and never waits: the ctx watcher calls it from
// INSIDE the WaitGroup stop waits on, so a shutdown that blocked on the group
// would deadlock its own waiter.
func (p *browserProxy) shutdown() {
	p.once.Do(func() {
		p.cancel()
		p.mu.Lock()
		p.stopped = true
		conns := make([]net.Conn, 0, len(p.conns))
		for conn := range p.conns {
			conns = append(conns, conn)
		}
		p.conns = nil
		p.mu.Unlock()
		if err := p.listener.Close(); err != nil {
			obs.Logger(p.ctx).Warn("browser proxy listener close failed", obs.FieldErr, err.Error())
		}
		for _, conn := range conns {
			if err := conn.Close(); err != nil {
				obs.Logger(p.ctx).Debug("browser proxy connection close failed", obs.FieldErr, err.Error())
			}
		}
	})
}

// track registers conn for shutdown's sweep. It reports false once shutdown
// has run — the connection is then closed by the caller rather than served,
// so a connection accepted in the race window cannot outlive stop.
func (p *browserProxy) track(conn net.Conn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return false
	}
	p.conns[conn] = struct{}{}
	return true
}

func (p *browserProxy) forget(conn net.Conn) {
	p.mu.Lock()
	if p.conns != nil {
		delete(p.conns, conn)
	}
	p.mu.Unlock()
	if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		obs.Logger(p.ctx).Debug("browser proxy connection close failed", obs.FieldErr, err.Error())
	}
}

func (p *browserProxy) accept() {
	for {
		conn, err := p.listener.Accept()
		if err != nil {
			// A closed listener is this proxy's own stop, not a fault; every
			// other accept error is named, because a proxy that stopped
			// accepting silently would look to Chrome exactly like a site
			// that refused to answer.
			if !errors.Is(err, net.ErrClosed) {
				obs.Logger(p.ctx).Warn("browser proxy stopped accepting", obs.FieldErr, err.Error())
			}
			return
		}
		if !p.track(conn) {
			if closeErr := conn.Close(); closeErr != nil {
				obs.Logger(p.ctx).Debug("browser proxy late connection close failed", obs.FieldErr, closeErr.Error())
			}
			return
		}
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			p.serve(conn)
		}()
	}
}

func (p *browserProxy) serve(conn net.Conn) {
	defer p.forget(conn)
	reader := bufio.NewReader(conn)
	request, err := http.ReadRequest(reader)
	if err != nil {
		if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
			obs.Logger(p.ctx).Debug("browser proxy request read failed", obs.FieldErr, err.Error())
			p.answer(conn, http.StatusBadRequest, browserProxyBadRequest, err.Error())
		}
		return
	}
	if request.Method == http.MethodConnect {
		p.tunnel(conn, reader, request)
		return
	}
	p.forward(conn, request)
}

// tunnel serves CONNECT: the target is dialed through the pinned dialer and
// the two connections are spliced. Chrome's TLS handshake runs end to end, so
// the proxy never sees plaintext and never terminates TLS.
func (p *browserProxy) tunnel(conn net.Conn, buffered *bufio.Reader, request *http.Request) {
	address := browserProxyAddress(request.Host, "", "443")
	upstream, ok := p.dialTarget(conn, "https://"+address, address)
	if !ok {
		return
	}
	defer p.forget(upstream)
	if _, err := io.WriteString(conn, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
		obs.Logger(p.ctx).Debug("browser proxy tunnel ack failed", obs.FieldErr, err.Error())
		return
	}
	p.splice(conn, buffered, upstream)
}

// forward serves a plain-HTTP proxy request (absolute-form request URI). The
// request is written to the pinned upstream connection in origin form and the
// response is streamed back verbatim: this proxy is a pipe, never a cache and
// never a rewriter. It asks the upstream to close after one exchange so a
// single client connection can never be pinned to a host the NEXT request was
// not validated against; Chrome opens a fresh proxy connection per hop.
func (p *browserProxy) forward(conn net.Conn, request *http.Request) {
	if request.URL == nil || request.URL.Host == "" {
		p.answer(conn, http.StatusBadRequest, browserProxyBadRequest,
			"a proxy request must carry an absolute URL")
		return
	}
	address := browserProxyAddress(request.URL.Host, request.URL.Scheme, "80")
	upstream, ok := p.dialTarget(conn, request.URL.String(), address)
	if !ok {
		return
	}
	defer p.forget(upstream)
	outbound := request.Clone(p.ctx)
	outbound.Close = true
	outbound.Header.Del("Proxy-Connection")
	outbound.Header.Del("Proxy-Authorization")
	if err := outbound.Write(upstream); err != nil {
		obs.Logger(p.ctx).Debug("browser proxy upstream write failed", obs.FieldErr, err.Error())
		p.answer(conn, http.StatusBadGateway, browserProxyUnreachable, err.Error())
		return
	}
	if _, err := io.Copy(conn, upstream); err != nil && !errors.Is(err, net.ErrClosed) {
		obs.Logger(p.ctx).Debug("browser proxy response relay ended", obs.FieldErr, err.Error())
	}
}

// dialTarget applies the package's own fetch policy to raw (scheme, userinfo,
// the private/internal suffix list) and then dials address through the pinned
// dialer, which re-applies the private-address rule to what DNS actually
// answered. A refusal and an outage are answered with DIFFERENT statuses:
// Chrome renders the reason, so the agent reading the page sees which one
// happened instead of one indistinguishable failure.
func (p *browserProxy) dialTarget(conn net.Conn, raw, address string) (net.Conn, bool) {
	if err := AssertFetchable(raw); err != nil {
		p.answer(conn, http.StatusForbidden, browserProxyRefused, err.Error())
		return nil, false
	}
	upstream, err := p.dial(p.ctx, "tcp", address)
	if err != nil {
		status, reason := http.StatusBadGateway, browserProxyUnreachable
		if errorKind(err) == errorKindBlocked {
			status, reason = http.StatusForbidden, browserProxyRefused
		}
		p.answer(conn, status, reason, err.Error())
		return nil, false
	}
	if !p.track(upstream) {
		if closeErr := upstream.Close(); closeErr != nil {
			obs.Logger(p.ctx).Debug("browser proxy late upstream close failed", obs.FieldErr, closeErr.Error())
		}
		return nil, false
	}
	return upstream, true
}

// splice copies both directions and returns only when both are done, so the
// connection's goroutines are finished before serve's deferred close runs —
// and therefore before stop's WaitGroup can clear.
func (p *browserProxy) splice(client net.Conn, buffered *bufio.Reader, upstream net.Conn) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := io.Copy(upstream, buffered); err != nil && !errors.Is(err, net.ErrClosed) {
			obs.Logger(p.ctx).Debug("browser proxy client relay ended", obs.FieldErr, err.Error())
		}
		if closer, ok := upstream.(*net.TCPConn); ok {
			if err := closer.CloseWrite(); err != nil && !errors.Is(err, net.ErrClosed) {
				obs.Logger(p.ctx).Debug("browser proxy upstream half-close failed", obs.FieldErr, err.Error())
			}
		}
	}()
	if _, err := io.Copy(client, upstream); err != nil && !errors.Is(err, net.ErrClosed) {
		obs.Logger(p.ctx).Debug("browser proxy upstream relay ended", obs.FieldErr, err.Error())
	}
	// Unblock the client→upstream copy for a peer that stopped sending
	// without closing; the deferred forget would do it, but only after this
	// function returns, and it must not return with that goroutine live.
	if err := client.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		obs.Logger(p.ctx).Debug("browser proxy client close failed", obs.FieldErr, err.Error())
	}
	<-done
}

// answer writes one bounded plain-text refusal. The reason phrase is one of
// this file's constants; detail is the underlying error, flattened to a single
// line so nothing a peer controls can inject a header into the status line.
func (p *browserProxy) answer(conn net.Conn, status int, reason, detail string) {
	body := reason + ": " + browserProxyDetail(detail) + "\n"
	head := fmt.Sprintf(
		"HTTP/1.1 %d %s\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Length: %d\r\nConnection: close\r\n\r\n",
		status, reason, len(body),
	)
	if _, err := io.WriteString(conn, head+body); err != nil {
		obs.Logger(p.ctx).Debug("browser proxy refusal write failed", obs.FieldErr, err.Error())
	}
}

// browserProxyDetail flattens an error into one short header-safe line.
func browserProxyDetail(detail string) string {
	const maxDetail = 200
	flat := strings.NewReplacer("\r", " ", "\n", " ").Replace(strings.TrimSpace(detail))
	if len(flat) > maxDetail {
		flat = flat[:maxDetail] + "…"
	}
	if flat == "" {
		return "no reason reported"
	}
	return flat
}

// browserProxyAddress normalises a proxy target to host:port, defaulting the
// port from the scheme when the client omitted it.
func browserProxyAddress(host, scheme, fallbackPort string) string {
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	port := fallbackPort
	if strings.EqualFold(scheme, schemeHTTPS) {
		port = "443"
	}
	return net.JoinHostPort(strings.Trim(host, "[]"), port)
}
