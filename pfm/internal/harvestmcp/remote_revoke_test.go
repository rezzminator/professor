package harvestmcp

import (
	"net/http"
	"net/url"
	"testing"
)

// A /revoke request naming no client is refused with invalid_client and the
// token it names stays live (RFC 7009 §2.1: the server authenticates the client).
func TestRemoteRevokeWithoutClientIdentityRefused(t *testing.T) {
	server := legacyNewRemote(t, legacyPublicURL, legacyPass, "")
	clientID, _, _ := legacyRegister(t, server, "none", legacyRedirect)
	code, verifier := legacyAuthorizationCode(t, server, clientID)
	tok, rec := legacyToken(
		t,
		server,
		url.Values{
			"grant_type":    {"authorization_code"},
			"code":          {code},
			"redirect_uri":  {legacyRedirect},
			"client_id":     {clientID},
			"code_verifier": {verifier},
		},
		nil,
	)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	revoked := legacyDo(
		t,
		server,
		http.MethodPost,
		"/revoke",
		url.Values{"token": {tok.AccessToken}}.Encode(),
		"application/x-www-form-urlencoded",
		nil,
	)
	if revoked.Code != http.StatusBadRequest || legacyJSON(t, revoked)["error"] != "invalid_client" {
		t.Fatalf("anonymous revoke = %d %s", revoked.Code, revoked.Body.String())
	}
	mcp := legacyDo(
		t,
		server,
		http.MethodPost,
		"/mcp",
		legacyMCPInit,
		"application/json",
		http.Header{"Accept": {"application/json, text/event-stream"}, "Authorization": {"Bearer " + tok.AccessToken}},
	)
	if mcp.Code != http.StatusOK {
		t.Fatalf("token after refused anonymous revoke = %d %s", mcp.Code, mcp.Body.String())
	}
}
