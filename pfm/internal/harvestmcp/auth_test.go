package harvestmcp

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
