package harvestmcp

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// TestAuthStoreMigratesACleartextClientSecretOnLoad is L2-F20's migration
// half: a state.json written before this fix (the shape the old code
// produced — a bare "client_secret") is rewritten atomically the moment it
// is loaded, so the plaintext never survives past the first open, and the
// client's digest is what load keeps in memory.
func TestAuthStoreMigratesACleartextClientSecretOnLoad(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "auth.json")
	legacy := authState{
		Clients: []persistedClient{{
			ClientID:                "legacy-client",
			ClientSecret:            "legacy-plaintext-secret",
			TokenEndpointAuthMethod: tokenAuthClientSecretBasic,
			RedirectURIs:            []string{legacyRedirect},
			GrantTypes:              []string{authorizationCodeGrant},
			ResponseTypes:           []string{"code"},
			Scope:                   HarvesterScope,
		}},
		Refresh: []persistedRefresh{
			{Hash: digest("some-refresh-token"), ClientID: "legacy-client", Scopes: []string{HarvesterScope}},
		},
	}
	body, err := json.MarshalIndent(legacy, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, body, 0o600); err != nil {
		t.Fatal(err)
	}

	store := newAuthStore(legacyPublicURL, legacyPublicURL+"/mcp", legacyPass, "", statePath)

	rewritten, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rewritten), "legacy-plaintext-secret") {
		t.Fatalf("the cleartext secret survived the migration rewrite: %s", rewritten)
	}
	if !strings.Contains(string(rewritten), `"client_secret_hash"`) {
		t.Fatalf("the migration did not persist a digest: %s", rewritten)
	}
	// The refresh token planted alongside the legacy client must survive the
	// migration rewrite untouched — a client-secret migration must never
	// silently drop a live refresh token.
	if !strings.Contains(string(rewritten), digest("some-refresh-token")) {
		t.Fatalf("the migration rewrite dropped an unrelated refresh record: %s", rewritten)
	}

	store.mu.Lock()
	client, ok := store.clients["legacy-client"]
	store.mu.Unlock()
	if !ok {
		t.Fatal("the migrated client was not loaded")
	}
	if client.ClientSecret != "" {
		t.Fatalf("the in-memory client still carries the cleartext secret: %+v", client)
	}
	if client.ClientSecretHash != digest("legacy-plaintext-secret") {
		t.Fatalf("the in-memory client's digest = %q, want digest(legacy-plaintext-secret)", client.ClientSecretHash)
	}
}

// TestRemoteLegacyStateFileStoresNoConfidentialClientSecret extends
// TestRemoteLegacy_StateFileStoresNoLiveTokens's coverage (remote_legacy_
// parity_test.go is at its C2 line ceiling — this sibling carries the new
// case instead, C13): a confidential client's secret must never reach the
// state file in cleartext either, only its digest, the same as a refresh
// token, and the client must still authenticate correctly against it.
func TestRemoteLegacyStateFileStoresNoConfidentialClientSecret(t *testing.T) {
	server := legacyNewRemote(t, legacyPublicURL, legacyPass, "")
	confidentialID, confidentialSecret, _ := legacyRegister(t, server, tokenAuthClientSecretBasic, legacyRedirect)
	if confidentialSecret == "" {
		t.Fatal("registration omitted client_secret for a confidential client")
	}
	code, verifier := legacyAuthorizationCode(t, server, confidentialID)
	if _, rec := legacyToken(
		t,
		server,
		url.Values{
			"grant_type":    {"authorization_code"},
			"code":          {code},
			"redirect_uri":  {legacyRedirect},
			"client_id":     {confidentialID},
			"code_verifier": {verifier},
		},
		http.Header{"Authorization": {legacyBasic(confidentialID, confidentialSecret)}},
	); rec.Code != http.StatusOK {
		t.Fatalf("confidential client authentication (by digest) failed: %d %s", rec.Code, rec.Body.String())
	}
	b, err := os.ReadFile(server.store.statePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), confidentialSecret) {
		t.Fatalf("state contains the confidential client's secret in cleartext: %s", b)
	}
	if !strings.Contains(string(b), `"client_secret_hash"`) {
		t.Fatalf("state does not persist the client secret as a digest: %s", b)
	}
}

// TestAuthenticateClientVerifiesByDigestNotPlaintext pins authenticateClient
// (remote.go) against the digest a confidential client is now stored under.
func TestAuthenticateClientVerifiesByDigestNotPlaintext(t *testing.T) {
	server := legacyNewRemote(t, legacyPublicURL, legacyPass, "")
	clientID, secret, _ := legacyRegister(t, server, tokenAuthClientSecretBasic, legacyRedirect)
	if !server.authenticateClient(clientID, secret, true) {
		t.Fatal("authenticateClient refused the correct secret")
	}
	if server.authenticateClient(clientID, "wrong-secret", true) {
		t.Fatal("authenticateClient accepted the wrong secret")
	}
	store := server.store
	store.mu.Lock()
	stored := store.clients[clientID]
	store.mu.Unlock()
	if stored.ClientSecret != "" {
		t.Fatalf("the registered client's in-memory record still carries a cleartext secret: %+v", stored)
	}
}

// TestBeginRefusesPastThePendingCeilingUntilExpiredOnesAreSwept: every
// /authorize request mints a pending transaction, so begin refuses once
// maxPendingConsents are open — and sweeps expired ones before counting, so
// abandoned transactions never hold the gateway shut past consentTTL.
func TestBeginRefusesPastThePendingCeilingUntilExpiredOnesAreSwept(t *testing.T) {
	fake := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	store := newAuthStore(legacyPublicURL, legacyPublicURL+"/mcp", legacyPass, "", "", fake)
	client := oauthClient{persistedClient: persistedClient{
		ClientID: "client-1", RedirectURIs: []string{legacyRedirect}, TokenEndpointAuthMethod: tokenAuthNone,
	}}
	_, challenge := legacyPKCE()
	begin := func() error {
		_, err := store.begin(client, legacyRedirect, "", challenge, pkceMethodS256, "", []string{HarvesterScope})
		return err
	}
	for i := range maxPendingConsents {
		if err := begin(); err != nil {
			t.Fatalf("authorization %d refused early: %v", i, err)
		}
	}
	if err := begin(); !errors.Is(err, errTooManyPendingConsents) {
		t.Fatalf("authorization past the ceiling = %v, want %v", err, errTooManyPendingConsents)
	}
	store.mu.Lock()
	open := len(store.pending)
	store.mu.Unlock()
	if open != maxPendingConsents {
		t.Fatalf("pending transactions = %d, want exactly the ceiling %d", open, maxPendingConsents)
	}
	fake.Advance(consentTTL + time.Second)
	if err := begin(); err != nil {
		t.Fatalf("authorization after every open one expired = %v, want the sweep to make room", err)
	}
	store.mu.Lock()
	open = len(store.pending)
	store.mu.Unlock()
	if open != 1 {
		t.Fatalf("pending transactions after the sweep = %d, want only the new one", open)
	}
}

// TestRegisterHoldsTheClientCeilingUnderConcurrency is
// TestRegisterRefusesPastTheClientCeiling raced: concurrent /register calls
// must not all pass the ceiling check before any of them inserts. It counts
// the result, so it catches the overshoot without -race.
func TestRegisterHoldsTheClientCeilingUnderConcurrency(t *testing.T) {
	for round := range 20 {
		store := newAuthStore(legacyPublicURL, legacyPublicURL+"/mcp", legacyPass, "", "")
		start := make(chan struct{})
		var wg sync.WaitGroup
		var mu sync.Mutex
		accepted := 0
		for range 4 * maxRegisteredClients {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, _, code := store.register(persistedClient{
					RedirectURIs: []string{legacyRedirect}, TokenEndpointAuthMethod: tokenAuthNone,
				})
				if code == "" {
					mu.Lock()
					accepted++
					mu.Unlock()
				} else if code != oauthErrorTooManyClients {
					t.Errorf("round %d: registration refused with %q, want %q", round, code, oauthErrorTooManyClients)
				}
			}()
		}
		close(start)
		wg.Wait()
		store.mu.Lock()
		stored := len(store.clients)
		store.mu.Unlock()
		if accepted != maxRegisteredClients || stored != maxRegisteredClients {
			t.Fatalf("round %d: accepted %d, stored %d clients, want exactly the ceiling %d",
				round, accepted, stored, maxRegisteredClients)
		}
	}
}

// TestConsentEntropyFailureIsNotAnExpiredTransaction: the right passphrase
// whose authorization code cannot be minted is a server failure — logged
// with its cause and rendered as a 500 — never the "link expired" page an
// unknown or stale transaction gets.
func TestConsentEntropyFailureIsNotAnExpiredTransaction(t *testing.T) {
	fake := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	store := newAuthStore(legacyPublicURL, legacyPublicURL+"/mcp", legacyPass, "", "", fake)
	client := oauthClient{persistedClient: persistedClient{
		ClientID: "client-1", RedirectURIs: []string{legacyRedirect}, TokenEndpointAuthMethod: tokenAuthNone,
	}}
	_, challenge := legacyPKCE()
	txn, err := store.begin(client, legacyRedirect, "", challenge, pkceMethodS256, "", []string{HarvesterScope})
	if err != nil {
		t.Fatal(err)
	}
	store.mint = func(int) (string, error) { return "", errors.New("entropy source exhausted") }
	_, recorder := obs.Test(t)
	const addr = "203.0.113.7:54321"

	code, ok, alive := store.consent(txn, legacyPass, addr)
	expiredCode, expiredOK, expiredAlive := store.consent("no-such-txn", legacyPass, addr)
	if code != "" || !ok || alive {
		t.Fatalf("entropy failure = (%q, %v, %v), want (\"\", true, false)", code, ok, alive)
	}
	if code == expiredCode && ok == expiredOK && alive == expiredAlive {
		t.Fatalf("entropy failure and an expired transaction both returned (%q, %v, %v)", code, ok, alive)
	}
	logged := false
	for _, record := range recorder.Records() {
		if record.Message == "harvester.auth.consent.mint" {
			if got, _ := record.Field(obs.FieldErr); strings.Contains(asString(got), "entropy source exhausted") {
				logged = true
			}
		}
	}
	if !logged {
		t.Fatalf("the entropy failure was not logged with its cause: %s", recorder.Raw())
	}

	server := legacyNewRemote(t, legacyPublicURL, legacyPass, "")
	clientID, _, _ := legacyRegister(t, server, tokenAuthNone, legacyRedirect)
	serverTxn := legacyAuthorize(t, server, clientID, challenge, "")
	server.store.mint = func(int) (string, error) { return "", errors.New("entropy source exhausted") }
	if rec := legacyConsent(t, server, serverTxn, legacyPass); rec.Code != http.StatusInternalServerError {
		t.Fatalf("consent with an entropy failure = %d %s, want 500", rec.Code, rec.Body.String())
	}
}

func asString(value any) string {
	text, _ := value.(string)
	return text
}
