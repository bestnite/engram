package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"example.com/flashcard/internal/store"
)

// 账号服务的稳定错误值；transport 层据此映射到稳定英文 code。
var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUserDisabled       = errors.New("user is disabled")
	ErrPasswordUnchanged  = errors.New("new password must differ from the current one")
)

// CreateUserInput 是创建本地账号的入参；空字段由服务补默认值。
type CreateUserInput struct {
	Username      string
	Email         string
	DisplayName   string
	Password      string
	Role          string
	Locale        string
	Timezone      string
	DayCutoffHour int
}

// AccountService 负责本地账号的核心流程：创建、认证、改密、禁用。
// 它持有 UserStore 与 SessionStore，因此能在改密/禁用时同步作废会话（DESIGN.md §11）。
type AccountService struct {
	users    *store.UserStore
	sessions *store.SessionStore
	hasher   *PasswordHasher
	now      func() time.Time
}

// NewAccountService 构造账号服务；依赖缺一不可。
func NewAccountService(users *store.UserStore, sessions *store.SessionStore, hasher *PasswordHasher) (*AccountService, error) {
	if users == nil || sessions == nil {
		return nil, errors.New("auth: user store and session store are required")
	}
	if hasher == nil {
		hasher = DefaultPasswordHasher()
	}
	return &AccountService{users: users, sessions: sessions, hasher: hasher,
		now: func() time.Time { return time.Now().UTC() }}, nil
}

// CreateLocalUser 校验密码策略、哈希密码并写入用户；邮箱大小写归一化，登录名保持原样。
func (s *AccountService) CreateLocalUser(ctx context.Context, in CreateUserInput) (*store.User, error) {
	if err := ValidatePasswordPolicy(in.Password); err != nil {
		return nil, err
	}
	hash, err := s.hasher.Hash(in.Password)
	if err != nil {
		return nil, err
	}
	u, err := newUserFromInput(in, &hash, s.now())
	if err != nil {
		return nil, err
	}
	if err := s.users.Create(ctx, u); err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	return u, nil
}

// CreateOIDCUser 创建一个纯 OIDC 账号（password_hash = NULL，DESIGN.md §4.1）。
//
// 与 CreateLocalUser 的区别只有凭据：不做密码策略校验，也不派生哈希 —— OIDC 账号靠
// 绑定的外部身份登录（M1-11 接入流程，M1-12 提供建号能力）。用户名与邮箱仍为必填，
// 否则唯一约束与后续找回都会失去依据。
func (s *AccountService) CreateOIDCUser(ctx context.Context, in CreateUserInput) (*store.User, error) {
	u, err := newUserFromInput(in, nil, s.now())
	if err != nil {
		return nil, err
	}
	if err := s.users.Create(ctx, u); err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	return u, nil
}

// newUserFromInput 把入参归一化为待落库的用户；passwordHash 为 nil 表示无本地密码。
// 默认值集中在这里，避免两条建号路径漂移（AGENTS.md §2.6：一个行为只有一处实现）。
func newUserFromInput(in CreateUserInput, passwordHash *string, now time.Time) (*store.User, error) {
	username := strings.TrimSpace(in.Username)
	email := strings.ToLower(strings.TrimSpace(in.Email))
	if username == "" || email == "" {
		return nil, errors.New("username and email are required")
	}
	role := in.Role
	if role == "" {
		role = store.RoleUser
	}
	locale := in.Locale
	if locale == "" {
		locale = "zh-CN"
	}
	tz := in.Timezone
	if tz == "" {
		tz = "Asia/Shanghai"
	}
	cutoff := in.DayCutoffHour
	if cutoff == 0 {
		cutoff = 4
	}
	display := strings.TrimSpace(in.DisplayName)
	if display == "" {
		display = username
	}
	return &store.User{
		Username:      username,
		Email:         email,
		PasswordHash:  passwordHash,
		DisplayName:   display,
		Role:          role,
		Status:        store.StatusActive,
		Locale:        locale,
		Timezone:      tz,
		DayCutoffHour: cutoff,
		CreatedAt:     now,
	}, nil
}

// Authenticate 校验用户名/密码；禁用用户一律拒绝，不区分"密码错"与"账号禁用"之外的细节。
func (s *AccountService) Authenticate(ctx context.Context, username, password string) (*store.User, error) {
	u, err := s.users.ByUsername(ctx, strings.TrimSpace(username))
	if err != nil {
		if store.IsNotFound(err) {
			// 用户不存在与密码错误返回同一个错误，避免账号枚举。
			return nil, ErrInvalidCredentials
		}
		return nil, fmt.Errorf("load user: %w", err)
	}
	if u.PasswordHash == nil {
		// 纯 OIDC 账号不能用密码登录。
		return nil, ErrInvalidCredentials
	}
	ok, err := Verify(*u.PasswordHash, password)
	if err != nil {
		return nil, fmt.Errorf("verify password: %w", err)
	}
	if !ok {
		return nil, ErrInvalidCredentials
	}
	if u.Status != store.StatusActive {
		return nil, ErrUserDisabled
	}
	return u, nil
}

// ChangePassword 校验旧密码、强度与"新旧不同"，写入新哈希并作废该用户全部会话（DESIGN.md §11）。
func (s *AccountService) ChangePassword(ctx context.Context, userID uint64, oldPassword, newPassword string) error {
	u, err := s.users.ByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("load user: %w", err)
	}
	if u.PasswordHash == nil {
		return ErrInvalidCredentials
	}
	ok, err := Verify(*u.PasswordHash, oldPassword)
	if err != nil {
		return fmt.Errorf("verify password: %w", err)
	}
	if !ok {
		return ErrInvalidCredentials
	}
	if oldPassword == newPassword {
		return ErrPasswordUnchanged
	}
	if err := ValidatePasswordPolicy(newPassword); err != nil {
		return err
	}
	hash, err := s.hasher.Hash(newPassword)
	if err != nil {
		return err
	}
	if err := s.users.SetPasswordHash(ctx, userID, hash); err != nil {
		return err
	}
	return s.sessions.RevokeAllForUser(ctx, userID, s.now())
}

// DisableUser 置为 disabled 并作废其全部会话；即使会话行被直接读回，中间件也会因状态不是 active 而拒绝。
func (s *AccountService) DisableUser(ctx context.Context, userID uint64) error {
	u, err := s.users.ByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("load user: %w", err)
	}
	u.Status = store.StatusDisabled
	if err := s.users.Update(ctx, u); err != nil {
		return err
	}
	return s.sessions.RevokeAllForUser(ctx, userID, s.now())
}

// EnableUser 重新启用账号；不恢复任何旧会话（用户需重新登录）。
func (s *AccountService) EnableUser(ctx context.Context, userID uint64) error {
	u, err := s.users.ByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("load user: %w", err)
	}
	u.Status = store.StatusActive
	return s.users.Update(ctx, u)
}
