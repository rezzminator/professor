package harvest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"hostops/pfm/internal/clock"
	"hostops/pfm/internal/obs"
)

// DNS-over-HTTPS resolution for every harvester dial.
//
// Why this exists: a consumer ISP can answer an A/AAAA query for a source host
// with its own block address (measured on this fleet: a mirror host resolving
// to a single ISP-owned IP that serves a block page on every port). The system
// resolver has no way to tell that answer from a real one, so every rung —
// direct, chrome, provider, browser — dials the block page and reports a
// connect failure or a 403 that looks like the SOURCE refusing us. Resolving
// over HTTPS to a resolver the ISP cannot rewrite removes the whole class.
//
// This is NOT a security boundary. The SSRF guard is unchanged and still runs
// on every address this resolver returns (publicIPs rejects private ranges
// whatever the answer's provenance), so a hostile DoH answer can redirect a
// fetch to another PUBLIC host but can never reach an internal one.

const (
	// dohEndpoint is queried by IP (dohBootstrapIPs) so resolving the resolver
	// never depends on the resolver we are replacing.
	dohEndpoint = "https://cloudflare-dns.com/dns-query"
	// dohMinTTL / dohMaxTTL bound what a record may ask us to cache. A
	// zero-TTL record would make every dial a fresh HTTPS round trip; a
	// week-long one would outlive a real failover.
	dohMinTTL = 30 * time.Second
	dohMaxTTL = 10 * time.Minute
	// dohTimeout keeps a stalled resolver from owning the fetch's whole budget:
	// on timeout we fall back to the system resolver and the dial still happens.
	dohTimeout = 5 * time.Second
)

// dohBootstrapIPs are Cloudflare's anycast resolver addresses. They are dialed
// directly while the TLS handshake still verifies the cloudflare-dns.com name,
// so a hijacked route cannot impersonate the resolver — it can only fail the
// handshake, which lands in the system-resolver fallback below.
var dohBootstrapIPs = []string{"1.1.1.1:443", "1.0.0.1:443", "[2606:4700:4700::1111]:443"}

// sharedDOHResolver is the process-wide resolver. One instance means one TTL
// cache shared by every Harvester AND by the browser rung's host-resolver
// pinning below, so Chrome dials the same address the HTTP rungs validated
// rather than resolving the name a second time through the system resolver.
var sharedDOHResolver = sync.OnceValue(newDOHResolver)

// ResolvePublicHost resolves host through the shared DNS-over-HTTPS resolver.
// It is exported for the browser adapter, which must hand Chrome an explicit
// address: Chrome performs its own resolution with no pinning hop, so without
// this it would re-resolve through the very resolver DoH exists to bypass.
func ResolvePublicHost(ctx context.Context, host string) ([]net.IP, error) {
	return sharedDOHResolver().LookupIP(ctx, host)
}

// BrowserHostResolverRule returns the Chrome --host-resolver-rules value that
// pins rawURL's host to the addresses DoH returned, or "" when the host needs
// no pinning (a literal IP) or could not be resolved. Pinning also NARROWS the
// DNS-rebinding window AssertFetchableStrict documents as a residual risk:
// Chrome can no longer re-resolve the main host to a different address after
// the policy check approved it.
func BrowserHostResolverRule(ctx context.Context, rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" {
		return ""
	}
	host := parsed.Hostname()
	if net.ParseIP(host) != nil || isSpecialUseName(strings.ToLower(host)) {
		return ""
	}
	ips, err := ResolvePublicHost(ctx, host)
	if err != nil {
		// Not fatal: Chrome falls back to its own resolution. Say so, because a
		// silent miss here is exactly how the rewritten answer would creep back.
		log.Printf(
			"harvest: could not pin %s for the browser rung (%v) — Chrome will resolve it itself, which this network may rewrite",
			host,
			err,
		)
		return ""
	}
	return browserHostResolverRuleFrom(rawURL, ips)
}

// browserHostResolverRuleFrom renders the Chrome rule for rawURL given already
// -resolved addresses. Split from BrowserHostResolverRule so the pinning policy
// is testable without a resolver.
func browserHostResolverRuleFrom(rawURL string, ips []net.IP) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" {
		return ""
	}
	host := parsed.Hostname()
	if net.ParseIP(host) != nil || isSpecialUseName(strings.ToLower(host)) {
		return "" // a literal address needs no mapping; a special-use name is not ours to pin.
	}
	if len(ips) == 0 {
		return ""
	}
	// The SSRF guard already rejects a private answer on the HTTP rungs. Pinning
	// one here would hand Chrome the internal address that guard exists to
	// refuse, so the rule is withheld rather than narrowed.
	for _, ip := range ips {
		if privateIP(ip) {
			log.Printf(
				"harvest: refusing to pin %s for the browser rung: the resolver returned the private address %s",
				host,
				ip,
			)
			return ""
		}
	}
	// A rule pins Chrome to ONE address, and the browser rung has no
	// multi-address fallback of its own the way the HTTP dialer does. The
	// resolved set is sorted by string, so without this an IPv6 address could
	// win the pin purely on lexical order and strand the browser rung on a
	// route this host cannot reach while every HTTP rung succeeds over IPv4.
	pin := ips[0]
	for _, ip := range ips {
		if ip.To4() != nil {
			pin = ip
			break
		}
	}
	return fmt.Sprintf("MAP %s %s", host, pin.String())
}

// dohResolver answers host lookups over HTTPS, with a TTL cache and a system
// -resolver fallback. The zero value is not usable; build one with newDOHResolver.
type dohResolver struct {
	endpoint string
	client   *http.Client
	clock    clock.Clock

	mu    sync.Mutex
	cache map[string]dohEntry

	// fallback is the resolver used when DoH cannot answer. Injectable so a
	// test can assert the fallback path without a network.
	fallback func(context.Context, string) ([]net.IP, error)

	// warned tracks hosts we have already logged a fallback for, so a long
	// session on a DoH-blocked network reports the degradation once per host
	// instead of once per dial.
	warned map[string]bool
}

type dohEntry struct {
	ips     []net.IP
	expires time.Time
}

// newDOHResolver builds the production DoH resolver. Its HTTP client dials the
// bootstrap IPs directly and never re-enters harvester's own transports, so
// there is no resolution cycle.
func newDOHResolver() *dohResolver {
	dialer := &net.Dialer{Timeout: dohTimeout}
	transport := &http.Transport{
		// Every address is a literal, so DialContext ignores the requested
		// host entirely and tries each bootstrap IP in turn. TLS still
		// verifies the cloudflare-dns.com certificate via the URL's host.
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var last error
			for _, addr := range dohBootstrapIPs {
				if strings.HasPrefix(addr, "[") && !strings.Contains(network, "6") && network != "tcp" {
					continue
				}
				conn, err := dialer.DialContext(ctx, "tcp", addr)
				if err == nil {
					return conn, nil
				}
				last = err
			}
			if last == nil {
				last = fmt.Errorf("no DoH bootstrap address configured")
			}
			return nil, fmt.Errorf("dial DoH resolver: %w", last)
		},
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        4,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: dohTimeout,
	}
	return &dohResolver{
		endpoint: dohEndpoint,
		client:   obs.WrapClient(&http.Client{Transport: transport, Timeout: dohTimeout}),
		clock:    clock.Real,
		cache:    make(map[string]dohEntry),
		warned:   make(map[string]bool),
		fallback: func(ctx context.Context, host string) ([]net.IP, error) {
			return net.DefaultResolver.LookupIP(ctx, "ip", host)
		},
	}
}

// LookupIP resolves host over HTTPS, falling back to the system resolver when
// DoH cannot answer. The fallback is LOGGED, not silent: a session that
// silently degraded to the poisoned resolver would report the block page's
// failures as if they came from the source.
func (r *dohResolver) LookupIP(ctx context.Context, host string) ([]net.IP, error) {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" {
		return nil, fmt.Errorf("DoH lookup: empty host")
	}
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	// RFC 6761/6762 special-use names are never in the public DNS, so a public
	// resolver can only ever answer NXDOMAIN for them. Asking costs a round
	// trip and a misleading "falling back" warning on every lookup; worse, it
	// puts every local fixture host on the network. The system resolver is the
	// correct authority for these.
	if isSpecialUseName(host) {
		return r.fallback(ctx, host)
	}
	if ips, ok := r.cached(host); ok {
		return ips, nil
	}
	ips, err := r.query(ctx, host)
	if err == nil && len(ips) > 0 {
		return ips, nil
	}
	var nxdomain *dohNXDomainError
	if errors.As(err, &nxdomain) {
		// NXDOMAIN is an ANSWER, not an outage: the resolver looked and the
		// name does not exist. Falling back to the system resolver here is
		// exactly the bug this authoritative check exists to close — it would
		// let a poisoned system resolver override a real "no such host" with
		// its own address, and it would warn about a "failure" that never
		// happened.
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	if err == nil {
		err = fmt.Errorf("DoH returned no addresses for %s", host)
	}
	r.warnOnce(host, err)
	fallbackIPs, fallbackErr := r.fallback(ctx, host)
	if fallbackErr != nil {
		// Report BOTH failures. One of them alone reads as "the host does not
		// exist" when the real story may be "our resolver was unreachable".
		return nil, fmt.Errorf(
			"DoH lookup failed for %s (%v) and the system resolver also failed: %w",
			host,
			err,
			fallbackErr,
		)
	}
	return fallbackIPs, nil
}

// specialUseTLDs are the RFC 6761/6762 reserved suffixes plus the reserved
// second-level example names. None of them resolves in the public DNS.
var specialUseTLDs = []string{".test", ".invalid", ".localhost", ".local", ".example", ".internal", ".home.arpa"}

func isSpecialUseName(host string) bool {
	for _, suffix := range specialUseTLDs {
		if host == strings.TrimPrefix(suffix, ".") || strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

func (r *dohResolver) warnOnce(host string, err error) {
	r.mu.Lock()
	already := r.warned[host]
	r.warned[host] = true
	r.mu.Unlock()
	if !already {
		log.Printf(
			"harvest: DNS-over-HTTPS could not resolve %s (%v) — falling back to the system resolver, whose answers this network may rewrite",
			host,
			err,
		)
	}
}

func (r *dohResolver) cached(host string) ([]net.IP, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	watch := r.clock
	if watch == nil {
		watch = clock.Real
	}
	entry, ok := r.cache[host]
	if !ok || watch.Now().After(entry.expires) {
		return nil, false
	}
	return append([]net.IP(nil), entry.ips...), true
}

func (r *dohResolver) store(host string, ips []net.IP, ttl time.Duration) {
	if len(ips) == 0 {
		return
	}
	if ttl < dohMinTTL {
		ttl = dohMinTTL
	}
	if ttl > dohMaxTTL {
		ttl = dohMaxTTL
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	watch := r.clock
	if watch == nil {
		watch = clock.Real
	}
	r.cache[host] = dohEntry{ips: append([]net.IP(nil), ips...), expires: watch.Now().Add(ttl)}
}

// dohNXDomainError marks a DoH answer of Status 3 (NXDOMAIN): the resolver
// was reached and answered "this name does not exist". It is a distinct type
// so LookupIP can tell it apart from a transport failure or any other DNS
// status, all of which keep the system-resolver fallback.
type dohNXDomainError struct {
	host  string
	qtype string
}

func (e *dohNXDomainError) Error() string {
	if e.qtype == "" {
		return fmt.Sprintf("DoH query for %s returned NXDOMAIN", e.host)
	}
	return fmt.Sprintf("DoH %s query for %s returned NXDOMAIN", e.qtype, e.host)
}

// dohAnswer is the RFC 8484 JSON response shape (application/dns-json).
type dohAnswer struct {
	Status int `json:"Status"`
	Answer []struct {
		Type int    `json:"type"`
		TTL  int    `json:"TTL"`
		Data string `json:"data"`
	} `json:"Answer"`
}

// query asks for A and AAAA in parallel and merges the answers. Both are
// needed: a host reachable only over IPv6 must still resolve, and a poisoned
// A record is commonly paired with a poisoned AAAA.
func (r *dohResolver) query(ctx context.Context, host string) ([]net.IP, error) {
	ctx, cancel := context.WithTimeout(ctx, dohTimeout)
	defer cancel()

	type outcome struct {
		ips []net.IP
		ttl time.Duration
		err error
	}
	results := make([]outcome, 2)
	var wg sync.WaitGroup
	for i, qtype := range []string{"A", "AAAA"} {
		wg.Add(1)
		go func(i int, qtype string) {
			defer wg.Done()
			ips, ttl, err := r.queryType(ctx, host, qtype)
			results[i] = outcome{ips: ips, ttl: ttl, err: err}
		}(i, qtype)
	}
	wg.Wait()

	var all []net.IP
	ttl := dohMaxTTL
	var firstErr error
	allNXDomain := true
	for _, res := range results {
		if res.err != nil {
			if firstErr == nil {
				firstErr = res.err
			}
			var nxdomain *dohNXDomainError
			if !errors.As(res.err, &nxdomain) {
				allNXDomain = false
			}
			continue
		}
		allNXDomain = false
		all = append(all, res.ips...)
		if len(res.ips) > 0 && res.ttl < ttl {
			ttl = res.ttl
		}
	}
	if len(all) == 0 {
		// Only when EVERY query type came back NXDOMAIN is the name itself
		// answered as not existing; a mix of NXDOMAIN and a transport failure
		// or SERVFAIL is still an outage, not an authoritative answer, and
		// keeps today's system-resolver fallback.
		if allNXDomain && firstErr != nil {
			return nil, &dohNXDomainError{host: host}
		}
		if firstErr != nil {
			return nil, firstErr
		}
		return nil, nil
	}
	// Deterministic order keeps a retry dialing the same address first, which
	// makes a partially-reachable host behave consistently across runs.
	sort.Slice(all, func(i, j int) bool { return all[i].String() < all[j].String() })
	r.store(host, all, ttl)
	return all, nil
}

func (r *dohResolver) queryType(ctx context.Context, host, qtype string) ([]net.IP, time.Duration, error) {
	endpoint := r.endpoint + "?name=" + url.QueryEscape(host) + "&type=" + qtype
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return nil, 0, fmt.Errorf("build DoH %s request for %s: %w", qtype, host, err)
	}
	req.Header.Set(headerAccept, "application/dns-json")
	req.Header.Set("User-Agent", defaultUA)
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("DoH %s query for %s: %w", qtype, host, err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			log.Printf("harvest: closing DoH %s response for %s: %v", qtype, host, closeErr)
		}
	}()
	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("DoH %s query for %s returned HTTP %d", qtype, host, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, 0, fmt.Errorf("read DoH %s response for %s: %w", qtype, host, err)
	}
	var answer dohAnswer
	if err := json.Unmarshal(body, &answer); err != nil {
		return nil, 0, fmt.Errorf("decode DoH %s response for %s: %w", qtype, host, err)
	}
	// Status 0 is NOERROR; 3 is NXDOMAIN, a real answer meaning the name does
	// not exist — tagged with dohNXDomainError so LookupIP can treat it as
	// authoritative rather than folding it into the generic fallback path
	// below with SERVFAIL and every other non-zero status.
	if answer.Status == 3 {
		return nil, 0, &dohNXDomainError{host: host, qtype: qtype}
	}
	if answer.Status != 0 {
		return nil, 0, fmt.Errorf("DoH %s query for %s returned DNS status %d", qtype, host, answer.Status)
	}
	wantType := 1
	if qtype == "AAAA" {
		wantType = 28
	}
	var ips []net.IP
	ttl := dohMaxTTL
	for _, record := range answer.Answer {
		if record.Type != wantType {
			continue // CNAME hops in the chain; only the address records matter.
		}
		ip := net.ParseIP(strings.TrimSpace(record.Data))
		if ip == nil {
			log.Printf("harvest: DoH %s answer for %s carried an unparseable address %q", qtype, host, record.Data)
			continue
		}
		ips = append(ips, ip)
		if recordTTL := time.Duration(record.TTL) * time.Second; recordTTL < ttl {
			ttl = recordTTL
		}
	}
	return ips, ttl, nil
}
