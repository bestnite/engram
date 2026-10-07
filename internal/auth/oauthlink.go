package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// 身份绑定决策的稳定错误值；transport 层据此映射到稳定英文 code 与本地化文案
// （bootstrap 文案“请联系管理员绑定账号”属于 M1-11 的页面职责，本包不产出用户可见文字）。
var (
	// ErrIdentityLinkDenied 表示 closed 策略下既无既有绑定、又无法自动匹配，只能由管理员手工绑定。
	ErrIdentityLinkDenied = errors.New("identity link denied by registration policy")
)

// oidcPlaceholderEmailSuffix 是无 email claim 时给 OIDC 账号合成的占位邮箱后缀。
// localhost 是仓库允许的占位值，绝不写真实域名；
// 合成邮箱保证 users.email 的 NOT NULL + UNIQUE 约束成立，且不暴露任何外部主机名。
const oidcPlaceholderEmailSuffix = "@localhost"

// OAuthProfile 是绑定决策所需的、来自 IdP 的规范化输入。
// Email 由调用方按需提供原始 claim 值；本包只做大小写与空白归一化。
type OAuthProfile struct {
	Provider      string
	Subject       string
	Email         string
	EmailVerified bool
	// Name 是 IdP 返回的显示名（claim 名可配）。仅用于自动建号时的
	// 展示名；绑定决策只看 Subject 与 Email，所以它不影响任何分支判定。
	Name string
}

// LinkDecisionKind 是身份绑定分支的标识，取值稳定用于审计与测试断言。
type LinkDecisionKind string

const (
	// LinkDecisionExisting 命中绑定情形 1：(provider, subject) 已有绑定，直接登录对应内部用户。
	LinkDecisionExisting LinkDecisionKind = "existing"
	// LinkDecisionAutoLink 命中绑定情形 2：邮箱已验证且与某个内部用户相同，自动绑定。
	LinkDecisionAutoLink LinkDecisionKind = "auto_link"
	// LinkDecisionCreateUser 命中绑定情形 3：open / invite 策略下自动建号并绑定。
	LinkDecisionCreateUser LinkDecisionKind = "create_user"
	// LinkDecisionDeny 命中绑定情形 3：closed 策略下拒绝登录，等待管理员绑定。
	LinkDecisionDeny LinkDecisionKind = "deny"
)

// LinkDecision 是纯决策函数的输出：命中哪个分支，以及 existing / auto_link 时的目标用户。
type LinkDecision struct {
	Kind   LinkDecisionKind
	UserID uint64
	// Err 仅在 Kind == LinkDecisionDeny 时非 nil。
	Err error
}

// DecideIdentityLink 是身份自动绑定三个分支的纯决策函数：不触库、不依赖 OIDC 协议。
//
// 输入覆盖四种情形（existing = sub 已存在，matched = 邮箱命中的内部用户）：
//  1. existing 非空 → 直接登录，忽略邮箱；（sub 已存在）
//  2. EmailVerified 且 Email 非空且 matched 非空 → 自动绑定；（已验邮箱）
//  3. 其余 → 按注册策略：open / invite 建号，closed 拒绝。（未验邮箱 / 无邮箱）
//
// matched 必须与 profile.Email 同址，否则视为未命中：这保证“只有邮箱真的对得上才自动绑定”。
// 未验证邮箱即便撞上既有账号也绝不自动绑定（防止仅凭可控 email claim 接管账号）。
func DecideIdentityLink(profile OAuthProfile, existing *store.Identity, matched *store.User, policy string) LinkDecision {
	if existing != nil {
		return LinkDecision{Kind: LinkDecisionExisting, UserID: existing.UserID}
	}
	email := normalizeEmail(profile.Email)
	if profile.EmailVerified && email != "" && matched != nil && strings.EqualFold(matched.Email, email) {
		return LinkDecision{Kind: LinkDecisionAutoLink, UserID: matched.ID}
	}
	// 邮箱已被既有账号占用却走到这里，只可能是“未验证邮箱撞名”：建号必然违反唯一约束。
	// 宁可拒绝，也不把新身份悄悄挂到别人的账号上。
	if matched != nil {
		return LinkDecision{Kind: LinkDecisionDeny, Err: ErrIdentityLinkDenied}
	}
	switch ParseRegistrationPolicy(policy) {
	case PolicyOpen, PolicyInvite:
		return LinkDecision{Kind: LinkDecisionCreateUser}
	default:
		return LinkDecision{Kind: LinkDecisionDeny, Err: ErrIdentityLinkDenied}
	}
}

// normalizeEmail 归一化邮箱：小写 + 去首尾空白。空串保持空串，表示 IdP 没有返回邮箱。
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// IdentityLinkStore 是绑定流程所需的存储能力，定义在消费者侧（AGENTS.md §2.4）。
// 生产实现见 newStoreIdentityLinkStore；测试可注入内存替身，因此决策与流程无需真 OIDC。
type IdentityLinkStore interface {
	// FindIdentity 按 (provider, subject) 查绑定；未绑定返回 (nil, nil)。
	FindIdentity(ctx context.Context, provider, subject string) (*store.Identity, error)
	// FindUserByID 按主键查内部用户；不存在返回 (nil, nil)。
	FindUserByID(ctx context.Context, id uint64) (*store.User, error)
	// FindUserByEmail 按邮箱查内部用户；不存在返回 (nil, nil)。
	FindUserByEmail(ctx context.Context, email string) (*store.User, error)
	// LinkIdentity 写入一条绑定。
	LinkIdentity(ctx context.Context, ident *store.Identity) error
	// CreateOIDCUser 创建一个无本地密码的内部账号。
	CreateOIDCUser(ctx context.Context, in CreateUserInput) (*store.User, error)
}

// IdentityLinkService 串联身份绑定的完整流程：查绑定 → 自动绑定 → 按策略建号。
// 它不实现 OIDC 协议交互（M1-11）；调用方只需把规范化后的 OAuthProfile 交给 Resolve。
type IdentityLinkService struct {
	store  IdentityLinkStore
	audit  *Auditor
	logger *slog.Logger
	now    func() time.Time
}

// NewIdentityLinkService 构造绑定服务。store 必填；audit 可为空（不记审计）；
// logger 为空时用 slog.Default()，审计写失败只记英文日志、不影响已经完成的绑定（M1-10 原则）。
func NewIdentityLinkService(st IdentityLinkStore, audit *Auditor, logger *slog.Logger) (*IdentityLinkService, error) {
	if st == nil {
		return nil, errors.New("auth: identity link store is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &IdentityLinkService{store: st, audit: audit, logger: logger,
		now: func() time.Time { return time.Now().UTC() }}, nil
}

// Resolve 执行一次 OIDC 首次登录的绑定判定，返回应登录的内部用户。
// 第二个返回值是命中的分支（供调用方记日志与审计），第三个是错误。
func (s *IdentityLinkService) Resolve(ctx context.Context, profile OAuthProfile, policy string) (*store.User, LinkDecision, error) {
	if strings.TrimSpace(profile.Provider) == "" || strings.TrimSpace(profile.Subject) == "" {
		return nil, LinkDecision{}, errors.New("auth: provider and subject are required")
	}

	existing, err := s.store.FindIdentity(ctx, profile.Provider, profile.Subject)
	if err != nil {
		return nil, LinkDecision{}, fmt.Errorf("find identity: %w", err)
	}

	// 只有 IdP 给了邮箱才查同名账号；查到的 matched 无论验证与否都交给纯函数，
	// 由纯函数决定它是否足以触发自动绑定或必须拒绝建号。
	email := normalizeEmail(profile.Email)
	var matched *store.User
	if email != "" {
		matched, err = s.store.FindUserByEmail(ctx, email)
		if err != nil {
			return nil, LinkDecision{}, fmt.Errorf("find user by email: %w", err)
		}
	}

	decision := DecideIdentityLink(profile, existing, matched, policy)
	switch decision.Kind {
	case LinkDecisionExisting:
		u, err := s.store.FindUserByID(ctx, decision.UserID)
		if err != nil {
			return nil, decision, fmt.Errorf("find user: %w", err)
		}
		if u == nil {
			return nil, decision, fmt.Errorf("identity references a missing user: id=%d", decision.UserID)
		}
		if u.Status != store.StatusActive {
			return nil, decision, ErrUserDisabled
		}
		return u, decision, nil

	case LinkDecisionAutoLink:
		if matched == nil || matched.Status != store.StatusActive {
			// 绑定目标已禁用：不建立新绑定，也不放行登录。
			return nil, decision, ErrUserDisabled
		}
		if err := s.link(ctx, profile, matched.ID, email); err != nil {
			return nil, decision, err
		}
		return matched, decision, nil

	case LinkDecisionCreateUser:
		created, err := s.store.CreateOIDCUser(ctx, CreateUserInput{
			Username:    oidcUsername(profile),
			Email:       oidcAccountEmail(profile),
			DisplayName: oidcDisplayName(profile),
		})
		if err != nil {
			return nil, decision, fmt.Errorf("create oidc user: %w", err)
		}
		if err := s.link(ctx, profile, created.ID, email); err != nil {
			return nil, decision, err
		}
		return created, decision, nil

	default:
		return nil, decision, decision.Err
	}
}

// link 写入一条 identities 并记审计；审计失败只记英文日志，不回滚绑定（M1-10 原则）。
func (s *IdentityLinkService) link(ctx context.Context, profile OAuthProfile, userID uint64, email string) error {
	ident := &store.Identity{
		UserID:   userID,
		Provider: profile.Provider,
		Subject:  profile.Subject,
		LinkedAt: s.now(),
	}
	if email != "" {
		ident.Email = &email
	}
	if err := s.store.LinkIdentity(ctx, ident); err != nil {
		return fmt.Errorf("link identity: %w", err)
	}
	if s.audit == nil {
		return nil
	}
	err := s.audit.Record(ctx, store.AuditEntry{
		UserID:     store.Ptr(userID),
		Action:     store.ActionIdentityLink,
		TargetType: "user",
		TargetID:   store.Ptr(userID),
		Detail: map[string]any{
			"provider":       profile.Provider,
			"subject":        profile.Subject,
			"email_verified": profile.EmailVerified,
		},
	})
	if err != nil {
		s.logger.Error("record identity link audit failed",
			"user_id", userID, "provider", profile.Provider, "error", err)
	}
	return nil
}

// oidcUsername 为自动建号生成确定且唯一的登录名：邮箱 local part（无邮箱时用 oidc）
// 加上 provider+subject 的哈希前缀。哈希保证同一 IdP 身份重复登录时得到同一个名字，
// 也保证两个不同身份不会撞名（users.username 唯一）。
func oidcUsername(profile OAuthProfile) string {
	sum := sha256.Sum256([]byte(profile.Provider + "\x00" + profile.Subject))
	suffix := hex.EncodeToString(sum[:])[:8]
	base := sanitizeUsername(emailLocalPart(profile.Email))
	if base == "" {
		base = "oidc"
	}
	if len(base) > 24 {
		base = base[:24]
	}
	return base + "-" + suffix
}

// oidcAccountEmail 返回落库邮箱：IdP 给了就用它，没给则合成占位邮箱保证唯一约束成立。
func oidcAccountEmail(profile OAuthProfile) string {
	if email := normalizeEmail(profile.Email); email != "" {
		return email
	}
	return oidcUsername(profile) + oidcPlaceholderEmailSuffix
}

// oidcDisplayName 生成显示名：优先 IdP 的 name claim，其次邮箱 local part，最后登录名；不产出用户可见文案。
func oidcDisplayName(profile OAuthProfile) string {
	if name := strings.TrimSpace(profile.Name); name != "" {
		return name
	}
	if local := emailLocalPart(profile.Email); local != "" {
		return local
	}
	return oidcUsername(profile)
}

// emailLocalPart 取 @ 之前的部分；无 @ 或为空时返回空串。
func emailLocalPart(email string) string {
	email = normalizeEmail(email)
	at := strings.IndexByte(email, '@')
	if at <= 0 {
		return ""
	}
	return email[:at]
}

// sanitizeUsername 只保留账户名允许的字符（小写字母、数字、点、下划线、连字符），
// 其余替换为连字符；结果可能为空，由调用方补默认前缀。
func sanitizeUsername(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-.")
}

// NewStoreIdentityLinkStore 把三个具体存储适配成 IdentityLinkStore：把“未找到”归一化成
// (nil, nil)，让上面的决策函数不必理解 GORM 的哨兵错误。生产装配在 cmd/engram。
func NewStoreIdentityLinkStore(identities *store.IdentityStore, users *store.UserStore, accounts *AccountService) IdentityLinkStore {
	return &storeIdentityLinkStore{identities: identities, users: users, accounts: accounts}
}

type storeIdentityLinkStore struct {
	identities *store.IdentityStore
	users      *store.UserStore
	accounts   *AccountService
}

func (a *storeIdentityLinkStore) FindIdentity(ctx context.Context, provider, subject string) (*store.Identity, error) {
	ident, err := a.identities.ByProviderSubject(ctx, provider, subject)
	if errors.Is(err, store.ErrIdentityNotFound) {
		return nil, nil
	}
	return ident, err
}

func (a *storeIdentityLinkStore) FindUserByID(ctx context.Context, id uint64) (*store.User, error) {
	u, err := a.users.ByID(ctx, id)
	if store.IsNotFound(err) {
		return nil, nil
	}
	return u, err
}

func (a *storeIdentityLinkStore) FindUserByEmail(ctx context.Context, email string) (*store.User, error) {
	u, err := a.users.ByEmail(ctx, email)
	if store.IsNotFound(err) {
		return nil, nil
	}
	return u, err
}

func (a *storeIdentityLinkStore) LinkIdentity(ctx context.Context, ident *store.Identity) error {
	return a.identities.Create(ctx, ident)
}

func (a *storeIdentityLinkStore) CreateOIDCUser(ctx context.Context, in CreateUserInput) (*store.User, error) {
	return a.accounts.CreateOIDCUser(ctx, in)
}
