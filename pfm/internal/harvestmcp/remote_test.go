package harvestmcp

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func remoteRequest(
	t *testing.T,
	server *RemoteServer,
	method, path, body, contentType string,
) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "https://harvester.example.test"+path, strings.NewReader(body))
	req.Host = "harvester.example.test"
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	return rec
}

func TestConsentPageTxnInputIsHidden(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	const txn = "distinctive-consent-txn-qa-20260821"
	doc, err := html.Parse(strings.NewReader(consentPage(txn, "")))
	if err != nil {
		t.Fatalf("parse consent page: %v", err)
	}

	var txnInputs []*html.Node
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "input" {
			for _, attr := range node.Attr {
				if attr.Key == "name" && attr.Val == "txn" {
					txnInputs = append(txnInputs, node)
					break
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(doc)

	if len(txnInputs) == 0 {
		t.Fatal("consent page has no input named txn")
	}
	if len(txnInputs) != 1 {
		t.Fatalf("consent page has %d inputs named txn, want exactly one", len(txnInputs))
	}
	for _, attr := range txnInputs[0].Attr {
		if attr.Key == "type" {
			if attr.Val != "hidden" {
				t.Fatalf("consent page txn input type = %q, want %q", attr.Val, "hidden")
			}
			return
		}
	}
	t.Fatal("consent page txn input has no type attribute, want \"hidden\"")
}

func TestRemoteStaticGateway(t *testing.T) {
	base := t.TempDir()
	static, err := NewRemote(
		RemoteOptions{
			Runtime:     Runtime{Home: base, CacheDir: base + "/cache"},
			PublicURL:   "https://harvester.example.test",
			StaticToken: "example-fixture-token",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if rec := remoteRequest(
		t,
		static,
		http.MethodGet,
		"/healthz",
		"",
		"",
	); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"auth":true`) {
		t.Fatalf("static health = %d %s", rec.Code, rec.Body.String())
	}
	if rec := remoteRequest(
		t,
		static,
		http.MethodGet,
		"/mcp/",
		"",
		"",
	); rec.Code == http.StatusPermanentRedirect ||
		rec.Code == http.StatusTemporaryRedirect {
		t.Fatalf("/mcp/ unexpectedly redirected: %d", rec.Code)
	}
	rec := remoteRequest(t, static, http.MethodGet, "/mcp", "", "")
	if rec.Code != http.StatusUnauthorized ||
		!strings.Contains(rec.Header().Get("WWW-Authenticate"), "resource_metadata") {
		t.Fatalf("static challenge = %d %s", rec.Code, rec.Header().Get("WWW-Authenticate"))
	}
	req := httptest.NewRequest(http.MethodGet, "https://harvester.example.test/mcp", nil)
	req.Host = "harvester.example.test"
	req.Header.Set("Authorization", "Bearer example-fixture-token")
	rec = httptest.NewRecorder()
	static.Handler().ServeHTTP(rec, req)
	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("static token rejected: %d %s", rec.Code, rec.Body.String())
	}
}

func TestRemoteOAuthPKCEAndRefreshRotationInMemory(t *testing.T) {
	base := t.TempDir()
	server, err := NewRemote(
		RemoteOptions{
			Runtime:    Runtime{Home: base, CacheDir: base + "/cache"},
			PublicURL:  "https://harvester.example.test",
			Passphrase: "fixture-pass",
			StatePath:  base + "/state/auth.json",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	reg := remoteRequest(
		t,
		server,
		http.MethodPost,
		"/register",
		`{"client_name":"fixture","redirect_uris":["https://client.example/callback"],"token_endpoint_auth_method":"none"}`,
		"application/json",
	)
	if reg.Code != http.StatusCreated {
		t.Fatalf("register = %d %s", reg.Code, reg.Body.String())
	}
	var client map[string]any
	if err := json.Unmarshal(reg.Body.Bytes(), &client); err != nil {
		t.Fatal(err)
	}
	clientID, _ := client["client_id"].(string)
	verifier := strings.Repeat("v", 43)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	query := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {"https://client.example/callback"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {"s"},
		"scope":                 {HarvesterScope},
	}
	auth := remoteRequest(t, server, http.MethodGet, "/authorize?"+query.Encode(), "", "")
	if auth.Code != http.StatusFound || !strings.Contains(auth.Header().Get("Location"), "/consent?txn=") {
		t.Fatalf("authorize = %d %s", auth.Code, auth.Header().Get("Location"))
	}
	txn := strings.TrimPrefix(auth.Header().Get("Location"), "https://harvester.example.test/consent?txn=")
	consent := remoteRequest(
		t,
		server,
		http.MethodPost,
		"/consent",
		"txn="+url.QueryEscape(txn)+"&passphrase=fixture-pass",
		"application/x-www-form-urlencoded",
	)
	if consent.Code != http.StatusFound {
		t.Fatalf("consent = %d %s", consent.Code, consent.Body.String())
	}
	callback, err := url.Parse(consent.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	tokenBody := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {callback.Query().Get("code")},
		"client_id":     {clientID},
		"redirect_uri":  {"https://client.example/callback"},
		"code_verifier": {verifier},
	}.Encode()
	token := remoteRequest(t, server, http.MethodPost, "/token", tokenBody, "application/x-www-form-urlencoded")
	if token.Code != http.StatusOK {
		t.Fatalf("token = %d %s", token.Code, token.Body.String())
	}
	var first tokenResponse
	if err := json.Unmarshal(token.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	refreshBody := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {first.RefreshToken},
		"client_id":     {clientID},
	}.Encode()
	rotated := remoteRequest(t, server, http.MethodPost, "/token", refreshBody, "application/x-www-form-urlencoded")
	if rotated.Code != http.StatusOK {
		t.Fatalf("refresh = %d %s", rotated.Code, rotated.Body.String())
	}
	old := remoteRequest(t, server, http.MethodPost, "/token", refreshBody, "application/x-www-form-urlencoded")
	if old.Code == http.StatusOK {
		t.Fatal("rotated refresh token remained usable")
	}
}

func TestRemoteResourceRules(t *testing.T) {
	if ResourceMatches("HTTPS://Harvester.Example/mcp", "https://harvester.example/mcp") != true {
		t.Fatal("scheme/host case should match")
	}
	if ResourceMatches("https://harvester.example/mcp/", "https://harvester.example/mcp") {
		t.Fatal("trailing slash should not match")
	}
}

func TestRemoteMetadataAliasesAndRegistrationRules(t *testing.T) {
	base := t.TempDir()
	server, err := NewRemote(
		RemoteOptions{
			Runtime:    Runtime{Home: base, CacheDir: base + "/cache"},
			PublicURL:  "https://harvester.example.test",
			Passphrase: "pass",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{
		"/.well-known/oauth-protected-resource",
		"/.well-known/oauth-protected-resource/mcp",
		"/.well-known/oauth-authorization-server",
		"/.well-known/oauth-authorization-server/mcp",
		"/.well-known/openid-configuration",
		"/.well-known/openid-configuration/mcp",
	}
	var first []byte
	for _, path := range paths {
		rec := remoteRequest(t, server, http.MethodGet, path, "", "")
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" ||
			rec.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Fatalf("metadata %s = %d headers=%v", path, rec.Code, rec.Header())
		}
		if strings.Contains(path, "protected") {
			if first == nil {
				first = rec.Body.Bytes()
			}
		} else if strings.Contains(path, "authorization") && !strings.Contains(path, "openid") {
			if !strings.Contains(rec.Body.String(), `"code_challenge_methods_supported":["S256"]`) {
				t.Fatalf("auth metadata omitted S256: %s", rec.Body.String())
			}
		}
	}
	bare := remoteRequest(t, server, http.MethodGet, paths[0], "", "")
	suffixed := remoteRequest(t, server, http.MethodGet, paths[1], "", "")
	if string(bare.Body.Bytes()) != string(suffixed.Body.Bytes()) {
		t.Fatal("protected-resource metadata aliases drifted")
	}
	badType := remoteRequest(
		t,
		server,
		http.MethodPost,
		"/register",
		"redirect_uris=x",
		"application/x-www-form-urlencoded",
	)
	if badType.Code != http.StatusBadRequest || !strings.Contains(badType.Body.String(), "invalid_client_metadata") {
		t.Fatalf("registration wrong content type = %d %s", badType.Code, badType.Body.String())
	}
	badJSON := remoteRequest(t, server, http.MethodPost, "/register", "{", "application/json")
	if badJSON.Code != http.StatusBadRequest || !strings.Contains(badJSON.Body.String(), "invalid_client_metadata") {
		t.Fatalf("registration malformed JSON = %d %s", badJSON.Code, badJSON.Body.String())
	}
	if len(first) == 0 {
		t.Fatal("protected-resource metadata was empty")
	}
}
