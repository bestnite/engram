package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"example.com/engram/internal/store"
)

// 本文件是 M1-16 的 TOTP 业务层：生成 secret、加密落库、校验验证码、一次性恢复码、关闭。
//
// 依赖选择（AGENTS.md §2.4、DESIGN.md §10.1）：TOTP 算法用事实标准库
// github.com/pquerna/otp，不自己写 HMAC。库负责 secret 生成与验证码校验；
// otpauth:// 链接的拼装只是格式转写，用同一个 helper 保证生成与展示一致。

const (
	// DefaultTOTPIssuer 是认证器 App 里显示的发行方名（项目正式名，DESIGN.md 附二）。
	DefaultTOTPIssuer = "Engram"
	// totpPeriod 与 totpSkew：30 秒一步，允许前后各一步漂移（客户端时钟偏差）。
	totpPeriod = 30
	totpSkew   = 1
	// RecoveryCodeCount 是一次生成的恢复码数量。
	RecoveryCodeCount = 10
	// recoveryCodeLength 是单个恢复码的字符数（不含分隔连字符）。
	recoveryCodeLength = 10
	// recoveryCodeAlphabet 是 Base32 字符集，去掉 0/1/8/9 等易混字符。
	recoveryCodeAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
)

// TOTP 相关的稳定错误值；transport 层据此映射到语言包 key。
var (
	ErrTOTPNotEnabled     = errors.New("totp is not enabled")
	ErrTOTPAlreadyEnabled = errors.New("totp is already enabled")
	ErrTOTPNoPendingSetup = errors.New("no pending totp setup")
	ErrTOTPInvalidCode    = errors.New("invalid totp code")
)

// TOTPService 负责 TOTP 的完整生命周期。
type TOTPService struct {
	store  *store.TOTPStore
	codec  *store.SecretCodec
	issuer string
	now    func() time.Time
}

// NewTOTPService 构造 TOTP 服务；store 与 codec 缺一不可（secret 必须能加密存储）。
func NewTOTPService(st *store.TOTPStore, codec *store.SecretCodec, issuer string) (*TOTPService, error) {
	if st == nil || codec == nil {
		return nil, errors.New("auth: totp store and secret codec are required")
	}
	if strings.TrimSpace(issuer) == "" {
		issuer = DefaultTOTPIssuer
	}
	return &TOTPService{store: st, codec: codec, issuer: issuer,
		now: func() time.Time { return time.Now().UTC() }}, nil
}

// Enabled 报告用户是否已启用 TOTP（只有已确认的绑定才算）。
func (s *TOTPService) Enabled(ctx context.Context, userID uint64) (bool, error) {
	return s.store.Enabled(ctx, userID)
}

// Begin 生成新的 secret 并以未确认状态加密落库；返回明文 secret 与 otpauth 链接。
// 明文只在此刻返回一次（页面展示）；库里只有密文。已启用的账号不允许重新绑定。
func (s *TOTPService) Begin(ctx context.Context, userID uint64, accountName string) (secret, otpauth string, err error) {
	enabled, err := s.store.Enabled(ctx, userID)
	if err != nil {
		return "", "", err
	}
	if enabled {
		return "", "", ErrTOTPAlreadyEnabled
	}
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      s.issuer,
		AccountName: accountName,
		Period:      totpPeriod,
	})
	if err != nil {
		return "", "", fmt.Errorf("generate totp key: %w", err)
	}
	secret = key.Secret()
	ciphertext, err := s.codec.Encrypt(secret)
	if err != nil {
		return "", "", fmt.Errorf("encrypt totp secret: %w", err)
	}
	if err := s.store.UpsertPending(ctx, userID, ciphertext, s.now()); err != nil {
		return "", "", err
	}
	return secret, otpauthURL(s.issuer, accountName, secret), nil
}

// Pending 取回尚未确认的绑定信息（明文 secret 与 otpauth 链接），供设置页刷新时重显。
// 返回 (nil, nil, false, nil) 表示没有待确认的绑定。
func (s *TOTPService) Pending(ctx context.Context, userID uint64, accountName string) (secret, otpauth string, ok bool, err error) {
	row, err := s.store.Get(ctx, userID)
	if err != nil {
		if store.IsNotFound(err) {
			return "", "", false, nil
		}
		return "", "", false, err
	}
	if row.ConfirmedAt != nil {
		return "", "", false, nil
	}
	plain, err := s.codec.Decrypt(row.SecretCiphertext)
	if err != nil {
		// 解不开必须大声报错，绝不当作「没绑定」（M6-10 的约定）。
		return "", "", false, fmt.Errorf("decrypt pending totp secret: %w", err)
	}
	return plain, otpauthURL(s.issuer, accountName, plain), true, nil
}

// Confirm 用一次验证码确认待确认的绑定；成功后启用并生成一批一次性恢复码。
// 返回的恢复码明文只在此刻返回一次。
func (s *TOTPService) Confirm(ctx context.Context, userID uint64, code string) (recoveryCodes []string, err error) {
	row, err := s.store.Get(ctx, userID)
	if err != nil {
		if store.IsNotFound(err) {
			return nil, ErrTOTPNoPendingSetup
		}
		return nil, err
	}
	if row.ConfirmedAt != nil {
		return nil, ErrTOTPAlreadyEnabled
	}
	plain, err := s.codec.Decrypt(row.SecretCiphertext)
	if err != nil {
		return nil, fmt.Errorf("decrypt pending totp secret: %w", err)
	}
	if !s.validateCode(plain, code) {
		return nil, ErrTOTPInvalidCode
	}
	if err := s.store.Confirm(ctx, userID, s.now()); err != nil {
		return nil, err
	}
	return s.RegenerateRecoveryCodes(ctx, userID)
}

// VerifySecondFactor 校验一次登录第二步输入：先按 6 位验证码校验，失败再尝试恢复码。
// 返回 usedRecovery 表示本次是通过恢复码通过的（调用方据此写审计）。
// 未启用或输入为空一律返回 (false, false, nil)，不区分原因，避免给爆破者额外信息。
func (s *TOTPService) VerifySecondFactor(ctx context.Context, userID uint64, code string) (usedRecovery, ok bool, err error) {
	entry := strings.TrimSpace(code)
	if entry == "" {
		return false, false, nil
	}
	row, err := s.store.Get(ctx, userID)
	if err != nil {
		if store.IsNotFound(err) {
			return false, false, nil
		}
		return false, false, err
	}
	if row.ConfirmedAt == nil {
		return false, false, nil
	}
	plain, err := s.codec.Decrypt(row.SecretCiphertext)
	if err != nil {
		return false, false, fmt.Errorf("decrypt totp secret: %w", err)
	}
	if isSixDigits(entry) && s.validateCode(plain, entry) {
		return false, true, nil
	}
	consumed, err := s.store.ConsumeRecoveryCode(ctx, userID, hashRecoveryCode(entry), s.now())
	if err != nil {
		return false, false, err
	}
	return consumed, consumed, nil
}

// RegenerateRecoveryCodes 生成一批新的恢复码（明文返回一次），并作废旧的未使用码。
func (s *TOTPService) RegenerateRecoveryCodes(ctx context.Context, userID uint64) ([]string, error) {
	plaintext, hashes, err := generateRecoveryCodes(RecoveryCodeCount)
	if err != nil {
		return nil, err
	}
	if err := s.store.ReplaceRecoveryCodes(ctx, userID, hashes, s.now()); err != nil {
		return nil, err
	}
	return plaintext, nil
}

// UnusedRecoveryCodeCount 返回剩余可用的恢复码数量。
func (s *TOTPService) UnusedRecoveryCodeCount(ctx context.Context, userID uint64) (int64, error) {
	return s.store.UnusedRecoveryCodeCount(ctx, userID)
}

// Disable 关闭 TOTP 并删除全部恢复码。密码确认由调用方完成（handler 有用户上下文）。
func (s *TOTPService) Disable(ctx context.Context, userID uint64) error {
	enabled, err := s.store.Enabled(ctx, userID)
	if err != nil {
		return err
	}
	if !enabled {
		return ErrTOTPNotEnabled
	}
	return s.store.Delete(ctx, userID)
}

// validateCode 用库校验 6 位验证码，允许前后各一步时钟漂移。
func (s *TOTPService) validateCode(secret, code string) bool {
	ok, err := totp.ValidateCustom(strings.TrimSpace(code), secret, s.now(), totp.ValidateOpts{
		Period:    totpPeriod,
		Skew:      totpSkew,
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	})
	return err == nil && ok
}

// otpauthURL 拼装 otpauth:// 链接（Google Authenticator Key URI 格式）。
// 生成与展示共用这一处实现，避免两条路径给出不同的链接。
func otpauthURL(issuer, account, secret string) string {
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", "6")
	q.Set("period", strconv.Itoa(totpPeriod))
	return "otpauth://totp/" + url.PathEscape(issuer+":"+account) + "?" + q.Encode()
}

// isSixDigits 报告输入是否恰好是 6 位数字（验证码的形态）。
func isSixDigits(s string) bool {
	if len(s) != 6 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// normalizeRecoveryCode 归一化恢复码：去连字符与空格、转大写。
func normalizeRecoveryCode(code string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(code) {
		if strings.ContainsRune(recoveryCodeAlphabet, r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// hashRecoveryCode 返回恢复码的 sha256 十六进制摘要。
// 恢复码是 50 bit 的随机值而非用户自选口令，因此不需要加盐/慢哈希；
// 加盐还要为每行额外存盐，得不偿失。库中只存摘要，明文不落库。
func hashRecoveryCode(code string) string {
	sum := sha256.Sum256([]byte(normalizeRecoveryCode(code)))
	return hex.EncodeToString(sum[:])
}

// generateRecoveryCodes 生成 n 个恢复码，返回明文（带连字符）与摘要。
func generateRecoveryCodes(n int) (plaintext []string, hashes []string, err error) {
	plaintext = make([]string, 0, n)
	hashes = make([]string, 0, n)
	for i := 0; i < n; i++ {
		raw, err := randomRecoveryCode(recoveryCodeLength)
		if err != nil {
			return nil, nil, err
		}
		plaintext = append(plaintext, raw[:5]+"-"+raw[5:])
		hashes = append(hashes, hashRecoveryCode(raw))
	}
	return plaintext, hashes, nil
}

// randomRecoveryCode 从 Base32 字符集里均匀取 length 个字符。
// 用拒绝采样消除取模偏差：字符集 32 恰好整除 256，这里的判断是防御性写法。
func randomRecoveryCode(length int) (string, error) {
	buf := make([]byte, length)
	out := make([]byte, 0, length)
	for len(out) < length {
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("generate recovery code: %w", err)
		}
		for _, b := range buf {
			idx := int(b) % len(recoveryCodeAlphabet)
			if idx < len(recoveryCodeAlphabet) {
				out = append(out, recoveryCodeAlphabet[idx])
				if len(out) == length {
					break
				}
			}
		}
	}
	return string(out), nil
}
