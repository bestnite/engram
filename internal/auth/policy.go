package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// 注册策略取值（DESIGN.md §4.2）；标识符一律英文（AGENTS.md §2.1）。
const (
	PolicyOpen   = "open"
	PolicyInvite = "invite"
	PolicyClosed = "closed"
)

// settings 表里与注册相关的键（AGENTS.md §2.1：catalog/设置键一律英文）。
const (
	// SettingKeyRegistrationPolicy 的 value 是 open | invite | closed 之一。
	SettingKeyRegistrationPolicy = "registration_policy"
	// SettingKeyEmailAllowlist 的 value 是邮箱域名白名单；空值表示不限制。
	SettingKeyEmailAllowlist = "registration_email_domains"
)

// 注册判定失败的稳定哨兵值；transport 层据此映射到稳定英文 code 与本地化文案。
var (
	// ErrRegistrationClosed 表示 closed 策略拒绝一切自助注册。
	ErrRegistrationClosed = errors.New("self-service registration is closed")
	// ErrEmailDomainNotAllowed 表示 open 策略下邮箱域名不在白名单内。
	ErrEmailDomainNotAllowed = errors.New("email domain is not allowed")
	// ErrInviteRequired 表示 invite 策略要求一个有效邀请。
	ErrInviteRequired = errors.New("a valid invite is required")
)

// ParseRegistrationPolicy 把 settings 里的原始值归一化。
// 空值或未知取值一律回落到 closed —— 自托管场景下最安全的默认（DESIGN.md §4.2）。
func ParseRegistrationPolicy(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case PolicyOpen:
		return PolicyOpen
	case PolicyInvite:
		return PolicyInvite
	default:
		return PolicyClosed
	}
}

// ParseEmailAllowlist 解析邮箱域名白名单，兼容两种写法：
// JSON 数组（["example.com","example.org"]）与逗号/空白分隔（example.com, example.org）。
// settings 表的 value 是 JSON 文本，LoadSettings 只解码 JSON 字符串，
// 因此数组形态会以原始文本到达这里，需要在这里拆开。
func ParseEmailAllowlist(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if strings.HasPrefix(raw, "[") {
		var arr []string
		if err := json.Unmarshal([]byte(raw), &arr); err == nil {
			return normalizeDomains(arr)
		}
		// JSON 解析失败时退回分隔符切分，避免一条坏数据让整个白名单失效。
	}
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	return normalizeDomains(fields)
}

// normalizeDomains 去空白、转小写并去掉空项，保持调用方给出的顺序。
func normalizeDomains(in []string) []string {
	out := make([]string, 0, len(in))
	for _, d := range in {
		d = strings.ToLower(strings.TrimSpace(d))
		if d != "" {
			out = append(out, d)
		}
	}
	return out
}

// EmailDomainAllowed 报告 email 的域名是否命中白名单。
// 白名单为空表示不限制；匹配为域名精确匹配（含子域需显式列出），大小写不敏感。
func EmailDomainAllowed(email string, allowlist []string) bool {
	if len(allowlist) == 0 {
		return true
	}
	at := strings.LastIndex(email, "@")
	if at < 0 || at == len(email)-1 {
		return false
	}
	domain := strings.ToLower(email[at+1:])
	for _, allowed := range allowlist {
		if domain == strings.ToLower(strings.TrimSpace(allowed)) {
			return true
		}
	}
	return false
}

// DecideRegistration 判定一次没有邀请的自助注册是否放行，并说明拒绝原因。
//
// invite 策略下无邀请一律拒绝（ErrInviteRequired）；open 策略下再叠加白名单；
// closed 策略拒绝一切。首个管理员引导不经过这里（DESIGN.md §4.1）。
func DecideRegistration(policy, email string, allowlist []string) error {
	switch ParseRegistrationPolicy(policy) {
	case PolicyOpen:
		if !EmailDomainAllowed(email, allowlist) {
			return fmt.Errorf("%w: %s", ErrEmailDomainNotAllowed, email)
		}
		return nil
	case PolicyInvite:
		return ErrInviteRequired
	default:
		return ErrRegistrationClosed
	}
}
