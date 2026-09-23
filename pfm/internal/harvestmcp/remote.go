package harvestmcp

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/harvest"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

const mcpPath = "/mcp"

// mcpMaxBodyBytes bounds an authenticated /mcp POST body — unbounded before
// this, the SDK's io.ReadAll(req.Body) let one token holder exhaust memory
// with a single request. 1 MiB matches /register, /token and /revoke's own
// cap (below) and comfortably fits the largest legitimate tool call: fetch
// and download take up to 50 source URLs each, a few hundred bytes apiece.
const mcpMaxBodyBytes = 1 << 20

// RemoteOptions is the external gateway contract. The daemon (pfm mcp serve)
// mounts it on its second, authenticated port; handler tests use it without
// opening a listener.
type RemoteOptions struct {
	Runtime     Runtime
	Version     string
	PublicURL   string
	Passphrase  string
	StaticToken string
	StatePath   string
}

// RemoteServer is the external gateway: exact /mcp path, bearer/OAuth wall,
// and local reads confined to the exported-artifact directory. It is never unauthenticated.
type RemoteServer struct {
	publicURL string
	resource  string
	parsed    *url.URL
	service   *Service
	store     *authStore
	mcp       http.Handler
}

// NewRemote builds the external gateway handler without binding a socket. It
// refuses to exist without a credential, and always confines local reads to
// the exported-artifact directory: a remote caller never owns this machine.
func NewRemote(options RemoteOptions) (*RemoteServer, error) {
	publicURL := strings.TrimRight(strings.TrimSpace(options.PublicURL), "/")
	parsed, err := url.Parse(publicURL)
	if err != nil || parsed.Hostname() == "" {
		return nil, fmt.Errorf("public_url must include a hostname, got %q", publicURL)
	}
	// validRedirectURI (auth.go) is the SAME https-or-loopback rule client
	// redirect URIs must pass; reused rather than a second copy here. A plain
	// http public_url would carry the passphrase, codes, and every bearer and
	// refresh token in cleartext across the internet.
	if !validRedirectURI(publicURL) {
		return nil, fmt.Errorf(
			"external.publicURL must be https (or http on loopback), got %q", publicURL,
		)
	}
	if options.Passphrase == "" && options.StaticToken == "" {
		return nil, errors.New(
			"the external harvester gateway requires external.auth.passphrase and/or external.auth.staticToken; it is never unauthenticated",
		)
	}
	runtime := options.Runtime
	if runtime.Clock == nil {
		runtime.Clock = clock.Real
	}
	cache, err := harvest.CacheRoot(runtime.CacheDir)
	if err != nil {
		return nil, fmt.Errorf("resolve harvester cache root: %w", err)
	}
	runtime.CacheDir = cache
	runtime.LocalRoots = []string{filepath.Join(cache, "public")}
	version := options.Version
	if version == "" {
		version = "dev"
	}
	service, err := NewConfiguredHarvester(version, runtime)
	if err != nil {
		return nil, err
	}
	r := &RemoteServer{publicURL: publicURL, resource: publicURL + mcpPath, parsed: parsed, service: service}
	statePath := options.StatePath
	if statePath == "" {
		statePath = defaultAuthStatePath(service.runtime.CacheDir)
	}
	r.store = newAuthStore(publicURL, r.resource, options.Passphrase, options.StaticToken, statePath, runtime.Clock)
	r.mcp = mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return service.Server() },
		&mcp.StreamableHTTPOptions{JSONResponse: false, Stateless: false, DisableLocalhostProtection: true},
	)
	return r, nil
}

// Handler returns a net/http handler suitable for httptest.NewRecorder or a
// production server. It never redirects /mcp to /mcp/.
func (r *RemoteServer) Handler() http.Handler {
	return obs.Handler("harvester-remote", http.HandlerFunc(r.serveHTTP))
}

// Close releases the shared conversion worker. It is safe to call repeatedly,
// which lets command setup failures clean up a partially constructed gateway.
func (r *RemoteServer) Close() error {
	if r == nil || r.service == nil {
		return nil
	}
	return r.service.Close()
}

func (r *RemoteServer) serveHTTP(w http.ResponseWriter, req *http.Request) {
	if !r.allowedHost(req.Host) {
		http.Error(w, "host is not allowed", http.StatusForbidden)
		return
	}
	switch req.URL.Path {
	case "/healthz":
		if req.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		r.writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "auth": r.store != nil}, false)
	case mcpPath:
		if r.store != nil && !r.bearerOK(req) {
			r.unauthorized(w)
			return
		}
		req.Body = http.MaxBytesReader(w, req.Body, mcpMaxBodyBytes)
		r.mcp.ServeHTTP(w, req)
	case "/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp":
		if r.store == nil {
			http.NotFound(w, req)
			return
		}
		r.metadata(
			w,
			map[string]any{
				"resource":              r.resource,
				"authorization_servers": []string{r.publicURL + "/"},
				"scopes_supported":      []string{HarvesterScope},
				"resource_name":         "harvester",
			},
		)
	case "/.well-known/oauth-authorization-server",
		"/.well-known/oauth-authorization-server/mcp",
		"/.well-known/openid-configuration",
		"/.well-known/openid-configuration/mcp":
		if r.store == nil || r.store.passphrase == "" {
			http.NotFound(w, req)
			return
		}
		r.metadata(w, r.authMetadata())
	case "/register":
		if r.store == nil || r.store.passphrase == "" {
			http.NotFound(w, req)
			return
		}
		r.register(w, req)
	case "/authorize":
		if r.store == nil || r.store.passphrase == "" {
			http.NotFound(w, req)
			return
		}
		r.authorize(w, req)
	case "/consent":
		if r.store == nil || r.store.passphrase == "" {
			http.NotFound(w, req)
			return
		}
		r.consent(w, req)
	case "/token":
		if r.store == nil || r.store.passphrase == "" {
			http.NotFound(w, req)
			return
		}
		r.token(w, req)
	case "/revoke":
		if r.store == nil || r.store.passphrase == "" {
			http.NotFound(w, req)
			return
		}
		r.revoke(w, req)
	default:
		http.NotFound(w, req)
	}
}

func (r *RemoteServer) allowedHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}
	requested, err := url.Parse("http://" + host)
	if err != nil || requested.Hostname() == "" {
		return false
	}
	return strings.EqualFold(requested.Host, r.parsed.Host) ||
		strings.EqualFold(requested.Hostname(), r.parsed.Hostname()) ||
		isLoopback(requested.Hostname())
}

func isLoopback(host string) bool {
	return strings.EqualFold(host, "localhost") || net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()
}

func (r *RemoteServer) bearerOK(req *http.Request) bool {
	parts := strings.Fields(req.Header.Get("Authorization"))
	return len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") && r.store.verify(parts[1])
}

func (r *RemoteServer) unauthorized(w http.ResponseWriter) {
	w.Header().
		Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata=%q, scope=%q`, r.publicURL+"/.well-known/oauth-protected-resource/mcp", HarvesterScope))
	r.oauthError(w, http.StatusUnauthorized, "invalid_token", "missing or invalid bearer token")
}

func (r *RemoteServer) metadata(w http.ResponseWriter, value map[string]any) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	r.writeJSON(w, http.StatusOK, value, false)
}

func (r *RemoteServer) authMetadata() map[string]any {
	issuer := r.publicURL + "/"
	return map[string]any{
		"issuer":                           issuer,
		"authorization_endpoint":           r.publicURL + "/authorize",
		"token_endpoint":                   r.publicURL + "/token",
		"registration_endpoint":            r.publicURL + "/register",
		"revocation_endpoint":              r.publicURL + "/revoke",
		"scopes_supported":                 []string{HarvesterScope},
		"response_types_supported":         []string{"code"},
		"grant_types_supported":            []string{authorizationCodeGrant, refreshTokenGrant},
		"code_challenge_methods_supported": []string{pkceMethodS256},
		"token_endpoint_auth_methods_supported": []string{
			tokenAuthNone,
			tokenAuthClientSecretPost,
			tokenAuthClientSecretBasic,
		},
		"revocation_endpoint_auth_methods_supported": []string{
			tokenAuthNone,
			tokenAuthClientSecretPost,
			tokenAuthClientSecretBasic,
		},
	}
}

func (r *RemoteServer) writeJSON(w http.ResponseWriter, status int, value any, noStore bool) {
	w.Header().Set("Content-Type", "application/json")
	if noStore {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Pragma", "no-cache")
	}
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		fmt.Fprintf(os.Stderr, "harvester remote: write JSON: %v\n", err)
	}
}

func (r *RemoteServer) oauthError(w http.ResponseWriter, status int, code, description string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	r.writeJSON(w, status, map[string]string{"error": code, "error_description": description}, false)
}

func (r *RemoteServer) register(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost || !contentType(req, "application/json") {
		r.oauthError(
			w,
			http.StatusBadRequest,
			"invalid_client_metadata",
			"registration requests must use application/json",
		)
		return
	}
	var input persistedClient
	if err := json.NewDecoder(io.LimitReader(req.Body, mcpMaxBodyBytes)).Decode(&input); err != nil {
		r.oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "registration body is not valid JSON")
		return
	}
	client, secret, code := r.store.register(input)
	if code == oauthErrorTooManyClients {
		r.oauthError(
			w,
			http.StatusTooManyRequests,
			code,
			"the harvester gateway has reached its registered-client limit; ask the operator to remove an unused client",
		)
		return
	}
	if code != "" {
		r.oauthError(w, http.StatusBadRequest, code, "client metadata is not supported")
		return
	}
	response := map[string]any{
		"client_id":                  client.ClientID,
		"client_id_issued_at":        client.ClientIDIssuedAt,
		"token_endpoint_auth_method": client.TokenEndpointAuthMethod,
		"redirect_uris":              client.RedirectURIs,
		"grant_types":                client.GrantTypes,
		"response_types":             client.ResponseTypes,
		"scope":                      client.Scope,
		"client_secret_expires_at":   client.ClientSecretExpiresAt,
	}
	if secret != "" {
		response["client_secret"] = secret
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	r.writeJSON(w, http.StatusCreated, response, false)
}

func contentType(req *http.Request, want string) bool {
	return strings.EqualFold(strings.TrimSpace(strings.Split(req.Header.Get("Content-Type"), ";")[0]), want)
}

func (r *RemoteServer) authorize(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	q := req.URL.Query()
	clientID, redirect := q.Get("client_id"), q.Get("redirect_uri")
	r.store.mu.Lock()
	client, known := r.store.clients[clientID]
	r.store.mu.Unlock()
	if !known || !slices.Contains(client.RedirectURIs, redirect) {
		r.oauthError(w, http.StatusBadRequest, "invalid_request", "unknown client or redirect_uri")
		return
	}
	resource := q.Get("resource")
	if resource != "" && !ResourceMatches(resource, r.resource) {
		r.redirectError(
			w,
			redirect,
			q.Get("state"),
			oauthErrorInvalidTarget,
			"resource does not identify this MCP server",
		)
		return
	}
	scope := strings.Fields(q.Get("scope"))
	if len(scope) == 0 {
		scope = []string{HarvesterScope}
	}
	txn, err := r.store.begin(
		client,
		redirect,
		q.Get("state"),
		q.Get("code_challenge"),
		q.Get("code_challenge_method"),
		resource,
		scope,
	)
	if err != nil {
		r.redirectError(w, redirect, q.Get("state"), "invalid_request", err.Error())
		return
	}
	http.Redirect(w, req, r.publicURL+"/consent?txn="+url.QueryEscape(txn), http.StatusFound)
}

func (r *RemoteServer) redirectError(w http.ResponseWriter, redirect, state, code, description string) {
	u, err := url.Parse(redirect)
	if err != nil {
		r.oauthError(w, http.StatusBadRequest, code, description)
		return
	}
	q := u.Query()
	q.Set("error", code)
	q.Set("error_description", description)
	if state != "" {
		q.Set("state", state)
	}
	u.RawQuery = q.Encode()
	w.Header().Set("Location", u.String())
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusFound)
}

func (r *RemoteServer) consent(w http.ResponseWriter, req *http.Request) {
	txn := req.URL.Query().Get("txn")
	if req.Method == http.MethodPost {
		if err := req.ParseForm(); err != nil {
			expiredConsent(w)
			return
		}
		txn = req.Form.Get("txn")
		code, ok, alive := r.store.consent(txn, req.Form.Get("passphrase"), remoteAddr(req))
		if !alive {
			expiredConsent(w)
			return
		}
		if !ok {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, consentPage(txn, "Incorrect passphrase."))
			return
		}
		r.store.mu.Lock()
		// The code is available by value; complete_consent's redirect data is
		// retained in the code record and consumed only at token exchange.
		ac := r.store.codes[code]
		r.store.mu.Unlock()
		w.Header().Set("Cache-Control", "no-store")
		r.redirectErrorOrCode(w, ac.RedirectURI, req, code, ac)
		return
	}
	r.store.mu.Lock()
	_, alive := r.store.pending[txn]
	r.store.mu.Unlock()
	if req.Method != http.MethodGet || !alive {
		expiredConsent(w)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, consentPage(txn, ""))
}

func expiredConsent(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	_, _ = io.WriteString(w, "<p>This authorization link has expired. Start again from Claude.</p>")
}

func (r *RemoteServer) redirectErrorOrCode(
	w http.ResponseWriter,
	redirect string,
	req *http.Request,
	code string,
	ac authorizationCode,
) {
	u, err := url.Parse(redirect)
	if err != nil {
		http.Error(w, "invalid redirect", http.StatusBadRequest)
		return
	}
	q := u.Query()
	q.Set("code", code)
	if ac.State != "" {
		q.Set("state", ac.State)
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, req, u.String(), http.StatusFound)
}

func consentPage(txn, message string) string {
	banner := `<p class="hint">Claude is asking to connect to your harvester server.</p>`
	if message != "" {
		banner = `<p class="err">` + html.EscapeString(message) + `</p>`
	}
	return `<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>harvester — authorize</title>
<style>
  body { font-family: ui-sans-serif, system-ui, sans-serif; background:#faf9f7; color:#1c1917;
         display:grid; place-items:center; min-height:100vh; margin:0; }
  form { background:#fff; padding:2rem; border-radius:12px; border:1px solid #e7e5e4;
          box-shadow:0 1px 3px rgba(0,0,0,.06); width:min(92vw,26rem); }
  h1 { font-size:1.1rem; margin:0 0 .25rem; }
  .hint, .err { font-size:.875rem; margin:0 0 1.25rem; }
  .hint { color:#57534e; } .err { color:#b91c1c; }
  input { width:100%; padding:.6rem .7rem; font-size:1rem; border:1px solid #d6d3d1;
           border-radius:8px; box-sizing:border-box; }
  button { margin-top:1rem; width:100%; padding:.6rem; font-size:1rem; border:0;
            border-radius:8px; background:#1c1917; color:#fafaf9; cursor:pointer; }
  @media (prefers-color-scheme: dark) {
    body { background:#1c1917; color:#fafaf9; }
    form { background:#292524; border-color:#44403c; }
    .hint { color:#a8a29e; }
    input { background:#1c1917; color:#fafaf9; border-color:#57534e; }
    button { background:#fafaf9; color:#1c1917; }
  }
</style></head><body>
<form method="post">
  <h1>Authorize harvester</h1>
  ` + banner + `
  <input type="hidden" name="txn" value="` + html.EscapeString(txn) + `">
  <input type="password" name="passphrase" placeholder="Operator passphrase" autofocus required>
  <button type="submit">Authorize</button>
</form></body></html>`
}

func (r *RemoteServer) token(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost || !contentType(req, "application/x-www-form-urlencoded") {
		r.oauthError(
			w,
			http.StatusBadRequest,
			"invalid_request",
			"token requests must use application/x-www-form-urlencoded",
		)
		return
	}
	if err := req.ParseForm(); err != nil {
		r.oauthError(w, http.StatusBadRequest, "invalid_request", "request body is not UTF-8")
		return
	}
	clientID, secret := clientCredentials(req)
	if clientID == "" {
		clientID = req.Form.Get("client_id")
	}
	if secret == "" {
		secret = req.Form.Get("client_secret")
	}
	if !r.authenticateClient(
		clientID,
		secret,
		req.Form.Get("client_secret") != "" || req.Header.Get("Authorization") != "",
	) {
		r.oauthError(w, http.StatusBadRequest, "invalid_client", "client authentication failed")
		return
	}
	resource := req.Form.Get("resource")
	if resource != "" && !ResourceMatches(resource, r.resource) {
		r.oauthError(w, http.StatusBadRequest, oauthErrorInvalidTarget, "resource does not identify this MCP server")
		return
	}
	grant := req.Form.Get("grant_type")
	var response tokenResponse
	var code string
	switch grant {
	case authorizationCodeGrant:
		response, code = r.store.exchange(
			req.Form.Get("code"),
			clientID,
			req.Form.Get("redirect_uri"),
			req.Form.Get("code_verifier"),
			resource,
		)
	case refreshTokenGrant:
		response, code = r.store.refreshExchange(req.Form.Get(refreshTokenGrant), clientID, resource)
	default:
		r.oauthError(w, http.StatusBadRequest, "unsupported_grant_type", "grant_type is not supported")
		return
	}
	if code != "" {
		r.oauthError(w, http.StatusBadRequest, code, tokenErrorDescription(code))
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	r.writeJSON(w, http.StatusOK, response, true)
}

func clientCredentials(req *http.Request) (string, string) {
	h := req.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Basic ") {
		return "", ""
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(strings.TrimPrefix(h, "Basic ")))
	if err != nil {
		return "", ""
	}
	parts := strings.SplitN(string(b), ":", 2)
	if len(parts) != 2 {
		return "", ""
	}
	id, _ := url.QueryUnescape(parts[0])
	secret, _ := url.QueryUnescape(parts[1])
	return id, secret
}

func (r *RemoteServer) authenticateClient(id, secret string, presented bool) bool {
	r.store.mu.Lock()
	c, ok := r.store.clients[id]
	r.store.mu.Unlock()
	if !ok {
		return false
	}
	switch c.TokenEndpointAuthMethod {
	case tokenAuthNone:
		return true
	case tokenAuthClientSecretPost, tokenAuthClientSecretBasic:
		// c.ClientSecret is a read-compatibility door only (auth.go's
		// persistedClient doc comment) — verify by digest, the stored shape
		// since L2-F20, via the same equalSecret constant-time compare a
		// bare secret would have used.
		return presented && c.ClientSecretHash != "" && equalSecret(digest(secret), c.ClientSecretHash)
	default:
		return false
	}
}

func tokenErrorDescription(code string) string {
	if code == oauthErrorInvalidTarget {
		return "resource does not identify this MCP server"
	}
	return "authorization grant is invalid"
}

func (r *RemoteServer) revoke(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost || !contentType(req, "application/x-www-form-urlencoded") {
		r.oauthError(
			w,
			http.StatusBadRequest,
			"invalid_request",
			"revocation requests must use application/x-www-form-urlencoded",
		)
		return
	}
	if err := req.ParseForm(); err != nil {
		r.oauthError(w, http.StatusBadRequest, "invalid_request", "request body is not UTF-8")
		return
	}
	id, secret := clientCredentials(req)
	if id == "" {
		id, secret = req.Form.Get("client_id"), req.Form.Get("client_secret")
	}
	if id != "" && !r.authenticateClient(id, secret, true) {
		r.oauthError(w, http.StatusBadRequest, "invalid_client", "client authentication failed")
		return
	}
	r.store.revoke(req.Form.Get("token"))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusOK)
}
