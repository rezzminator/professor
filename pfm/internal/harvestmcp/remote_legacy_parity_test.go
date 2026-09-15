package harvestmcp

// This file is the executable crosswalk for the RETIRED standalone Python
// harvester's remote-auth suite (harvester/ was deleted after full migration to
// pfm; this parity suite keeps its guarantees alive on the Go side).
// Keep one test per legacy case (unless the crosswalk names an exact existing
// pin), because these are contract tests for the remote HTTP/OAuth boundary.

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hostops/pfm/internal/harvest"
)

const (
	legacyPublicURL = "https://harvester.example.test"
	legacyRedirect  = "https://client.example/callback"
	legacyPass      = "correct-horse-battery-staple"
	legacyStatic    = "static-token-for-tests"
)

var legacyMCPInit = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`

func legacyNewRemote(t *testing.T, publicURL, passphrase, staticToken string) *RemoteServer {
	t.Helper()
	base := t.TempDir()
	server, err := NewRemote(RemoteOptions{
		Runtime:     Runtime{Home: base, CacheDir: filepath.Join(base, "cache")},
		PublicURL:   publicURL,
		Passphrase:  passphrase,
		StaticToken: staticToken,
		StatePath:   filepath.Join(base, "state", "auth.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func legacyRequest(
	t *testing.T,
	server *RemoteServer,
	method, publicURL, path, body, contentType string,
	headers http.Header,
) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, strings.TrimRight(publicURL, "/")+path, strings.NewReader(body))
	u, err := url.Parse(publicURL)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = u.Host
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	return rec
}

func legacyDo(
	t *testing.T,
	server *RemoteServer,
	method, path, body, contentType string,
	headers http.Header,
) *httptest.ResponseRecorder {
	t.Helper()
	return legacyRequest(t, server, method, legacyPublicURL, path, body, contentType, headers)
}

func legacyJSON(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &value); err != nil {
		t.Fatalf("response is not JSON (%d): %v; body=%q", rec.Code, err, rec.Body.String())
	}
	return value
}

func legacyPKCE() (string, string) {
	verifier := strings.Repeat("v", 64)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

func legacyRegister(
	t *testing.T,
	server *RemoteServer,
	authMethod, redirectURI string,
) (string, string, map[string]any) {
	t.Helper()
	payload := map[string]any{
		"client_name":                "test",
		"redirect_uris":              []string{redirectURI},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": authMethod,
		"scope":                      HarvesterScope,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	rec := legacyDo(t, server, http.MethodPost, "/register", string(body), "application/json", nil)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("register = %d %s", rec.Code, rec.Body.String())
	}
	value := legacyJSON(t, rec)
	id, _ := value["client_id"].(string)
	secret, _ := value["client_secret"].(string)
	if id == "" {
		t.Fatalf("registration omitted client_id: %s", rec.Body.String())
	}
	return id, secret, value
}

func legacyAuthorize(t *testing.T, server *RemoteServer, clientID, challenge, resource string) string {
	t.Helper()
	params := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {legacyRedirect},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {"st4te"},
		"scope":                 {HarvesterScope},
	}
	if resource != "" {
		params.Set("resource", resource)
	}
	rec := legacyDo(t, server, http.MethodGet, "/authorize?"+params.Encode(), "", "", nil)
	if rec.Code != http.StatusFound {
		t.Fatalf("authorize = %d %s", rec.Code, rec.Body.String())
	}
	location, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	txn := location.Query().Get("txn")
	if txn == "" {
		t.Fatalf("authorize location omitted txn: %s", rec.Header().Get("Location"))
	}
	return txn
}

func legacyConsent(t *testing.T, server *RemoteServer, txn, passphrase string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"txn": {txn}, "passphrase": {passphrase}}
	return legacyDo(t, server, http.MethodPost, "/consent", form.Encode(), "application/x-www-form-urlencoded", nil)
}

func legacyCodeFromConsent(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	if rec.Code != http.StatusFound {
		t.Fatalf("consent = %d %s", rec.Code, rec.Body.String())
	}
	location, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	code := location.Query().Get("code")
	if code == "" {
		t.Fatalf("consent callback omitted code: %s", rec.Header().Get("Location"))
	}
	return code
}

func legacyAuthorizationCode(t *testing.T, server *RemoteServer, clientID string) (string, string) {
	t.Helper()
	verifier, challenge := legacyPKCE()
	txn := legacyAuthorize(t, server, clientID, challenge, "")
	code := legacyCodeFromConsent(t, legacyConsent(t, server, txn, legacyPass))
	return code, verifier
}

func legacyToken(
	t *testing.T,
	server *RemoteServer,
	body url.Values,
	headers http.Header,
) (tokenResponse, *httptest.ResponseRecorder) {
	t.Helper()
	rec := legacyDo(t, server, http.MethodPost, "/token", body.Encode(), "application/x-www-form-urlencoded", headers)
	var response tokenResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
	}
	return response, rec
}

func legacyBasic(id, secret string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(id+":"+secret))
}

// ── transport wiring ─────────────────────────────────────────────────────────

// The external gateway has no unauthenticated mode: the retired standalone
// server's open gateway and its open "internal" twin are gone — the daemon's
// loopback port is the internal door now.
func TestRemoteRefusesToExistWithoutCredentials(t *testing.T) {
	base := t.TempDir()
	_, err := NewRemote(
		RemoteOptions{Runtime: Runtime{Home: base, CacheDir: filepath.Join(base, "cache")}, PublicURL: legacyPublicURL},
	)
	if err == nil || !strings.Contains(err.Error(), "never unauthenticated") {
		t.Fatalf("credential-free NewRemote error = %v; want the refusal", err)
	}
}

func TestRemoteLegacy_ExactMCPPath(t *testing.T) {
	server := legacyNewRemote(t, legacyPublicURL, legacyPass, legacyStatic)
	rec := legacyDo(
		t,
		server,
		http.MethodPost,
		"/mcp",
		legacyMCPInit,
		"application/json",
		http.Header{"Accept": {"application/json, text/event-stream"}},
	)
	if rec.Code == http.StatusTemporaryRedirect || rec.Code == http.StatusPermanentRedirect ||
		rec.Code != http.StatusUnauthorized {
		t.Fatalf("POST /mcp = %d, want exact path 401 without redirect", rec.Code)
	}
}

func TestRemoteLegacy_PublicHostAllowedByDNSRebindingProtection(t *testing.T) {
	server := legacyNewRemote(t, legacyPublicURL, "", legacyStatic)
	rec := legacyDo(t, server, http.MethodPost, "/mcp", legacyMCPInit, "application/json", http.Header{
		"Accept": {"application/json, text/event-stream"}, "Authorization": {"Bearer " + legacyStatic},
	})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "harvester") {
		t.Fatalf("public host initialize = %d %s", rec.Code, rec.Body.String())
	}
}

func TestRemoteLegacy_ForeignHostRejected(t *testing.T) {
	server := legacyNewRemote(t, legacyPublicURL, "", legacyStatic)
	req := httptest.NewRequest(http.MethodPost, legacyPublicURL+"/mcp", strings.NewReader(legacyMCPInit))
	req.Host = "evil.example.com"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+legacyStatic)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code < http.StatusBadRequest {
		t.Fatalf("foreign Host accepted with %d", rec.Code)
	}
}

func TestRemoteLegacy_PublicURLRequiresHostname(t *testing.T) {
	_, err := NewRemote(
		RemoteOptions{Runtime: Runtime{Home: t.TempDir()}, PublicURL: "not-a-url", StaticToken: legacyStatic},
	)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "hostname") {
		t.Fatalf("invalid public URL error = %v", err)
	}
}

// ── 401 handshake and metadata ──────────────────────────────────────────────

func TestRemoteLegacy_401CarriesResourceMetadataPointer(t *testing.T) {
	server := legacyNewRemote(t, legacyPublicURL, legacyPass, "")
	rec := legacyDo(
		t,
		server,
		http.MethodPost,
		"/mcp",
		legacyMCPInit,
		"application/json",
		http.Header{"Accept": {"application/json, text/event-stream"}},
	)
	www := rec.Header().Get("WWW-Authenticate")
	if rec.Code != http.StatusUnauthorized || !strings.Contains(www, `resource_metadata="`) ||
		!strings.Contains(www, legacyPublicURL+"/.well-known/oauth-protected-resource/mcp") ||
		!strings.Contains(www, `scope="harvest"`) {
		t.Fatalf("401 challenge = %d %q", rec.Code, www)
	}
}

func TestRemoteLegacy_ProtectedResourceMetadataMatchesEnteredURL(t *testing.T) {
	server := legacyNewRemote(t, legacyPublicURL, legacyPass, "")
	value := legacyJSON(
		t,
		legacyDo(t, server, http.MethodGet, "/.well-known/oauth-protected-resource/mcp", "", "", nil),
	)
	if value["resource"] != legacyPublicURL+"/mcp" {
		t.Fatalf("resource = %v", value["resource"])
	}
	servers, ok := value["authorization_servers"].([]any)
	if !ok || len(servers) != 1 || servers[0] != legacyPublicURL+"/" {
		t.Fatalf("authorization_servers = %#v", value["authorization_servers"])
	}
}

func TestRemoteLegacy_BareProtectedResourceMetadataServed(t *testing.T) {
	server := legacyNewRemote(t, legacyPublicURL, legacyPass, "")
	value := legacyJSON(t, legacyDo(t, server, http.MethodGet, "/.well-known/oauth-protected-resource", "", "", nil))
	if value["resource"] != legacyPublicURL+"/mcp" {
		t.Fatalf("bare resource = %v", value["resource"])
	}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestRemoteLegacy_AuthorizationMetadataAdvertisesS256DCRAndAuthMethods(t *testing.T) {
	server := legacyNewRemote(t, legacyPublicURL, legacyPass, "")
	rec := legacyDo(t, server, http.MethodGet, "/.well-known/oauth-authorization-server", "", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("authorization metadata = %d %s", rec.Code, rec.Body.String())
	}
	value := legacyJSON(t, rec)
	if got := fmt.Sprint(
		value["code_challenge_methods_supported"],
	); got != "[S256]" || value["registration_endpoint"] != legacyPublicURL+"/register" ||
		fmt.Sprint(value["scopes_supported"]) != "[harvest]" {
		t.Fatalf("authorization metadata fields = %#v", value)
	}
	if strings.Contains(rec.Body.String(), "offline_access") {
		t.Fatal("authorization metadata advertised offline_access")
	}
	if fmt.Sprint(value["token_endpoint_auth_methods_supported"]) != "[none client_secret_post client_secret_basic]" ||
		fmt.Sprint(
			value["revocation_endpoint_auth_methods_supported"],
		) != "[none client_secret_post client_secret_basic]" {
		t.Fatalf("auth method metadata = %#v", value)
	}

	clientID, _, _ := legacyRegister(t, server, "none", legacyRedirect)
	_, challenge := legacyPKCE()
	txn := legacyAuthorize(t, server, clientID, challenge, "")
	code := legacyCodeFromConsent(t, legacyConsent(t, server, txn, legacyPass))
	_, token := legacyToken(
		t,
		server,
		url.Values{
			"grant_type":    {"authorization_code"},
			"code":          {code},
			"redirect_uri":  {legacyRedirect},
			"client_id":     {clientID},
			"code_verifier": {strings.Repeat("v", 64)},
		},
		nil,
	)
	if token.Code != http.StatusOK {
		t.Fatalf("public-client token exchange = %d %s", token.Code, token.Body.String())
	}
}

func TestRemoteLegacy_SuffixedAuthorizationMetadataMatchesRoot(t *testing.T) {
	server := legacyNewRemote(t, legacyPublicURL, legacyPass, "")
	root := legacyDo(t, server, http.MethodGet, "/.well-known/oauth-authorization-server", "", "", nil)
	suffixed := legacyDo(t, server, http.MethodGet, "/.well-known/oauth-authorization-server/mcp", "", "", nil)
	if !bytesEqual(root.Body.Bytes(), suffixed.Body.Bytes()) {
		t.Fatalf("authorization metadata aliases drifted")
	}
}

func TestRemoteLegacy_OpenIDConfigurationMatchesAuthorizationMetadata(t *testing.T) {
	server := legacyNewRemote(t, legacyPublicURL, legacyPass, "")
	authz := legacyDo(t, server, http.MethodGet, "/.well-known/oauth-authorization-server", "", "", nil)
	oidc := legacyDo(t, server, http.MethodGet, "/.well-known/openid-configuration", "", "", nil)
	if oidc.Code != http.StatusOK || !bytesEqual(authz.Body.Bytes(), oidc.Body.Bytes()) {
		t.Fatalf("OIDC metadata = %d %q", oidc.Code, oidc.Body.String())
	}
}

func TestRemoteLegacy_SuffixedOpenIDConfigurationMatchesAuthorizationMetadata(t *testing.T) {
	server := legacyNewRemote(t, legacyPublicURL, legacyPass, "")
	authz := legacyDo(t, server, http.MethodGet, "/.well-known/oauth-authorization-server", "", "", nil)
	oidc := legacyDo(t, server, http.MethodGet, "/.well-known/openid-configuration/mcp", "", "", nil)
	if oidc.Code != http.StatusOK || !bytesEqual(authz.Body.Bytes(), oidc.Body.Bytes()) {
		t.Fatalf("suffixed OIDC metadata = %d %q", oidc.Code, oidc.Body.String())
	}
}

// ── bearer modes and persistence ────────────────────────────────────────────

func TestRemoteLegacy_StaticTokenAccepted(t *testing.T) {
	server := legacyNewRemote(t, legacyPublicURL, "", legacyStatic)
	rec := legacyDo(
		t,
		server,
		http.MethodPost,
		"/mcp",
		legacyMCPInit,
		"application/json",
		http.Header{"Accept": {"application/json, text/event-stream"}, "Authorization": {"Bearer " + legacyStatic}},
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("static token = %d %s", rec.Code, rec.Body.String())
	}
}

func TestRemoteLegacy_WrongTokensRejected(t *testing.T) {
	server := legacyNewRemote(t, legacyPublicURL, "", legacyStatic)
	for _, bad := range []string{"wrong-token", legacyStatic + "x", legacyStatic[:len(legacyStatic)-1], ""} {
		rec := legacyDo(
			t,
			server,
			http.MethodPost,
			"/mcp",
			legacyMCPInit,
			"application/json",
			http.Header{"Accept": {"application/json, text/event-stream"}, "Authorization": {"Bearer " + bad}},
		)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("wrong token %q = %d", bad, rec.Code)
		}
	}
}

func TestRemoteLegacy_StaticTokenOnlyRequiresBearerAndMountsNoOAuth(t *testing.T) {
	server := legacyNewRemote(t, legacyPublicURL, "", legacyStatic)
	for _, path := range []string{"/.well-known/oauth-authorization-server", "/consent", "/token", "/register", "/revoke"} {
		if rec := legacyDo(t, server, http.MethodGet, path, "", "", nil); rec.Code != http.StatusNotFound {
			t.Errorf("static-only GET %s = %d", path, rec.Code)
		}
	}
	without := legacyDo(
		t,
		server,
		http.MethodPost,
		"/mcp",
		legacyMCPInit,
		"application/json",
		http.Header{"Accept": {"application/json, text/event-stream"}},
	)
	if without.Code != http.StatusUnauthorized {
		t.Fatalf("static-only missing bearer = %d", without.Code)
	}
	with := legacyDo(
		t,
		server,
		http.MethodPost,
		"/mcp",
		legacyMCPInit,
		"application/json",
		http.Header{"Accept": {"application/json, text/event-stream"}, "Authorization": {"Bearer " + legacyStatic}},
	)
	if with.Code != http.StatusOK {
		t.Fatalf("static-only bearer = %d %s", with.Code, with.Body.String())
	}
}

func TestRemoteLegacy_CorruptStateDoesNotCrashStartup(t *testing.T) {
	state := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(state, []byte(`{"clients":[],"refresh":[{"no_hash_key":true}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	server, err := NewRemote(
		RemoteOptions{
			Runtime:    Runtime{Home: t.TempDir(), CacheDir: filepath.Join(t.TempDir(), "cache")},
			PublicURL:  legacyPublicURL,
			Passphrase: legacyPass,
			StatePath:  state,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	server.store.mu.Lock()
	refreshCount := len(server.store.refresh)
	server.store.mu.Unlock()
	if refreshCount != 0 {
		t.Fatalf("corrupt refresh state loaded %d entries", refreshCount)
	}
}

func TestRemoteLegacy_StaticTokenAbsentWhenNotConfigured(t *testing.T) {
	server := legacyNewRemote(t, legacyPublicURL, legacyPass, "")
	rec := legacyDo(
		t,
		server,
		http.MethodPost,
		"/mcp",
		legacyMCPInit,
		"application/json",
		http.Header{"Accept": {"application/json, text/event-stream"}, "Authorization": {"Bearer "}},
	)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("empty bearer accepted = %d", rec.Code)
	}
}

// ── registration and authorization code flow ────────────────────────────────

func TestRemoteLegacy_RegistrationRequiresJSONAndDisablesCaching(t *testing.T) {
	server := legacyNewRemote(t, legacyPublicURL, legacyPass, "")
	payload := `{"redirect_uris":["` + legacyRedirect + `"],"grant_types":["authorization_code","refresh_token"],"response_types":["code"],"token_endpoint_auth_method":"none","scope":"harvest"}`
	rec := legacyDo(t, server, http.MethodPost, "/register", payload, "application/json", nil)
	if rec.Code != http.StatusCreated || rec.Header().Get("Content-Type") != "application/json" ||
		rec.Header().Get("Cache-Control") != "no-store" ||
		rec.Header().Get("Pragma") != "no-cache" {
		t.Fatalf("valid registration = %d headers=%v body=%s", rec.Code, rec.Header(), rec.Body.String())
	}
	rec = legacyDo(
		t,
		server,
		http.MethodPost,
		"/register",
		`{"redirect_uris":["`+legacyRedirect+`"]}`,
		"application/x-www-form-urlencoded",
		nil,
	)
	if rec.Code != http.StatusBadRequest || legacyJSON(t, rec)["error"] != "invalid_client_metadata" {
		t.Fatalf("wrong registration content type = %d %s", rec.Code, rec.Body.String())
	}
	rec = legacyDo(t, server, http.MethodPost, "/register", "{", "application/json", nil)
	if rec.Code != http.StatusBadRequest || legacyJSON(t, rec)["error"] != "invalid_client_metadata" {
		t.Fatalf("malformed registration = %d %s", rec.Code, rec.Body.String())
	}
}

func TestRemoteLegacy_RegistrationDefaultAndCompleteSecretMetadata(t *testing.T) {
	server := legacyNewRemote(t, legacyPublicURL, legacyPass, "")
	payload := `{"redirect_uris":["` + legacyRedirect + `"],"grant_types":["authorization_code","refresh_token"],"response_types":["code"],"scope":"harvest"}`
	rec := legacyDo(t, server, http.MethodPost, "/register", payload, "application/json", nil)
	value := legacyJSON(t, rec)
	if rec.Code != http.StatusCreated || value["token_endpoint_auth_method"] != "client_secret_basic" ||
		value["client_secret"] == "" ||
		value["client_secret_expires_at"] != float64(0) {
		t.Fatalf("default registration = %d %#v", rec.Code, value)
	}
}

func TestRemoteLegacy_RegistrationRejectsUnsupportedMetadata(t *testing.T) {
	server := legacyNewRemote(t, legacyPublicURL, legacyPass, "")
	cases := []struct{ method, redirect, want string }{
		{"private_key_jwt", legacyRedirect, "invalid_client_metadata"},
		{"none", "http://client.example/callback", "invalid_redirect_uri"},
		{"none", "https://client.example/callback#fragment", "invalid_redirect_uri"},
	}
	for _, tc := range cases {
		payload := fmt.Sprintf(
			`{"redirect_uris":[%q],"grant_types":["authorization_code","refresh_token"],"response_types":["code"],"token_endpoint_auth_method":%q,"scope":"harvest"}`,
			tc.redirect,
			tc.method,
		)
		rec := legacyDo(t, server, http.MethodPost, "/register", payload, "application/json", nil)
		if rec.Code != http.StatusBadRequest || legacyJSON(t, rec)["error"] != tc.want {
			t.Errorf("registration method=%s redirect=%s = %d %s", tc.method, tc.redirect, rec.Code, rec.Body.String())
		}
	}
}

func TestRemoteLegacy_HTTPLoopbackRedirectAllowed(t *testing.T) {
	server := legacyNewRemote(t, legacyPublicURL, legacyPass, "")
	id, _, _ := legacyRegister(t, server, "none", "http://127.0.0.1:43123/callback")
	if id == "" {
		t.Fatal("loopback client id empty")
	}
}

func TestRemoteLegacy_FullAuthorizationCodeFlow(t *testing.T) {
	server := legacyNewRemote(t, legacyPublicURL, legacyPass, "")
	clientID, _, _ := legacyRegister(t, server, "none", legacyRedirect)
	code, verifier := legacyAuthorizationCode(t, server, clientID)
	first, rec := legacyToken(
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
	if rec.Code != http.StatusOK || first.AccessToken == "" {
		t.Fatalf("authorization-code token = %d %s", rec.Code, rec.Body.String())
	}
	mcp := legacyDo(
		t,
		server,
		http.MethodPost,
		"/mcp",
		legacyMCPInit,
		"application/json",
		http.Header{
			"Accept":        {"application/json, text/event-stream"},
			"Authorization": {"Bearer " + first.AccessToken},
		},
	)
	if mcp.Code != http.StatusOK {
		t.Fatalf("issued access token MCP = %d %s", mcp.Code, mcp.Body.String())
	}
}

func TestRemoteLegacy_ResourceIndicatorValidatedAuthorizeAndToken(t *testing.T) {
	server := legacyNewRemote(t, legacyPublicURL, legacyPass, "")
	clientID, _, _ := legacyRegister(t, server, "none", legacyRedirect)
	verifier, challenge := legacyPKCE()
	resource := "HTTPS://HARVESTER.EXAMPLE.TEST/mcp"
	txn := legacyAuthorize(t, server, clientID, challenge, resource)
	code := legacyCodeFromConsent(t, legacyConsent(t, server, txn, legacyPass))
	response, rec := legacyToken(
		t,
		server,
		url.Values{
			"grant_type":    {"authorization_code"},
			"code":          {code},
			"redirect_uri":  {legacyRedirect},
			"client_id":     {clientID},
			"code_verifier": {verifier},
			"resource":      {resource},
		},
		nil,
	)
	if rec.Code != http.StatusOK || response.AccessToken == "" {
		t.Fatalf("uppercase resource token = %d %s", rec.Code, rec.Body.String())
	}
	mcp := legacyDo(
		t,
		server,
		http.MethodPost,
		"/mcp",
		legacyMCPInit,
		"application/json",
		http.Header{
			"Accept":        {"application/json, text/event-stream"},
			"Authorization": {"Bearer " + response.AccessToken},
		},
	)
	if mcp.Code != http.StatusOK {
		t.Fatalf("resource-bound token MCP = %d", mcp.Code)
	}
}

func TestRemoteLegacy_AuthorizeRejectsForeignResource(t *testing.T) {
	server := legacyNewRemote(t, legacyPublicURL, legacyPass, "")
	clientID, _, _ := legacyRegister(t, server, "none", legacyRedirect)
	_, challenge := legacyPKCE()
	params := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {legacyRedirect},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {"st4te"},
		"scope":                 {HarvesterScope},
		"resource":              {"https://other.example/mcp"},
	}
	rec := legacyDo(t, server, http.MethodGet, "/authorize?"+params.Encode(), "", "", nil)
	if rec.Code != http.StatusFound {
		t.Fatalf("foreign-resource authorize = %d %s", rec.Code, rec.Body.String())
	}
	query, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if query.Query().Get("error") != "invalid_target" || query.Query().Get("state") != "st4te" {
		t.Fatalf("foreign-resource redirect = %s", rec.Header().Get("Location"))
	}
}

func TestRemoteLegacy_TokenForeignResourceDoesNotBurnCode(t *testing.T) {
	server := legacyNewRemote(t, legacyPublicURL, legacyPass, "")
	clientID, _, _ := legacyRegister(t, server, "none", legacyRedirect)
	code, verifier := legacyAuthorizationCode(t, server, clientID)
	body := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {legacyRedirect},
		"client_id":     {clientID},
		"code_verifier": {verifier},
	}
	_, wrong := legacyToken(
		t,
		server,
		url.Values{
			"grant_type":    {"authorization_code"},
			"code":          {code},
			"redirect_uri":  {legacyRedirect},
			"client_id":     {clientID},
			"code_verifier": {verifier},
			"resource":      {"https://other.example/mcp"},
		},
		nil,
	)
	if wrong.Code != http.StatusBadRequest || legacyJSON(t, wrong)["error"] != "invalid_target" {
		t.Fatalf("wrong-resource token = %d %s", wrong.Code, wrong.Body.String())
	}
	_, right := legacyToken(t, server, body, nil)
	if right.Code != http.StatusOK {
		t.Fatalf("code burned by wrong-resource token = %d %s", right.Code, right.Body.String())
	}
}

func TestRemoteLegacy_ConfidentialClientBasicTokenAuth(t *testing.T) {
	server := legacyNewRemote(t, legacyPublicURL, legacyPass, "")
	clientID, secret, _ := legacyRegister(t, server, "client_secret_basic", legacyRedirect)
	code, verifier := legacyAuthorizationCode(t, server, clientID)
	response, rec := legacyToken(
		t,
		server,
		url.Values{
			"grant_type":    {"authorization_code"},
			"code":          {code},
			"redirect_uri":  {legacyRedirect},
			"code_verifier": {verifier},
		},
		http.Header{"Authorization": {legacyBasic(clientID, secret)}},
	)
	if rec.Code != http.StatusOK || response.AccessToken == "" {
		t.Fatalf("Basic token auth = %d %s", rec.Code, rec.Body.String())
	}
}

func TestRemoteLegacy_TokenBodyCredentialsAccepted(t *testing.T) {
	server := legacyNewRemote(t, legacyPublicURL, legacyPass, "")
	clientID, secret, _ := legacyRegister(t, server, "client_secret_post", legacyRedirect)
	code, verifier := legacyAuthorizationCode(t, server, clientID)
	response, rec := legacyToken(
		t,
		server,
		url.Values{
			"grant_type":    {"authorization_code"},
			"code":          {code},
			"redirect_uri":  {legacyRedirect},
			"code_verifier": {verifier},
			"client_id":     {clientID},
			"client_secret": {secret},
		},
		nil,
	)
	if rec.Code != http.StatusOK || response.AccessToken == "" {
		t.Fatalf("body credentials = %d %s", rec.Code, rec.Body.String())
	}
}

func TestRemoteLegacy_UnsupportedGrantUsesRFC6749Error(t *testing.T) {
	server := legacyNewRemote(t, legacyPublicURL, legacyPass, "")
	clientID, _, _ := legacyRegister(t, server, "none", legacyRedirect)
	rec := legacyDo(
		t,
		server,
		http.MethodPost,
		"/token",
		url.Values{"grant_type": {"client_credentials"}, "client_id": {clientID}}.Encode(),
		"application/x-www-form-urlencoded",
		nil,
	)
	if rec.Code != http.StatusBadRequest || legacyJSON(t, rec)["error"] != "unsupported_grant_type" {
		t.Fatalf("unsupported grant = %d %s", rec.Code, rec.Body.String())
	}
}

func TestRemoteLegacy_TokenAndRevocationRequireFormEncoding(t *testing.T) {
	server := legacyNewRemote(t, legacyPublicURL, legacyPass, "")
	clientID, _, _ := legacyRegister(t, server, "none", legacyRedirect)
	token := legacyDo(
		t,
		server,
		http.MethodPost,
		"/token",
		`{"grant_type":"refresh_token","refresh_token":"unused","client_id":"`+clientID+`"}`,
		"application/json",
		nil,
	)
	if token.Code != http.StatusBadRequest || legacyJSON(t, token)["error"] != "invalid_request" {
		t.Fatalf("JSON token = %d %s", token.Code, token.Body.String())
	}
	revoke := legacyDo(
		t,
		server,
		http.MethodPost,
		"/revoke",
		`{"token":"unused","client_id":"`+clientID+`"}`,
		"application/json",
		nil,
	)
	if revoke.Code != http.StatusBadRequest || legacyJSON(t, revoke)["error"] != "invalid_request" {
		t.Fatalf("JSON revoke = %d %s", revoke.Code, revoke.Body.String())
	}
}

func TestRemoteLegacy_PKCERejectsWrongVerifier(t *testing.T) {
	server := legacyNewRemote(t, legacyPublicURL, legacyPass, "")
	clientID, _, _ := legacyRegister(t, server, "none", legacyRedirect)
	_, challenge := legacyPKCE()
	txn := legacyAuthorize(t, server, clientID, challenge, "")
	code := legacyCodeFromConsent(t, legacyConsent(t, server, txn, legacyPass))
	_, rec := legacyToken(
		t,
		server,
		url.Values{
			"grant_type":    {"authorization_code"},
			"code":          {code},
			"redirect_uri":  {legacyRedirect},
			"client_id":     {clientID},
			"code_verifier": {strings.Repeat("w", 64)},
		},
		nil,
	)
	if rec.Code != http.StatusBadRequest || legacyJSON(t, rec)["error"] != "invalid_grant" {
		t.Fatalf("wrong verifier = %d %s", rec.Code, rec.Body.String())
	}
}

func TestRemoteLegacy_PKCERejectsMalformedChallenge(t *testing.T) {
	server := legacyNewRemote(t, legacyPublicURL, legacyPass, "")
	clientID, _, _ := legacyRegister(t, server, "none", legacyRedirect)
	params := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {legacyRedirect},
		"code_challenge":        {"too-short"},
		"code_challenge_method": {"S256"},
		"state":                 {"st4te"},
		"scope":                 {HarvesterScope},
	}
	rec := legacyDo(t, server, http.MethodGet, "/authorize?"+params.Encode(), "", "", nil)
	location, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if location.Query().Get("error") != "invalid_request" {
		t.Fatalf("malformed challenge redirect = %s", rec.Header().Get("Location"))
	}
}

func TestRemoteLegacy_WrongPassphraseMintsNoCode(t *testing.T) {
	server := legacyNewRemote(t, legacyPublicURL, legacyPass, "")
	clientID, _, _ := legacyRegister(t, server, "none", legacyRedirect)
	_, challenge := legacyPKCE()
	txn := legacyAuthorize(t, server, clientID, challenge, "")
	rec := legacyConsent(t, server, txn, "not-the-passphrase")
	if rec.Code != http.StatusUnauthorized || rec.Header().Get("Location") != "" {
		t.Fatalf("wrong passphrase = %d location=%q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestRemoteLegacy_ConsentTransactionBurnsAfterRepeatedFailures(t *testing.T) {
	server := legacyNewRemote(t, legacyPublicURL, legacyPass, "")
	clientID, _, _ := legacyRegister(t, server, "none", legacyRedirect)
	_, challenge := legacyPKCE()
	txn := legacyAuthorize(t, server, clientID, challenge, "")
	for i := 0; i < 6; i++ {
		_ = legacyConsent(t, server, txn, "wrong")
	}
	if rec := legacyConsent(t, server, txn, legacyPass); rec.Code != http.StatusBadRequest {
		t.Fatalf("burned consent accepted correct passphrase: %d", rec.Code)
	}
}

func TestRemoteLegacy_AuthorizationCodeSingleUse(t *testing.T) {
	server := legacyNewRemote(t, legacyPublicURL, legacyPass, "")
	clientID, _, _ := legacyRegister(t, server, "none", legacyRedirect)
	code, verifier := legacyAuthorizationCode(t, server, clientID)
	body := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {legacyRedirect},
		"client_id":     {clientID},
		"code_verifier": {verifier},
	}
	_, first := legacyToken(t, server, body, nil)
	if first.Code != http.StatusOK {
		t.Fatalf("first code exchange = %d %s", first.Code, first.Body.String())
	}
	_, replay := legacyToken(t, server, body, nil)
	if replay.Code != http.StatusBadRequest || legacyJSON(t, replay)["error"] != "invalid_grant" {
		t.Fatalf("code replay = %d %s", replay.Code, replay.Body.String())
	}
}

func TestRemoteLegacy_RefreshRotatesAndOldDies(t *testing.T) {
	server := legacyNewRemote(t, legacyPublicURL, legacyPass, "")
	clientID, _, _ := legacyRegister(t, server, "none", legacyRedirect)
	code, verifier := legacyAuthorizationCode(t, server, clientID)
	first, rec := legacyToken(
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
	if rec.Code != http.StatusOK || first.RefreshToken == "" {
		t.Fatalf("initial token = %d %s", rec.Code, rec.Body.String())
	}
	rotated, rec := legacyToken(
		t,
		server,
		url.Values{"grant_type": {"refresh_token"}, "refresh_token": {first.RefreshToken}, "client_id": {clientID}},
		nil,
	)
	if rec.Code != http.StatusOK || rotated.RefreshToken == first.RefreshToken || rotated.AccessToken == "" {
		t.Fatalf("rotated token = %d %s", rec.Code, rec.Body.String())
	}
	mcp := legacyDo(
		t,
		server,
		http.MethodPost,
		"/mcp",
		legacyMCPInit,
		"application/json",
		http.Header{
			"Accept":        {"application/json, text/event-stream"},
			"Authorization": {"Bearer " + rotated.AccessToken},
		},
	)
	if mcp.Code != http.StatusOK {
		t.Fatalf("rotated access token MCP = %d", mcp.Code)
	}
	_, replay := legacyToken(
		t,
		server,
		url.Values{"grant_type": {"refresh_token"}, "refresh_token": {first.RefreshToken}, "client_id": {clientID}},
		nil,
	)
	if replay.Code != http.StatusBadRequest || legacyJSON(t, replay)["error"] != "invalid_grant" {
		t.Fatalf("refresh replay = %d %s", replay.Code, replay.Body.String())
	}
}

func TestRemoteLegacy_RefreshRejectsForeignResource(t *testing.T) {
	server := legacyNewRemote(t, legacyPublicURL, legacyPass, "")
	clientID, _, _ := legacyRegister(t, server, "none", legacyRedirect)
	code, verifier := legacyAuthorizationCode(t, server, clientID)
	first, rec := legacyToken(
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
	_, foreign := legacyToken(
		t,
		server,
		url.Values{
			"grant_type":    {"refresh_token"},
			"refresh_token": {first.RefreshToken},
			"client_id":     {clientID},
			"resource":      {"https://other.example/mcp"},
		},
		nil,
	)
	if foreign.Code != http.StatusBadRequest || legacyJSON(t, foreign)["error"] != "invalid_target" {
		t.Fatalf("foreign refresh resource = %d %s", foreign.Code, foreign.Body.String())
	}
}

func TestRemoteLegacy_PublicClientRevocation(t *testing.T) {
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
		url.Values{"token": {tok.AccessToken}, "token_type_hint": {"access_token"}, "client_id": {clientID}}.Encode(),
		"application/x-www-form-urlencoded",
		nil,
	)
	if revoked.Code != http.StatusOK || revoked.Header().Get("Cache-Control") != "no-store" ||
		revoked.Header().Get("Pragma") != "no-cache" {
		t.Fatalf("revoke = %d headers=%v", revoked.Code, revoked.Header())
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
	if mcp.Code != http.StatusUnauthorized {
		t.Fatalf("revoked access token = %d", mcp.Code)
	}
}

func TestRemoteLegacy_UnknownRevocationIdempotentlySucceeds(t *testing.T) {
	server := legacyNewRemote(t, legacyPublicURL, legacyPass, "")
	clientID, _, _ := legacyRegister(t, server, "none", legacyRedirect)
	rec := legacyDo(
		t,
		server,
		http.MethodPost,
		"/revoke",
		url.Values{"token": {"does-not-exist"}, "client_id": {clientID}}.Encode(),
		"application/x-www-form-urlencoded",
		nil,
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("unknown revoke = %d %s", rec.Code, rec.Body.String())
	}
}

func TestRemoteLegacy_ResourceServerRejectsForeignAudience(t *testing.T) {
	server := legacyNewRemote(t, legacyPublicURL, legacyPass, "")
	raw := "wrong-audience-token"
	server.store.mu.Lock()
	server.store.access[digest(raw)] = accessToken{
		Raw:      raw,
		ClientID: "test",
		Scope:    []string{HarvesterScope},
		Resource: "https://other.example/mcp",
		Expires:  time.Now().Add(time.Hour),
	}
	server.store.mu.Unlock()
	if server.store.verify(raw) {
		t.Fatal("access token for another audience was accepted")
	}
}

// ── credential storage and local-read confinement ────────────────────────────

func TestRemoteLegacy_StateFileOutsideCacheRoot(t *testing.T) {
	cache := filepath.Join(t.TempDir(), ".cache")
	state := defaultAuthStatePath(cache)
	if strings.HasPrefix(filepath.Clean(state), filepath.Clean(cache)+string(os.PathSeparator)) {
		t.Fatalf("state path %q is inside cache root %q", state, cache)
	}
}

func TestRemoteLegacy_StateFileStoresNoLiveTokens(t *testing.T) {
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
	b, err := os.ReadFile(server.store.statePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), tok.AccessToken) || strings.Contains(string(b), tok.RefreshToken) {
		t.Fatalf("state contains a live token: %s", b)
	}
}

func TestRemoteLegacy_StateFileOwnerOnly(t *testing.T) {
	state := filepath.Join(t.TempDir(), "auth.json")
	store := newAuthStore(legacyPublicURL, legacyPublicURL+"/mcp", legacyPass, "", state)
	store.mu.Lock()
	store.saveLocked()
	store.mu.Unlock()
	mode := mustFileMode(t, state)
	if mode&0o077 != 0 {
		t.Fatalf("state mode = %o", mode)
	}
}

func mustFileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode()
}

func TestRemoteLegacy_ConfinementAllowsCacheAndRefusesOutside(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, ".cache")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(cache, "artifact.md")
	outside := filepath.Join(root, "secrets.md")
	if err := os.WriteFile(inside, []byte("cached"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	if harvest.DenyLocalPath(inside, []string{cache}) != "" || harvest.DenyLocalPath(outside, []string{cache}) == "" ||
		harvest.DenyLocalPath("/etc/hostname", []string{cache}) == "" {
		t.Fatal("cache confinement did not match legacy allow/deny semantics")
	}
}

func TestRemoteLegacy_ConfinementFollowsSymlinksOut(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, ".cache")
	secret := filepath.Join(root, "secret.md")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secret, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(cache, "innocent.md")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	if harvest.DenyLocalPath(link, []string{cache}) == "" {
		t.Fatal("symlink escape was allowed")
	}
}

func TestRemoteLegacy_ConfinementBlocksParentTraversal(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, ".cache")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(root, "secret.md")
	if err := os.WriteFile(secret, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	if harvest.DenyLocalPath(filepath.Join(cache, "..", "secret.md"), []string{cache}) == "" {
		t.Fatal("parent traversal was allowed")
	}
}

func TestRemoteLegacy_DenylistStillAppliesInsideRoots(t *testing.T) {
	cache := filepath.Join(t.TempDir(), ".cache")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(cache, "leaked.pem")
	if err := os.WriteFile(key, []byte("example key material"), 0o600); err != nil {
		t.Fatal(err)
	}
	if harvest.DenyLocalPath(key, []string{cache}) == "" {
		t.Fatal("credential denylist did not apply inside permitted root")
	}
}

func TestRemoteLegacy_UnconfinedByDefault(t *testing.T) {
	doc := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(doc, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if harvest.DenyLocalPath(doc, nil) != "" {
		t.Fatal("nil roots were not unconfined")
	}
}

func TestRemoteLegacy_BuildAppConfinesReadsToCache(t *testing.T) {
	base := t.TempDir()
	cache := filepath.Join(base, "cache")
	server, err := NewRemote(
		RemoteOptions{
			Runtime:     Runtime{Home: base, CacheDir: cache},
			PublicURL:   legacyPublicURL,
			StaticToken: legacyStatic,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	publicCache := filepath.Join(cache, "public")
	if len(server.service.runtime.LocalRoots) != 1 || server.service.runtime.LocalRoots[0] != publicCache {
		t.Fatalf("local roots = %#v", server.service.runtime.LocalRoots)
	}
}

// ── explicit seam pins requested by the remote parity brief ─────────────────

func TestRemoteLegacy_ExpiryIsEnforced(t *testing.T) {
	server := legacyNewRemote(t, legacyPublicURL, legacyPass, "")
	server.store.mu.Lock()
	server.store.access[digest("expired")] = accessToken{
		Raw:      "expired",
		ClientID: "c",
		Scope:    []string{HarvesterScope},
		Resource: server.resource,
		Expires:  time.Now().Add(-time.Second),
	}
	server.store.codes["expired-code"] = authorizationCode{
		Code:     "expired-code",
		ClientID: "c",
		Expires:  time.Now().Add(-time.Second),
	}
	server.store.clients["c"] = oauthClient{
		persistedClient: persistedClient{ClientID: "c", TokenEndpointAuthMethod: "none"},
	}
	server.store.mu.Unlock()
	if server.store.verify("expired") {
		t.Fatal("expired access token accepted")
	}
	server.store.mu.Lock()
	_, present := server.store.codes["expired-code"]
	server.store.mu.Unlock()
	if !present {
		t.Fatal("fixture code disappeared before expiry assertion")
	}
	// Exchange must reject an expired code; this also proves the HTTP error shape.
	_, rec := legacyToken(
		t,
		server,
		url.Values{"grant_type": {"authorization_code"}, "code": {"expired-code"}, "client_id": {"c"}},
		nil,
	)
	if rec.Code != http.StatusBadRequest || legacyJSON(t, rec)["error"] != "invalid_grant" {
		t.Fatalf("expired authorization code = %d %s", rec.Code, rec.Body.String())
	}
}

func TestRemoteLegacy_ConsentPageContainsOperatorForm(t *testing.T) {
	server := legacyNewRemote(t, legacyPublicURL, legacyPass, "")
	clientID, _, _ := legacyRegister(t, server, "none", legacyRedirect)
	_, challenge := legacyPKCE()
	txn := legacyAuthorize(t, server, clientID, challenge, "")
	rec := legacyDo(t, server, http.MethodGet, "/consent?txn="+url.QueryEscape(txn), "", "", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `name="passphrase"`) ||
		!strings.Contains(rec.Body.String(), "Authorize harvester") {
		t.Fatalf("consent page = %d %s", rec.Code, rec.Body.String())
	}
}
