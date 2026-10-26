package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/zitadel/oidc/v3/pkg/client/rp"
	"github.com/zitadel/oidc/v3/pkg/oidc"

	"git.nite07.com/nite/engram/internal/store"
)

// OIDC client.
//
// All protocol interaction is delegated to github.com/zitadel/oidc/v3:
//   - discovery document and endpoint resolution: rp.NewRelyingPartyOIDC (this file only layers a TTL'd per-issuer cache on top,
//     invalidated by Invalidate after a configuration change).
//   - Authorization Code + PKCE: the challenge is computed by the OIDC library's oidc.NewSHACodeChallenge,
//     and the authorization URL and token exchange are handled by rp.AuthURL / rp.CodeExchange.
//   - ID Token validation: rp.CodeExchange goes through rp.IDTokenVerifier internally (signature against the JWKS, iss, aud, exp, iat).
//
// This file keeps only what the project needs and the library does not provide: the one-time state/nonce table, the claim name mapping,
// and handing the validation result to IdentityLinkService (oauthlink.go) for identity linking. The linking branches are not reimplemented here.

// Settings keys for the OIDC settings. The client_secret suffix is `_secret`,
// so store.IsSensitiveSettingKey requires it to go through the AES-GCM encrypted channel (store/secret.go).
const (
	SettingKeyOIDCEnabled            = "oidc.enabled"
	SettingKeyOIDCIssuer             = "oidc.issuer"
	SettingKeyOIDCClientID           = "oidc.client_id"
	SettingKeyOIDCClientSecret       = "oidc.client_secret"
	SettingKeyOIDCScopes             = "oidc.scopes"
	SettingKeyOIDCClaimSubject       = "oidc.claim.subject"
	SettingKeyOIDCClaimEmail         = "oidc.claim.email"
	SettingKeyOIDCClaimName          = "oidc.claim.name"
	SettingKeyOIDCClaimEmailVerified = "oidc.claim.email_verified"
)

// Defaults: the scopes and the claim mapping (claim names are configurable).
const (
	defaultOIDCScopes         = "openid,email,profile"
	defaultClaimSubject       = "sub"
	defaultClaimEmail         = "email"
	defaultClaimName          = "name"
	defaultClaimEmailVerified = "email_verified"
)

// In-memory cap and expiry eviction for the OIDC pending-login table (pending).
//
// Anonymous visitors can repeatedly GET /auth/oidc/start, and only a callback carrying the correct state deletes the corresponding entry:
// without a cap and eviction this map grows without bound, which is a memory DoS path.
const (
	// maxPendingAuth is the maximum number of pending logins; when the cap is reached and there is no expired entry to evict, new starts are rejected.
	maxPendingAuth = 1000
	// pendingEvictEvery is the minimum interval between expired-entry sweeps: a full scan on every start is expensive, so sweeps are throttled to it;
	// when the table is full a sweep is forced immediately, so boundedness does not depend on the throttle having elapsed.
	pendingEvictEvery = 5 * time.Minute
)

// oidcDiscoveryFallbackRedirectURI is used only by the admin panel's "test connection":
// that action only validates the discovery document and starts no login, so the redirect value takes no part in the request.
const oidcDiscoveryFallbackRedirectURI = "http://localhost/auth/oidc/callback"

// OIDCConfig is the effective OIDC configuration. ClientSecret is the decrypted plaintext; it is never logged and never echoed back.
type OIDCConfig struct {
	Enabled            bool
	Issuer             string
	ClientID           string
	ClientSecret       string
	Scopes             []string
	ClaimSubject       string
	ClaimEmail         string
	ClaimName          string
	ClaimEmailVerified string
}

// Usable reports whether the configuration is complete enough to start a login: the switch is on and issuer / client_id are both present.
// The login entry point and related routes are only available when Usable is true (no half-open state allowed).
func (c *OIDCConfig) Usable() bool {
	return c != nil && c.Enabled && strings.TrimSpace(c.Issuer) != "" && strings.TrimSpace(c.ClientID) != ""
}

// LoadOIDCConfig reads the OIDC configuration from the settings table; client_secret is decrypted via SecretCodec.
// When codec is nil the secret read is skipped (tests, or setups where the encryption channel is not wired in).
func LoadOIDCConfig(ctx context.Context, db *gorm.DB, codec *store.SecretCodec) (*OIDCConfig, error) {
	if db == nil {
		return nil, errors.New("auth: database is required to load oidc settings")
	}
	settings, err := store.LoadSettings(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("load oidc settings: %w", err)
	}
	cfg := &OIDCConfig{
		Enabled:            parseOIDCBool(settings[SettingKeyOIDCEnabled]),
		Issuer:             strings.TrimRight(strings.TrimSpace(settings[SettingKeyOIDCIssuer]), "/"),
		ClientID:           strings.TrimSpace(settings[SettingKeyOIDCClientID]),
		Scopes:             splitOIDCScopes(settings[SettingKeyOIDCScopes]),
		ClaimSubject:       orDefault(settings[SettingKeyOIDCClaimSubject], defaultClaimSubject),
		ClaimEmail:         orDefault(settings[SettingKeyOIDCClaimEmail], defaultClaimEmail),
		ClaimName:          orDefault(settings[SettingKeyOIDCClaimName], defaultClaimName),
		ClaimEmailVerified: orDefault(settings[SettingKeyOIDCClaimEmailVerified], defaultClaimEmailVerified),
	}
	if codec != nil {
		secret, ok, err := store.GetSecret(ctx, db, codec, SettingKeyOIDCClientSecret)
		if err != nil {
			return nil, fmt.Errorf("load oidc client secret: %w", err)
		}
		if ok {
			cfg.ClientSecret = secret
		}
	}
	return cfg, nil
}

// parseOIDCBool parses the switch; only true/1/yes/on are accepted, everything else is false (off by default).
func parseOIDCBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "1", "yes", "on":
		return true
	default:
		return false
	}
}

// splitOIDCScopes splits a comma / whitespace separated scope list; an empty value falls back to the default scopes.
func splitOIDCScopes(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
	if len(fields) == 0 {
		fields = strings.Split(defaultOIDCScopes, ",")
	}
	return fields
}

// orDefault returns the trimmed value, falling back to the default when it is empty.
func orDefault(raw, def string) string {
	if v := strings.TrimSpace(raw); v != "" {
		return v
	}
	return def
}

// Pending-intent values. An empty Intent on a pending entry is treated as a login, so entries written before this field existed keep working.
const (
	PendingIntentLogin = "login"
	PendingIntentBind  = "bind"
)

// PendingAuth is the temporary state saved when a login or an identity bind is started, retrieved by state at callback time.
// Nonce / Verifier are sensitive values that live only in memory and go neither into a cookie nor into the database.
// Intent selects what the callback does; UserID names the caller on a bind and is zero for a login.
type PendingAuth struct {
	Nonce       string
	Verifier    string
	RedirectURI string
	ExpiresAt   time.Time
	Intent      string
	UserID      uint64
	// SessionID binds a pending bind to the browser session that started it. A user can have several sessions, and completing the flow
	// under a different one must not attach an identity to the account. It is empty for a login.
	SessionID string
}

// cachedRelyingParty is a TTL'd cache entry for the library client; issuer is used for invalidation per issuer.
type cachedRelyingParty struct {
	issuer    string
	party     rp.RelyingParty
	fetchedAt time.Time
}

// OIDCClient is a concurrency-safe OIDC client: a cache of library RPs plus the state table of pending logins.
// Discovery document parsing and ID Token validation are both handled by rp.RelyingParty; this type only does caching and one-time state.
type OIDCClient struct {
	http *http.Client
	now  func() time.Time
	ttl  time.Duration

	mu      sync.Mutex
	cache   map[string]cachedRelyingParty
	pending map[string]PendingAuth
	// lastPendingEvict is the moment of the last expired-entry eviction (guarded by mu together with pending), used to throttle sweeps.
	lastPendingEvict time.Time
}

// NewOIDCClient constructs the client; when httpClient is nil a default client with a timeout is used (fetching the discovery document must not wait forever).
func NewOIDCClient(httpClient *http.Client) *OIDCClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &OIDCClient{
		http:    httpClient,
		now:     func() time.Time { return time.Now().UTC() },
		ttl:     time.Hour,
		cache:   make(map[string]cachedRelyingParty),
		pending: make(map[string]PendingAuth),
	}
}

// oidcCacheKey concatenates the inputs that affect construction of the library RP into a cache key.
func oidcCacheKey(issuer, clientID, redirectURI string) string {
	return issuer + "\x00" + clientID + "\x00" + redirectURI
}

// Client returns the library RP for the configuration (with TTL caching). redirectURI must match the actual callback,
// because it is written into the authorization URL and the token request. It is invalidated by Invalidate after a configuration change.
func (c *OIDCClient) Client(ctx context.Context, cfg *OIDCConfig, redirectURI string) (rp.RelyingParty, error) {
	if cfg == nil || strings.TrimSpace(cfg.Issuer) == "" {
		return nil, errors.New("oidc: issuer is empty")
	}
	key := oidcCacheKey(cfg.Issuer, cfg.ClientID, redirectURI)
	c.mu.Lock()
	if entry, ok := c.cache[key]; ok && c.now().Sub(entry.fetchedAt) < c.ttl {
		party := entry.party
		c.mu.Unlock()
		return party, nil
	}
	c.mu.Unlock()

	party, err := c.build(ctx, cfg, redirectURI)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.cache[key] = cachedRelyingParty{issuer: cfg.Issuer, party: party, fetchedAt: c.now()}
	c.mu.Unlock()
	return party, nil
}

// Discover only validates the discovery document and does not cache: the admin panel's "test connection" must really reach the provider every time,
// and on failure the returned error contains the provider's status code and a snippet of the response body, for the page to display as is.
func (c *OIDCClient) Discover(ctx context.Context, issuer string) (rp.RelyingParty, error) {
	issuer = strings.TrimRight(strings.TrimSpace(issuer), "/")
	if issuer == "" {
		return nil, errors.New("oidc: issuer is empty")
	}
	return c.build(ctx, &OIDCConfig{Issuer: issuer, Scopes: splitOIDCScopes("")}, oidcDiscoveryFallbackRedirectURI)
}

// build constructs an RP with the library; the discovery document is fetched here, and the library resolves the endpoints and JWKS location.
func (c *OIDCClient) build(ctx context.Context, cfg *OIDCConfig, redirectURI string) (rp.RelyingParty, error) {
	party, err := rp.NewRelyingPartyOIDC(
		ctx, cfg.Issuer, cfg.ClientID, cfg.ClientSecret, redirectURI, cfg.Scopes,
		rp.WithHTTPClient(c.http),
		// the nonce differs per login and cannot be pinned on a cached RP, so the library's built-in nonce hook is turned off here,
		// and ExchangeCode validates this login's nonce with oidc.CheckNonce after exchanging the code for tokens.
		rp.WithVerifierOpts(rp.WithNonce(nil)),
	)
	if err != nil {
		return nil, fmt.Errorf("oidc: discovery for issuer %s failed: %w", cfg.Issuer, err)
	}
	return party, nil
}

// Invalidate drops all cached library RPs for one issuer; call it after a configuration change.
func (c *OIDCClient) Invalidate(issuer string) {
	issuer = strings.TrimRight(strings.TrimSpace(issuer), "/")
	c.mu.Lock()
	for key, entry := range c.cache {
		if entry.issuer == issuer {
			delete(c.cache, key)
		}
	}
	c.mu.Unlock()
}

// PutPending saves the temporary state of a started login, indexed by state, and rejects new starts once the limit is exceeded.
// Returning false means the state table is full and there is no expired entry to evict, so the caller must abort this start.
func (c *OIDCClient) PutPending(state string, p PendingAuth) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	// When the table is full, evict expired entries first (regardless of whether the throttle has elapsed); otherwise sweep occasionally at the throttle interval.
	if len(c.pending) >= maxPendingAuth || now.Sub(c.lastPendingEvict) >= pendingEvictEvery {
		c.evictExpiredPendingLocked(now)
	}
	if len(c.pending) >= maxPendingAuth {
		return false
	}
	c.pending[state] = p
	return true
}

// evictExpiredPendingLocked deletes every expired pending login; the caller must hold c.mu.
func (c *OIDCClient) evictExpiredPendingLocked(now time.Time) {
	for state, p := range c.pending {
		if now.After(p.ExpiresAt) {
			delete(c.pending, state)
		}
	}
	c.lastPendingEvict = now
}

// TakePending retrieves and deletes the temporary state for a state; ok=false when it is unknown or expired.
// Deletion makes it one-time: replaying the same state is rejected.
func (c *OIDCClient) TakePending(state string) (PendingAuth, bool) {
	c.mu.Lock()
	p, ok := c.pending[state]
	if ok {
		delete(c.pending, state)
	}
	c.mu.Unlock()
	if !ok || c.now().After(p.ExpiresAt) {
		return PendingAuth{}, false
	}
	return p, true
}

// PKCEChallenge is the pair of values for Authorization Code + PKCE (RFC 7636 S256).
type PKCEChallenge struct {
	Verifier  string
	Challenge string
}

// NewPKCE generates a code_verifier and an S256 code_challenge.
// The challenge is computed by the OIDC library's oidc.NewSHACodeChallenge; we do not implement the hash encoding ourselves.
func NewPKCE() (PKCEChallenge, error) {
	verifier, err := randomURLSafe(32)
	if err != nil {
		return PKCEChallenge{}, err
	}
	return PKCEChallenge{Verifier: verifier, Challenge: oidc.NewSHACodeChallenge(verifier)}, nil
}

// NewState generates the random string used for state / nonce (32 bytes, URL-safe encoding).
func NewState() (string, error) { return randomURLSafe(32) }

// randomURLSafe returns the base64url encoding of n random bytes.
func randomURLSafe(n int) (string, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", fmt.Errorf("oidc: generate random value: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// BuildAuthURL builds the authorization URL with the library: response_type=code, PKCE, state and nonce are all included.
func BuildAuthURL(party rp.RelyingParty, state, nonce, challenge string) string {
	opts := []rp.AuthURLOpt{rp.WithCodeChallenge(challenge)}
	if nonce != "" {
		opts = append(opts, rp.AuthURLOpt(rp.WithURLParam("nonce", nonce)))
	}
	return rp.AuthURL(state, party, opts...)
}

// ExchangeCode exchanges the authorization code for tokens and lets the library validate the ID Token:
// signature (against the JWKS), iss, aud, exp and iat are all done inside rp.CodeExchange.
// This project then additionally validates the one-time nonce and that subject is non-empty; failing any of these returns an error.
func (c *OIDCClient) ExchangeCode(ctx context.Context, party rp.RelyingParty, code, verifier, expectedNonce string) (*oidc.IDTokenClaims, error) {
	tokens, err := rp.CodeExchange[*oidc.IDTokenClaims](ctx, code, party, rp.WithCodeVerifier(verifier))
	if err != nil {
		return nil, fmt.Errorf("oidc: token exchange failed: %w", err)
	}
	claims := tokens.IDTokenClaims
	if claims == nil {
		return nil, errors.New("oidc: token response has no id_token")
	}
	if expectedNonce != "" {
		if err := oidc.CheckNonce(claims, expectedNonce); err != nil {
			return nil, fmt.Errorf("oidc: %w", err)
		}
	}
	if claims.GetSubject() == "" {
		return nil, errors.New("oidc: id_token has no subject")
	}
	return claims, nil
}

// ProfileFromClaims converts ID Token claims into the input of the linking flow according to the configured claim mapping.
func (cfg *OIDCConfig) ProfileFromClaims(issuer string, raw map[string]any) OAuthProfile {
	return OAuthProfile{
		Provider:      issuer,
		Subject:       stringClaimValue(raw, cfg.ClaimSubject),
		Email:         stringClaimValue(raw, cfg.ClaimEmail),
		EmailVerified: boolClaimValue(raw, cfg.ClaimEmailVerified),
		Name:          stringClaimValue(raw, cfg.ClaimName),
	}
}

func stringClaimValue(raw map[string]any, key string) string {
	if key == "" {
		return ""
	}
	if v, ok := raw[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func boolClaimValue(raw map[string]any, key string) bool {
	if key == "" {
		return false
	}
	if v, ok := raw[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}
