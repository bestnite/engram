package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"example.com/flashcard/internal/store"
)

// OIDC 客户端（DESIGN.md §4.4、§4.5；AGENTS.md §5 M1-11）。
//
// 本文件只做标准 OIDC 协议交互：发现文档（带缓存）、Authorization Code + PKCE、
// state / nonce 校验、ID Token 签名校验。身份绑定一律交给 IdentityLinkService（oauthlink.go），
// 这里不重复实现任何绑定分支。
//
// 说明：项目设计选型是 github.com/zitadel/oidc/v3（DESIGN.md §10.1），但当前工作树
// （feat/m1-oidc 基于 main）的 go.mod 里并没有这个依赖，模块缓存只有 v3.47.7。为了不在
// 依赖树上引入未经验证的改动、也为了让 stub provider 测试完全自包含，这里用标准库实现
// 了同一套流程。协议行为与选型无关，后续若接入 zitadel/oidc，只需替换本文件的实现。

// OIDC 设置的 settings 键（DESIGN.md §8.4）。client_secret 后缀是 `_secret`，
// 因此 store.IsSensitiveSettingKey 会要求它走 AES-GCM 加密通道（store/secret.go）。
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

// 默认值：scope 与 claim 映射（DESIGN.md §4.4 的 claim 名可配）。
const (
	defaultOIDCScopes         = "openid,email,profile"
	defaultClaimSubject       = "sub"
	defaultClaimEmail         = "email"
	defaultClaimName          = "name"
	defaultClaimEmailVerified = "email_verified"
)

// ErrOIDCDisabled 表示 OIDC 未启用或配置不完整；transport 层据此把相关路由置为不可用。
var ErrOIDCDisabled = errors.New("oidc is not configured")

// OIDCConfig 是 OIDC 的生效配置。ClientSecret 是解密后的明文，绝不写日志、绝不回显。
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

// Usable 报告配置是否完整到可以发起登录：开关打开且 issuer / client_id 齐备。
// 只有 Usable 为真时登录入口与相关路由才可用（不允许半开）。
func (c *OIDCConfig) Usable() bool {
	return c != nil && c.Enabled && strings.TrimSpace(c.Issuer) != "" && strings.TrimSpace(c.ClientID) != ""
}

// LoadOIDCConfig 从 settings 表读取 OIDC 配置；client_secret 经 SecretCodec 解密。
// codec 为空时跳过密钥读取（测试或未装配加密通道的场景）。
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

// parseOIDCBool 解析开关；只认 true/1/yes/on，其余一律 false（默认关闭）。
func parseOIDCBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "1", "yes", "on":
		return true
	default:
		return false
	}
}

// splitOIDCScopes 把逗号 / 空白分隔的 scope 列表拆开；空值回落到默认 scope。
func splitOIDCScopes(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
	if len(fields) == 0 {
		fields = strings.Split(defaultOIDCScopes, ",")
	}
	return fields
}

// orDefault 返回去空白后的值；为空时回落到默认值。
func orDefault(raw, def string) string {
	if v := strings.TrimSpace(raw); v != "" {
		return v
	}
	return def
}

// oidcDiscovery 是发现文档里本项目用到的字段。
type oidcDiscovery struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
}

// discoveryCache 是带时间戳的发现文档缓存项。
type discoveryCache struct {
	doc       oidcDiscovery
	fetchedAt time.Time
}

// PendingAuth 是一次登录发起时保存的临时状态，回调时按 state 取回。
// Nonce / Verifier 是敏感值，只存在于内存，不落 cookie、不落库。
type PendingAuth struct {
	Nonce       string
	Verifier    string
	RedirectURI string
	ExpiresAt   time.Time
}

// OIDCClient 是并发安全的 OIDC 协议客户端：发现文档缓存 + 待完成登录的 state 表。
type OIDCClient struct {
	http *http.Client
	now  func() time.Time
	ttl  time.Duration

	mu      sync.Mutex
	cache   map[string]discoveryCache
	pending map[string]PendingAuth
}

// NewOIDCClient 构造客户端；httpClient 为空时用带超时的默认客户端（发现文档拉取不能无限等待）。
func NewOIDCClient(httpClient *http.Client) *OIDCClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &OIDCClient{
		http:    httpClient,
		now:     func() time.Time { return time.Now().UTC() },
		ttl:     time.Hour,
		cache:   make(map[string]discoveryCache),
		pending: make(map[string]PendingAuth),
	}
}

// Discover 拉取并缓存 issuer 的发现文档。缓存过期或显式失效后重新拉取；
// 失败时返回的错误包含 provider 的状态码与响应体片段，供管理面板「测试连接」原样显示。
func (c *OIDCClient) Discover(ctx context.Context, issuer string) (*oidcDiscovery, error) {
	return c.discover(ctx, issuer)
}

// ---- 发现文档 ----

func (c *OIDCClient) discover(ctx context.Context, issuer string) (*oidcDiscovery, error) {
	key := strings.TrimRight(strings.TrimSpace(issuer), "/")
	if key == "" {
		return nil, errors.New("oidc: issuer is empty")
	}
	c.mu.Lock()
	if entry, ok := c.cache[key]; ok && c.now().Sub(entry.fetchedAt) < c.ttl {
		doc := entry.doc
		c.mu.Unlock()
		return &doc, nil
	}
	c.mu.Unlock()

	wellKnown := key + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wellKnown, nil)
	if err != nil {
		return nil, fmt.Errorf("oidc: build discovery request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oidc: discovery request to %s failed: %w", wellKnown, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oidc: discovery endpoint returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var doc oidcDiscovery
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("oidc: discovery document is not valid JSON: %w (body: %s)", err, strings.TrimSpace(string(body)))
	}
	if doc.AuthorizationEndpoint == "" || doc.TokenEndpoint == "" || doc.JWKSURI == "" {
		return nil, fmt.Errorf("oidc: discovery document is missing required endpoints")
	}
	c.mu.Lock()
	c.cache[key] = discoveryCache{doc: doc, fetchedAt: c.now()}
	c.mu.Unlock()
	return &doc, nil
}

// Invalidate 丢弃某个 issuer 的发现文档缓存；配置变更后调用（DESIGN.md §4.4）。
func (c *OIDCClient) Invalidate(issuer string) {
	key := strings.TrimRight(strings.TrimSpace(issuer), "/")
	c.mu.Lock()
	delete(c.cache, key)
	c.mu.Unlock()
}

// PutPending 保存一次登录发起时的临时状态，按 state 索引。
func (c *OIDCClient) PutPending(state string, p PendingAuth) {
	c.mu.Lock()
	c.pending[state] = p
	c.mu.Unlock()
}

// TakePending 取出并删除 state 对应的临时状态；未知或已过期返回 ok=false。
// 删除即一次性：同一个 state 重放会被拒。
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

// PKCEChallenge 是 Authorization Code + PKCE 的一对值（RFC 7636 S256）。
type PKCEChallenge struct {
	Verifier  string
	Challenge string
}

// NewPKCE 生成 code_verifier 与 S256 code_challenge。
func NewPKCE() (PKCEChallenge, error) {
	verifier, err := randomURLSafe(32)
	if err != nil {
		return PKCEChallenge{}, err
	}
	sum := sha256.Sum256([]byte(verifier))
	return PKCEChallenge{Verifier: verifier, Challenge: base64.RawURLEncoding.EncodeToString(sum[:])}, nil
}

// NewState 生成 state / nonce 用的随机串（32 字节 URL 安全编码）。
func NewState() (string, error) { return randomURLSafe(32) }

// randomURLSafe 生成 n 字节随机数的 base64url 编码。
func randomURLSafe(n int) (string, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", fmt.Errorf("oidc: generate random value: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// BuildAuthURL 拼授权端点 URL：response_type=code、PKCE、state、nonce 全部带上。
func BuildAuthURL(doc *oidcDiscovery, cfg *OIDCConfig, state, nonce, challenge, redirectURI string) (string, error) {
	base, err := url.Parse(doc.AuthorizationEndpoint)
	if err != nil {
		return "", fmt.Errorf("oidc: invalid authorization endpoint: %w", err)
	}
	q := base.Query()
	q.Set("response_type", "code")
	q.Set("client_id", cfg.ClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", strings.Join(cfg.Scopes, " "))
	q.Set("state", state)
	q.Set("nonce", nonce)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	base.RawQuery = q.Encode()
	return base.String(), nil
}

// ExchangeCode 用授权码换取 token，返回 ID Token 原文。
// 失败时把 provider 返回的 error / error_description 原样带进错误文本
// （管理面板与日志都能看到真实原因，而不是笼统的「登录失败」）。
func (c *OIDCClient) ExchangeCode(ctx context.Context, doc *oidcDiscovery, cfg *OIDCConfig, code, verifier, redirectURI string) (string, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", cfg.ClientID)
	form.Set("code_verifier", verifier)
	if cfg.ClientSecret != "" {
		form.Set("client_secret", cfg.ClientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, doc.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("oidc: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("oidc: token request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	var tr struct {
		IDToken          string `json:"id_token"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", fmt.Errorf("oidc: token response is not valid JSON (status %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if tr.Error != "" {
		return "", fmt.Errorf("oidc: token endpoint error %q: %s", tr.Error, tr.ErrorDescription)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("oidc: token endpoint returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if tr.IDToken == "" {
		return "", errors.New("oidc: token response has no id_token")
	}
	return tr.IDToken, nil
}

// IDTokenClaims 是校验通过后的 ID Token 声明：结构化字段 + 原始 map（供 claim 映射取任意 claim）。
type IDTokenClaims struct {
	Issuer    string
	Subject   string
	Audience  []string
	ExpiresAt time.Time
	Nonce     string
	Raw       map[string]any
}

// VerifyIDToken 校验 ID Token：RS256 签名（对 JWKS）、iss、aud、exp 与 nonce。
// 任何一项不通过都返回错误，绝不放行未校验的 token。
func (c *OIDCClient) VerifyIDToken(ctx context.Context, doc *oidcDiscovery, cfg *OIDCConfig, idToken, expectedNonce string) (*IDTokenClaims, error) {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return nil, errors.New("oidc: id_token is not a compact JWS")
	}
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("oidc: decode id_token header: %w", err)
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return nil, fmt.Errorf("oidc: parse id_token header: %w", err)
	}
	// 只接受 RS256：拒绝 none / HS256（HS256 会用公钥当对称密钥，是经典混淆攻击）。
	if header.Alg != "RS256" {
		return nil, fmt.Errorf("oidc: unsupported id_token signing algorithm %q (only RS256 is accepted)", header.Alg)
	}

	keys, err := c.fetchJWKS(ctx, doc.JWKSURI)
	if err != nil {
		return nil, err
	}
	pub, err := selectRSAKey(keys, header.Kid)
	if err != nil {
		return nil, err
	}

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("oidc: decode id_token signature: %w", err)
	}
	signed := []byte(parts[0] + "." + parts[1])
	digest := sha256.Sum256(signed)
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig); err != nil {
		return nil, fmt.Errorf("oidc: id_token signature verification failed: %w", err)
	}

	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("oidc: decode id_token payload: %w", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(payloadJSON, &raw); err != nil {
		return nil, fmt.Errorf("oidc: parse id_token payload: %w", err)
	}
	claims := &IDTokenClaims{
		Issuer:  stringClaimValue(raw, "iss"),
		Subject: stringClaimValue(raw, "sub"),
		Nonce:   stringClaimValue(raw, "nonce"),
		Raw:     raw,
	}
	claims.Audience = audienceClaim(raw)
	if exp, ok := numericClaimValue(raw, "exp"); ok {
		claims.ExpiresAt = time.Unix(int64(exp), 0).UTC()
	}

	if claims.Issuer != doc.Issuer && claims.Issuer != cfg.Issuer {
		return nil, fmt.Errorf("oidc: id_token issuer %q does not match %q", claims.Issuer, doc.Issuer)
	}
	if !audienceContains(claims.Audience, cfg.ClientID) {
		return nil, fmt.Errorf("oidc: id_token audience %v does not include client_id", claims.Audience)
	}
	if claims.ExpiresAt.IsZero() || c.now().After(claims.ExpiresAt) {
		return nil, errors.New("oidc: id_token is expired or has no exp")
	}
	if expectedNonce != "" && claims.Nonce != expectedNonce {
		return nil, errors.New("oidc: id_token nonce does not match the login request")
	}
	if claims.Subject == "" {
		return nil, errors.New("oidc: id_token has no subject")
	}
	return claims, nil
}

// jwks / jwk 是 JSON Web Key Set 的解析结构（只处理 RSA 公钥）。
type jwks struct {
	Keys []jwk `json:"keys"`
}

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
}

func (c *OIDCClient) fetchJWKS(ctx context.Context, jwksURI string) (*jwks, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURI, nil)
	if err != nil {
		return nil, fmt.Errorf("oidc: build jwks request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oidc: jwks request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oidc: jwks endpoint returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var set jwks
	if err := json.Unmarshal(body, &set); err != nil {
		return nil, fmt.Errorf("oidc: jwks document is not valid JSON: %w", err)
	}
	return &set, nil
}

// selectRSAKey 按 kid 选公钥；kid 为空时退化为第一把 RSA 公钥。
func selectRSAKey(set *jwks, kid string) (*rsa.PublicKey, error) {
	var candidates []jwk
	for _, k := range set.Keys {
		if !strings.EqualFold(k.Kty, "RSA") {
			continue
		}
		if kid == "" || k.Kid == kid {
			candidates = append(candidates, k)
		}
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("oidc: no matching RSA key in jwks (kid=%q)", kid)
	}
	return rsaKeyFromJWK(candidates[0])
}

func rsaKeyFromJWK(k jwk) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, fmt.Errorf("oidc: decode jwk modulus: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, fmt.Errorf("oidc: decode jwk exponent: %w", err)
	}
	e := 0
	for _, b := range eBytes {
		e = e<<8 | int(b)
	}
	if e == 0 {
		return nil, errors.New("oidc: jwk exponent is zero")
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: e}, nil
}

// ProfileFromClaims 按配置的 claim 映射把 ID Token 声明转成绑定流程的输入。
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

func numericClaimValue(raw map[string]any, key string) (float64, bool) {
	if v, ok := raw[key]; ok {
		if f, ok := v.(float64); ok {
			return f, true
		}
	}
	return 0, false
}

func audienceClaim(raw map[string]any) []string {
	switch v := raw["aud"].(type) {
	case string:
		return []string{v}
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func audienceContains(aud []string, want string) bool {
	for _, a := range aud {
		if a == want {
			return true
		}
	}
	return false
}
