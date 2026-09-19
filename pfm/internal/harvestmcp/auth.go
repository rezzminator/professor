package harvestmcp

// auth.go is a deliberately small OAuth 2.1 provider.  The old Harvester
// service has one operator, one resource, and one scope; keeping those facts
// explicit makes the HTTP boundary auditable and, importantly, keeps live
// credentials out of the state file.

import (
	cryptoRand "crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"hostops/pfm/internal/clock"
)

const (
	HarvesterScope             = "harvest"
	authorizationCodeGrant     = "authorization_code"
	pkceMethodS256             = "S256"
	tokenAuthClientSecretBasic = "client_secret_basic"
	tokenAuthClientSecretPost  = "client_secret_post"
	tokenAuthNone              = "none"
	refreshTokenGrant          = "refresh_token"
	oauthErrorInvalidGrant     = "invalid_grant"
	oauthErrorInvalidTarget    = "invalid_target"
	oauthErrorServer           = "server_error"
	authCodeTTL                = 5 * time.Minute
	accessTokenTTL             = time.Hour
	consentTTL                 = 10 * time.Minute
	maxConsentAttempts         = 5
	defaultTokenExpiry         = 3600
)

var pkceChallenge = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", sum[:])
}

func equalSecret(got, want string) bool {
	a, b := []byte(got), []byte(want)
	if len(a) != len(b) {
		// Compare fixed-size digests so a length mismatch does not expose the
		// common prefix through the timing of the comparison.
		ha, hb := sha256.Sum256(a), sha256.Sum256(b)
		_ = subtle.ConstantTimeCompare(ha[:], hb[:])
		return false
	}
	return subtle.ConstantTimeCompare(a, b) == 1
}

func tokenURLSafe(size int) (string, error) {
	raw := make([]byte, size)
	if _, err := cryptoRand.Read(raw); err != nil {
		return "", fmt.Errorf("generate OAuth token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// persistedClient's ClientSecret field is a READ-compatibility door only: a
// state.json written before L2-F20's fix, or a /register request body
// (register() below always clears whatever a caller supplies before
// persisting). saveLocked never writes a non-empty ClientSecret — load
// migrates one to ClientSecretHash the moment it is seen, atomically
// rewriting the file — so the cleartext never survives past the first open
// past this fix.
type persistedClient struct {
	ClientID                string   `json:"client_id"`
	ClientSecret            string   `json:"client_secret,omitempty"`
	ClientSecretHash        string   `json:"client_secret_hash,omitempty"`
	ClientIDIssuedAt        int64    `json:"client_id_issued_at,omitempty"`
	ClientSecretExpiresAt   int64    `json:"client_secret_expires_at"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	Scope                   string   `json:"scope,omitempty"`
}

type persistedRefresh struct {
	Hash     string   `json:"_hash"`
	ClientID string   `json:"client_id"`
	Scopes   []string `json:"scopes"`
}

type authState struct {
	Clients []persistedClient  `json:"clients"`
	Refresh []persistedRefresh `json:"refresh"`
}

type oauthClient struct {
	persistedClient
}

type pendingConsent struct {
	ClientID    string
	RedirectURI string
	State       string
	Scope       []string
	Challenge   string
	Method      string
	Resource    string
	Created     time.Time
	Attempts    int
}

type authorizationCode struct {
	Code        string
	ClientID    string
	RedirectURI string
	State       string
	Scope       []string
	Challenge   string
	Resource    string
	Expires     time.Time
}

type accessToken struct {
	Raw      string
	ClientID string
	Scope    []string
	Resource string
	Expires  time.Time
}

type refreshToken struct {
	Hash     string
	ClientID string
	Scope    []string
}

type authStore struct {
	mu         sync.Mutex
	issuer     string
	resource   string
	passphrase string
	staticHash string
	statePath  string
	clock      clock.Clock
	clients    map[string]oauthClient
	pending    map[string]pendingConsent
	codes      map[string]authorizationCode
	access     map[string]accessToken
	refresh    map[string]refreshToken
	// limiter is the real bound on passphrase guessing (L2-F21):
	// maxConsentAttempts above caps ONE transaction, but /authorize mints a
	// fresh one on every request.
	limiter *passphraseLimiter
}

func newAuthStore(issuer, resource, passphrase, staticToken, statePath string, clocks ...clock.Clock) *authStore {
	watch := clock.Real
	if len(clocks) > 0 && clocks[0] != nil {
		watch = clocks[0]
	}
	s := &authStore{
		issuer: issuer, resource: resource, passphrase: passphrase,
		statePath: statePath, clock: watch, clients: map[string]oauthClient{},
		pending: map[string]pendingConsent{}, codes: map[string]authorizationCode{},
		access: map[string]accessToken{}, refresh: map[string]refreshToken{},
		limiter: newPassphraseLimiter(),
	}
	if staticToken != "" {
		s.staticHash = digest(staticToken)
	}
	s.load()
	return s
}

func (s *authStore) load() {
	if s.statePath == "" {
		return
	}
	b, err := os.ReadFile(s.statePath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "harvester auth: read state %s: %v\n", s.statePath, err)
		}
		return
	}
	var state authState
	if err := json.Unmarshal(b, &state); err != nil {
		fmt.Fprintf(os.Stderr, "harvester auth: invalid state %s: %v\n", s.statePath, err)
		return
	}
	migrated := false
	for i := range state.Clients {
		c := &state.Clients[i]
		if c.ClientID == "" {
			continue
		}
		// L2-F20: a client secret written before this fix is cleartext.
		// Migrate it to a digest and drop the plaintext the moment it is
		// seen — the write below rewrites the file atomically, so the
		// cleartext never lands on disk again past this load.
		if c.ClientSecret != "" {
			c.ClientSecretHash = digest(c.ClientSecret)
			c.ClientSecret = ""
			migrated = true
		}
		s.clients[c.ClientID] = oauthClient{persistedClient: *c}
	}
	for _, r := range state.Refresh {
		if r.Hash == "" || r.ClientID == "" {
			// A malformed refresh record invalidates the set, matching the old
			// provider's fail-safe load behavior — break, not return, so a
			// client-secret migration below still persists.
			s.refresh = map[string]refreshToken{}
			break
		}
		s.refresh[r.Hash] = refreshToken{Hash: r.Hash, ClientID: r.ClientID, Scope: append([]string(nil), r.Scopes...)}
	}
	if migrated {
		// s.refresh is fully populated above; saveLocked persists both maps
		// together, so a migration never drops a live refresh token.
		s.mu.Lock()
		s.saveLocked()
		s.mu.Unlock()
	}
}

func (s *authStore) saveLocked() {
	if s.statePath == "" {
		return
	}
	state := authState{}
	for id := range s.clients {
		c := s.clients[id]
		state.Clients = append(state.Clients, c.persistedClient)
	}
	for _, r := range s.refresh {
		state.Refresh = append(state.Refresh, persistedRefresh{Hash: r.Hash, ClientID: r.ClientID, Scopes: r.Scope})
	}
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "harvester auth: encode state %s: %v\n", s.statePath, err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.statePath), 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "harvester auth: create state directory: %v\n", err)
		return
	}
	tmp := s.statePath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "harvester auth: write state: %v\n", err)
		return
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "harvester auth: chmod state: %v\n", err)
		return
	}
	if err := os.Rename(tmp, s.statePath); err != nil {
		fmt.Fprintf(os.Stderr, "harvester auth: install state: %v\n", err)
	}
}

// register returns the persisted client, an optional client secret, and the
// OAuth error code (empty on success). Registration errors are surfaced at the
// HTTP boundary as RFC 7591 error labels, so keep the code as a string rather
// than leaking implementation errors into the response shape.
func (s *authStore) register(c persistedClient) (persistedClient, string, string) {
	// Client identifiers and secrets are server-issued; never honor a caller's
	// attempt to smuggle either into the registration document.
	c.ClientID = ""
	c.ClientSecret = ""
	c.ClientSecretHash = ""
	if c.TokenEndpointAuthMethod == "" {
		c.TokenEndpointAuthMethod = tokenAuthClientSecretBasic
	}
	switch c.TokenEndpointAuthMethod {
	case tokenAuthNone, tokenAuthClientSecretPost, tokenAuthClientSecretBasic:
	default:
		return persistedClient{}, "", "invalid_client_metadata"
	}
	for _, redirect := range c.RedirectURIs {
		if !validRedirectURI(redirect) {
			return persistedClient{}, "", "invalid_redirect_uri"
		}
	}
	// L2-F21: /register was unthrottled and persisted every client forever.
	// Refuse at the ceiling rather than silently evicting an
	// oldest-but-still-used client — see maxRegisteredClients' doc comment.
	s.mu.Lock()
	full := len(s.clients) >= maxRegisteredClients
	s.mu.Unlock()
	if full {
		return persistedClient{}, "", oauthErrorTooManyClients
	}
	id, err := tokenURLSafe(18)
	if err != nil {
		return persistedClient{}, "", oauthErrorServer
	}
	c.ClientID = id
	c.ClientIDIssuedAt = s.clock.Now().Unix()
	var secret string
	if c.TokenEndpointAuthMethod != tokenAuthNone {
		secret, err = tokenURLSafe(24)
		if err != nil {
			return persistedClient{}, "", oauthErrorServer
		}
		// The plaintext is returned once, below, for the registration
		// response (RFC 7591) — never stored. Only its digest is persisted
		// (L2-F20); authenticateClient (remote.go) verifies against it.
		c.ClientSecretHash = digest(secret)
	}
	c.ClientSecretExpiresAt = 0
	if len(c.GrantTypes) == 0 {
		c.GrantTypes = []string{authorizationCodeGrant, refreshTokenGrant}
	}
	if len(c.ResponseTypes) == 0 {
		c.ResponseTypes = []string{"code"}
	}
	if c.Scope == "" {
		c.Scope = HarvesterScope
	}
	s.mu.Lock()
	s.clients[id] = oauthClient{persistedClient: c}
	s.saveLocked()
	s.mu.Unlock()
	return c, secret, ""
}

func validRedirectURI(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.Fragment != "" {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return true
	case "http":
		if strings.EqualFold(u.Hostname(), "localhost") {
			return true
		}
		ip := net.ParseIP(u.Hostname())
		return ip != nil && ip.IsLoopback()
	default:
		return false
	}
}

// ResourceMatches permits only case changes in scheme and host. Ports,
// path, query, userinfo, fragments and trailing slash remain significant.
func ResourceMatches(candidate, expected string) bool {
	a, errA := url.Parse(candidate)
	b, errB := url.Parse(expected)
	if errA != nil || errB != nil || a.Scheme == "" || a.Hostname() == "" || b.Scheme == "" || b.Hostname() == "" ||
		a.Fragment != "" ||
		b.Fragment != "" {
		return false
	}
	userA, userB := "", ""
	if a.User != nil {
		userA = a.User.String()
	}
	if b.User != nil {
		userB = b.User.String()
	}
	return strings.EqualFold(a.Scheme, b.Scheme) &&
		strings.EqualFold(a.Hostname(), b.Hostname()) &&
		a.Port() == b.Port() && userA == userB &&
		a.EscapedPath() == b.EscapedPath() && a.RawQuery == b.RawQuery
}

func (s *authStore) begin(
	c oauthClient,
	redirect, state, challenge, method, resource string,
	scope []string,
) (string, error) {
	if resource == "" {
		resource = s.resource
	}
	if !ResourceMatches(resource, s.resource) {
		return "", errors.New("resource does not identify this MCP server")
	}
	if method == "" {
		method = pkceMethodS256
	}
	if method != pkceMethodS256 || !pkceChallenge.MatchString(challenge) {
		return "", errors.New("code_challenge is not valid PKCE")
	}
	txn, err := tokenURLSafe(18)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.pending[txn] = pendingConsent{
		ClientID:    c.ClientID,
		RedirectURI: redirect,
		State:       state,
		Scope:       scope,
		Challenge:   challenge,
		Method:      method,
		Resource:    resource,
		Created:     s.clock.Now(),
	}
	s.mu.Unlock()
	return txn, nil
}

// consent spends one passphrase guess against txn from source address addr.
// addr is metered by passphraseLimiter independently of maxConsentAttempts:
// that bound caps ONE transaction, this one caps every transaction addr (or
// the whole gateway) can mint. A locked-out guess is refused the SAME way a
// wrong passphrase is (alive=true, ok=false) — the caller never learns
// whether it hit the lockout or just guessed wrong.
func (s *authStore) consent(txn, supplied, addr string) (string, bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pending[txn]
	if !ok || s.clock.Now().Sub(p.Created) > consentTTL || s.passphrase == "" {
		delete(s.pending, txn)
		return "", false, false
	}
	p.Attempts++
	if p.Attempts > maxConsentAttempts {
		delete(s.pending, txn)
		return "", false, false
	}
	s.pending[txn] = p
	now := s.clock.Now()
	if !s.limiter.allowed(addr, now) {
		return "", false, true
	}
	if !equalSecret(supplied, s.passphrase) {
		s.limiter.recordFailure(addr, now)
		return "", false, true
	}
	s.limiter.recordSuccess(addr)
	delete(s.pending, txn)
	code, err := tokenURLSafe(24)
	if err != nil {
		return "", false, false
	}
	s.codes[code] = authorizationCode{
		Code:        code,
		ClientID:    p.ClientID,
		RedirectURI: p.RedirectURI,
		State:       p.State,
		Scope:       p.Scope,
		Challenge:   p.Challenge,
		Resource:    p.Resource,
		Expires:     s.clock.Now().Add(authCodeTTL),
	}
	return code, true, true
}

func (s *authStore) exchange(code, clientID, redirect, verifier, resource string) (tokenResponse, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.codes[code]
	if !ok || s.clock.Now().After(c.Expires) || c.ClientID != clientID || c.RedirectURI != redirect {
		return tokenResponse{}, oauthErrorInvalidGrant
	}
	if resource != "" && !ResourceMatches(resource, s.resource) {
		return tokenResponse{}, oauthErrorInvalidTarget
	}
	if !verifyPKCE(c.Challenge, verifier) {
		return tokenResponse{}, oauthErrorInvalidGrant
	}
	delete(s.codes, code)
	issued, err := s.issueLocked(c.ClientID, c.Scope)
	if err != nil {
		return tokenResponse{}, oauthErrorServer
	}
	return issued, ""
}

func verifyPKCE(challenge, verifier string) bool {
	if challenge == "" || verifier == "" {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:]) == challenge
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

func (s *authStore) issueLocked(clientID string, scope []string) (tokenResponse, error) {
	access, err := tokenURLSafe(24)
	if err != nil {
		return tokenResponse{}, err
	}
	refresh, err := tokenURLSafe(24)
	if err != nil {
		return tokenResponse{}, err
	}
	if len(scope) == 0 {
		scope = []string{HarvesterScope}
	}
	s.access[digest(access)] = accessToken{
		Raw:      access,
		ClientID: clientID,
		Scope:    append([]string(nil), scope...),
		Resource: s.resource,
		Expires:  s.clock.Now().Add(accessTokenTTL),
	}
	hash := digest(refresh)
	s.refresh[hash] = refreshToken{Hash: hash, ClientID: clientID, Scope: append([]string(nil), scope...)}
	s.saveLocked()
	return tokenResponse{
		AccessToken:  access,
		TokenType:    "Bearer",
		ExpiresIn:    defaultTokenExpiry,
		Scope:        strings.Join(scope, " "),
		RefreshToken: refresh,
	}, nil
}

func (s *authStore) refreshExchange(raw, clientID, resource string) (tokenResponse, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if resource != "" && !ResourceMatches(resource, s.resource) {
		return tokenResponse{}, oauthErrorInvalidTarget
	}
	hash := digest(raw)
	r, ok := s.refresh[hash]
	if !ok || r.ClientID != clientID {
		return tokenResponse{}, oauthErrorInvalidGrant
	}
	delete(s.refresh, hash)
	issued, err := s.issueLocked(clientID, r.Scope)
	if err != nil {
		return tokenResponse{}, oauthErrorServer
	}
	return issued, ""
}

func (s *authStore) verify(raw string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.staticHash != "" && equalSecret(digest(raw), s.staticHash) {
		return true
	}
	h, ok := s.access[digest(raw)]
	if !ok || s.clock.Now().After(h.Expires) || !ResourceMatches(h.Resource, s.resource) {
		return false
	}
	return true
}

func (s *authStore) revoke(raw string) {
	s.mu.Lock()
	delete(s.access, digest(raw))
	delete(s.refresh, digest(raw))
	s.saveLocked()
	s.mu.Unlock()
}

// defaultAuthStatePath keeps the OAuth store beside — never inside — the
// cache root, which the gateway serves to remote callers. The daemon passes
// external.stateDir from harvester.config.json when it is set.
func defaultAuthStatePath(cacheDir string) string {
	return filepath.Join(filepath.Dir(cacheDir), ".harvester-state", "auth.json")
}
