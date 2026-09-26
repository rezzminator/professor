package harvestmcp

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// L2-F21: maxConsentAttempts (auth.go) bounds guesses PER consent
// transaction, but /authorize mints a fresh transaction — and its own
// 5-try budget — on every request, so passphrase guessing was unbounded on
// the internet-facing gateway. passphraseLimiter is the real backstop: a
// lockout keyed by the caller's source address, plus a global one so a
// botnet spreading guesses across addresses cannot outrun the per-address
// limit. Chosen shape: N strikes locks that bucket out for a fixed window,
// then resets — simple, and every strike after lockout is free (it does not
// extend the lockout), so a flood cannot keep it locked forever.
const (
	passphraseAddrMaxFailures   = 10
	passphraseAddrLockout       = 15 * time.Minute
	passphraseGlobalMaxFailures = 50
	passphraseGlobalLockout     = 15 * time.Minute
)

type attemptWindow struct {
	failures    int
	lockedUntil time.Time
}

// recordFailureLocked adds one strike, locking the window for lockout once
// limit is reached. A strike against an already-locked window is a no-op —
// it does not extend the lockout, so a flood cannot keep it locked forever.
func (w *attemptWindow) recordFailureLocked(now time.Time, limit int, lockout time.Duration) {
	if w.lockedUntil.After(now) {
		return
	}
	w.failures++
	if w.failures >= limit {
		w.lockedUntil = now.Add(lockout)
		w.failures = 0
	}
}

// passphraseLimiter bounds passphrase guesses across every /authorize
// transaction a caller mints, keyed by source address, plus a global bound.
type passphraseLimiter struct {
	mu     sync.Mutex
	byAddr map[string]*attemptWindow
	global attemptWindow
}

func newPassphraseLimiter() *passphraseLimiter {
	return &passphraseLimiter{byAddr: map[string]*attemptWindow{}}
}

// allowed reports whether addr may attempt a passphrase right now: neither
// the global nor its own window is locked.
func (l *passphraseLimiter) allowed(addr string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.global.lockedUntil.After(now) {
		return false
	}
	if w, found := l.byAddr[addr]; found && w.lockedUntil.After(now) {
		return false
	}
	return true
}

// recordFailure records one wrong guess against both the global window and
// addr's own.
func (l *passphraseLimiter) recordFailure(addr string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.global.recordFailureLocked(now, passphraseGlobalMaxFailures, passphraseGlobalLockout)
	w, found := l.byAddr[addr]
	if !found {
		w = &attemptWindow{}
		l.byAddr[addr] = w
	}
	w.recordFailureLocked(now, passphraseAddrMaxFailures, passphraseAddrLockout)
}

// recordSuccess clears addr's own window; the global counter is left as is —
// one address succeeding does not un-suspect the others sharing the global
// budget.
func (l *passphraseLimiter) recordSuccess(addr string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.byAddr, addr)
}

// remoteAddr is the source-address key a passphrase attempt is bucketed
// under: req.RemoteAddr's host, port stripped (many requests from the same
// caller carry a different ephemeral port each time) — the raw value on a
// SplitHostPort failure, so a malformed RemoteAddr still buckets somewhere
// rather than being silently unmetered.
func remoteAddr(req *http.Request) string {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		return req.RemoteAddr
	}
	return host
}

// maxRegisteredClients bounds how many clients /register may accumulate.
// Chosen policy: refuse once at the ceiling rather than silently evicting an
// oldest-but-still-used client — an operator running this gateway for a
// handful of AI clients will not hit it; hitting it is a signal to prune by
// hand, not a reason to guess which registration nobody needs anymore.
const maxRegisteredClients = 64

// oauthErrorTooManyClients is a real RFC 6749 error code ("the authorization
// server is currently unable to handle the request due to a temporary
// overloading") reused for a registration refused at the client-count
// ceiling — not invented, since RFC 7591 defines no dedicated one.
const oauthErrorTooManyClients = "temporarily_unavailable"
