package harvestmcp

import (
	"testing"
	"time"

	"hostops/pfm/internal/clock"
)

// TestPassphraseLimiterLocksOutAcrossTransactionsThenRecovers is L2-F21's
// passphrase half: maxConsentAttempts bounds guesses per transaction, but
// /authorize mints a fresh transaction on every request, so guessing was
// unbounded without the limiter — a new txn per attempt (never reusing one,
// so maxConsentAttempts itself never trips) proves the limiter, not the
// per-transaction cap, is what stops the flood.
func TestPassphraseLimiterLocksOutAcrossTransactionsThenRecovers(t *testing.T) {
	fake := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	store := newAuthStore(legacyPublicURL, legacyPublicURL+"/mcp", legacyPass, "", "", fake)
	client := oauthClient{persistedClient: persistedClient{
		ClientID: "client-1", RedirectURIs: []string{legacyRedirect}, TokenEndpointAuthMethod: tokenAuthNone,
	}}
	_, challenge := legacyPKCE()
	const addr = "203.0.113.7:54321"

	newTxn := func() string {
		t.Helper()
		txn, err := store.begin(client, legacyRedirect, "", challenge, pkceMethodS256, "", []string{HarvesterScope})
		if err != nil {
			t.Fatal(err)
		}
		return txn
	}

	for i := range passphraseAddrMaxFailures {
		txn := newTxn()
		if _, ok, alive := store.consent(txn, "wrong-passphrase", addr); ok || !alive {
			t.Fatalf("attempt %d: ok=%t alive=%t, want a plain wrong-passphrase refusal", i, ok, alive)
		}
	}
	// The limiter is now locked for addr: even the CORRECT passphrase is
	// refused, indistinguishably (ok=false, alive=true) from a wrong guess —
	// the caller never learns it tripped a lockout rather than mistyping.
	if _, ok, alive := store.consent(newTxn(), legacyPass, addr); ok || !alive {
		t.Fatalf("locked-out attempt with the correct passphrase = ok=%t alive=%t, want refused", ok, alive)
	}
	// A different source address is unaffected by addr's lockout.
	if code, ok, alive := store.consent(newTxn(), legacyPass, "198.51.100.9:1111"); !ok || !alive || code == "" {
		t.Fatalf("a different address was refused by addr's own lockout: code=%q ok=%t alive=%t", code, ok, alive)
	}

	fake.Advance(passphraseAddrLockout)
	if code, ok, alive := store.consent(newTxn(), legacyPass, addr); !ok || !alive || code == "" {
		t.Fatalf("attempt after the lockout window = code=%q ok=%t alive=%t, want it to succeed", code, ok, alive)
	}
}

// TestRegisterRefusesPastTheClientCeiling is L2-F21's registration half:
// /register was unthrottled and persisted every client forever.
func TestRegisterRefusesPastTheClientCeiling(t *testing.T) {
	store := newAuthStore(legacyPublicURL, legacyPublicURL+"/mcp", legacyPass, "", "")
	for i := range maxRegisteredClients {
		if _, _, code := store.register(persistedClient{
			RedirectURIs: []string{legacyRedirect}, TokenEndpointAuthMethod: tokenAuthNone,
		}); code != "" {
			t.Fatalf("registration %d refused early: %s", i, code)
		}
	}
	_, _, code := store.register(persistedClient{
		RedirectURIs: []string{legacyRedirect}, TokenEndpointAuthMethod: tokenAuthNone,
	})
	if code != oauthErrorTooManyClients {
		t.Fatalf("registration past the ceiling = %q, want %q", code, oauthErrorTooManyClients)
	}
}
