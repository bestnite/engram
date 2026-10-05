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

// OIDC 客户端（DESIGN.md §4.4、§4.5；ROADMAP.md M1-15）。
//
// 协议交互全部交给 github.com/zitadel/oidc/v3：
//   - 发现文档与端点解析：rp.NewRelyingPartyOIDC（本文件只在其上做一层带 TTL 的按 issuer 缓存，
//     配置变更后由 Invalidate 失效）。
//   - Authorization Code + PKCE：challenge 由 OIDC 库的 oidc.NewSHACodeChallenge 计算，
//     授权 URL 与 token 交换由 rp.AuthURL / rp.CodeExchange 完成。
//   - ID Token 校验：rp.CodeExchange 内部走 rp.IDTokenVerifier（签名对 JWKS、iss、aud、exp、iat）。
//
// 本文件只保留本项目必需、库不提供的部分：state/nonce 的一次性表、claim 名映射，
// 以及把校验结果交给 IdentityLinkService（oauthlink.go）做身份绑定。绑定分支不在这里重复实现。

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

// OIDC 待完成登录表（pending）的内存上限与过期淘汰（F17；DESIGN.md §4.4）。
//
// 匿名者可以反复 GET /auth/oidc/start，而只有携带正确 state 的回调才会删除对应项：
// 没有上限与淘汰时这个 map 会被堆大，是一条内存 DoS 路径。
const (
	// maxPendingAuth 是未完成登录的最大条数；达到上限且没有可淘汰的过期项时，拒绝新发起。
	maxPendingAuth = 1000
	// pendingEvictEvery 是过期项清理的最小间隔：每次发起都全量扫表代价高，按此间隔节流；
	// 表满时则强制立即清理一次，使有界性不依赖节流是否到期。
	pendingEvictEvery = 5 * time.Minute
)

// oidcDiscoveryFallbackRedirectURI 只用于管理面板「测试连接」：
// 该动作只校验发现文档，不发起登录，redirect 值不参与请求。
const oidcDiscoveryFallbackRedirectURI = "http://localhost/auth/oidc/callback"

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

// PendingAuth 是一次登录发起时保存的临时状态，回调时按 state 取回。
// Nonce / Verifier 是敏感值，只存在于内存，不落 cookie、不落库。
type PendingAuth struct {
	Nonce       string
	Verifier    string
	RedirectURI string
	ExpiresAt   time.Time
}

// cachedRelyingParty 是带 TTL 的库客户端缓存项；issuer 用于按 issuer 失效。
type cachedRelyingParty struct {
	issuer    string
	party     rp.RelyingParty
	fetchedAt time.Time
}

// OIDCClient 是并发安全的 OIDC 客户端：库 RP 的缓存 + 待完成登录的 state 表。
// 发现文档解析与 ID Token 校验都由 rp.RelyingParty 承担，这里只负责缓存与一次性 state。
type OIDCClient struct {
	http *http.Client
	now  func() time.Time
	ttl  time.Duration

	mu      sync.Mutex
	cache   map[string]cachedRelyingParty
	pending map[string]PendingAuth
	// lastPendingEvict 是上一次淘汰过期项的时刻（与 pending 同受 mu 保护），用于节流扫描。
	lastPendingEvict time.Time
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
		cache:   make(map[string]cachedRelyingParty),
		pending: make(map[string]PendingAuth),
	}
}

// oidcCacheKey 把影响库 RP 构造的输入拼成缓存键。
func oidcCacheKey(issuer, clientID, redirectURI string) string {
	return issuer + "\x00" + clientID + "\x00" + redirectURI
}

// Client 返回配置对应的库 RP（带 TTL 缓存）。redirectURI 必须与实际回调一致，
// 因为它会被写进授权 URL 与 token 请求。配置变更后由 Invalidate 失效。
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

// Discover 只做发现文档校验，不缓存：管理面板「测试连接」每次都要真实访问 provider，
// 失败时返回的错误包含 provider 的状态码与响应体片段，供页面原样显示（DESIGN.md §4.4）。
func (c *OIDCClient) Discover(ctx context.Context, issuer string) (rp.RelyingParty, error) {
	issuer = strings.TrimRight(strings.TrimSpace(issuer), "/")
	if issuer == "" {
		return nil, errors.New("oidc: issuer is empty")
	}
	return c.build(ctx, &OIDCConfig{Issuer: issuer, Scopes: splitOIDCScopes("")}, oidcDiscoveryFallbackRedirectURI)
}

// build 用库构造一个 RP；发现文档在此拉取，端点与 JWKS 位置由库解析。
func (c *OIDCClient) build(ctx context.Context, cfg *OIDCConfig, redirectURI string) (rp.RelyingParty, error) {
	party, err := rp.NewRelyingPartyOIDC(
		ctx, cfg.Issuer, cfg.ClientID, cfg.ClientSecret, redirectURI, cfg.Scopes,
		rp.WithHTTPClient(c.http),
		// nonce 是每次登录不同的值，无法在缓存的 RP 上固定；这里关闭库内置的 nonce 钩子，
		// 由 ExchangeCode 在换取 token 后用 oidc.CheckNonce 校验本次登录的 nonce。
		rp.WithVerifierOpts(rp.WithNonce(nil)),
	)
	if err != nil {
		return nil, fmt.Errorf("oidc: discovery for issuer %s failed: %w", cfg.Issuer, err)
	}
	return party, nil
}

// Invalidate 丢弃某个 issuer 的所有库 RP 缓存；配置变更后调用（DESIGN.md §4.4）。
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

// PutPending 保存一次登录发起时的临时状态，按 state 索引，并在超限时拒绝新发起。
// 返回 false 表示 state 表已满且没有可淘汰的过期项，调用方必须中止本次发起（DESIGN.md §4.4）。
func (c *OIDCClient) PutPending(state string, p PendingAuth) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	// 表满时先强制淘汰过期项（无论节流是否到期），否则按节流间隔偶尔清理一次。
	if len(c.pending) >= maxPendingAuth || now.Sub(c.lastPendingEvict) >= pendingEvictEvery {
		c.evictExpiredPendingLocked(now)
	}
	if len(c.pending) >= maxPendingAuth {
		return false
	}
	c.pending[state] = p
	return true
}

// evictExpiredPendingLocked 删除所有已过期的待完成登录；调用方必须持有 c.mu。
func (c *OIDCClient) evictExpiredPendingLocked(now time.Time) {
	for state, p := range c.pending {
		if now.After(p.ExpiresAt) {
			delete(c.pending, state)
		}
	}
	c.lastPendingEvict = now
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
// challenge 由 OIDC 库的 oidc.NewSHACodeChallenge 计算，不自行实现哈希编码。
func NewPKCE() (PKCEChallenge, error) {
	verifier, err := randomURLSafe(32)
	if err != nil {
		return PKCEChallenge{}, err
	}
	return PKCEChallenge{Verifier: verifier, Challenge: oidc.NewSHACodeChallenge(verifier)}, nil
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

// BuildAuthURL 用库拼授权 URL：response_type=code、PKCE、state、nonce 全部带上。
func BuildAuthURL(party rp.RelyingParty, state, nonce, challenge string) string {
	opts := []rp.AuthURLOpt{rp.WithCodeChallenge(challenge)}
	if nonce != "" {
		opts = append(opts, rp.AuthURLOpt(rp.WithURLParam("nonce", nonce)))
	}
	return rp.AuthURL(state, party, opts...)
}

// ExchangeCode 用授权码换 token 并由库校验 ID Token：
// 签名（对 JWKS）、iss、aud、exp、iat 都在 rp.CodeExchange 内部完成。
// 之后本项目再校验一次性 nonce 与 subject 非空；任何一项不通过都返回错误。
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
