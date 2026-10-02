// Package mail 提供可选的邮件投递层（DESIGN.md §4.7；AGENTS.md M1-17）。
//
// 三条不变量：
//   - 邮件是可选组件。SMTP 未配置时，Enqueue 返回 ErrNotConfigured，绝不静默丢弃；
//     依赖邮件的流程据此禁用并说明原因，而不是假装发出去了。
//   - 绝不在请求路径里同步发信。Enqueue 只写 outbox 队列表并唤醒后台 worker。
//   - 发信失败不得让触发它的操作失败。Enqueue 成功后投递结果由 worker 带退避重试写回。
//
// 本文件只放公共类型与 SMTP 配置解析；outbox/worker 见 outbox.go，SMTP 传输见 smtp.go。
package mail

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/config"
	"git.nite07.com/nite/engram/internal/store"
)

// ErrNotConfigured 表示 SMTP 未配置，依赖邮件的流程必须禁用并说明原因。
// 它是可判定的哨兵错误（errors.Is），调用方据此区分"未配置"与"投递失败"。
var ErrNotConfigured = errors.New("mail is not configured")

// Message 是一封待投递邮件。Type 是自由字符串，目录由 M1-18 定义；本包不解释它。
type Message struct {
	To       string
	Type     string
	Subject  string
	TextBody string
	HTMLBody string
	// Headers 是附加邮件头（例如 List-Unsubscribe）；可空。
	Headers map[string]string
}

// SMTP 传输的 TLS 模式；取值英文且稳定（AGENTS.md §2.1）。
const (
	// TLSModeNone 是明文（常见于内网中继或 25 端口）。
	TLSModeNone = "none"
	// TLSModeStartTLS 是明文连接后 STARTTLS 升级（587 端口，默认）。
	TLSModeStartTLS = "starttls"
	// TLSModeImplicit 是连接即 TLS（465 端口）。
	TLSModeImplicit = "implicit"
)

// SMTP 设置的稳定英文键名。与 OIDC 一致走 settings 表；口令键以 _password 结尾，
// 因此 store.IsSensitiveSettingKey 会把它判定为敏感值（加密存储、界面只显示已配置/未配置）。
const (
	SettingKeySMTPHost     = "smtp_host"
	SettingKeySMTPPort     = "smtp_port"
	SettingKeySMTPUsername = "smtp_username"
	SettingKeySMTPPassword = "smtp_password"
	SettingKeySMTPFrom     = "smtp_from"
	SettingKeySMTPTLSMode  = "smtp_tls_mode"
)

// 环境变量覆盖名。优先级：环境变量 > settings 表 > 默认值（DESIGN.md §8.4）。
const (
	EnvSMTPHost     = "SMTP_HOST"
	EnvSMTPPort     = "SMTP_PORT"
	EnvSMTPUsername = "SMTP_USERNAME"
	EnvSMTPPassword = "SMTP_PASSWORD"
	EnvSMTPFrom     = "SMTP_FROM"
	EnvSMTPTLSMode  = "SMTP_TLS_MODE"
)

// smtpEnvNames 把设置键映射到环境变量名；未登记的键没有环境变量覆盖。
var smtpEnvNames = map[string]string{
	SettingKeySMTPHost:     EnvSMTPHost,
	SettingKeySMTPPort:     EnvSMTPPort,
	SettingKeySMTPUsername: EnvSMTPUsername,
	SettingKeySMTPFrom:     EnvSMTPFrom,
	SettingKeySMTPTLSMode:  EnvSMTPTLSMode,
}

// smtpDefaults 是未配置任何来源时的内置默认值。host/from 默认空（即"未配置"），
// 端口与 TLS 模式给可用默认，让管理员只需填 host 与 from。
var smtpDefaults = map[string]string{
	SettingKeySMTPHost:     "",
	SettingKeySMTPPort:     "587",
	SettingKeySMTPUsername: "",
	SettingKeySMTPFrom:     "",
	SettingKeySMTPTLSMode:  TLSModeStartTLS,
}

// DefaultPort 是未配置端口时的默认 SMTP 端口。
const DefaultPort = 587

// SMTPConfig 是解析后的 SMTP 传输配置（含明文口令，仅供投递与测试连接使用）。
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	TLSMode  string
}

// Resolver 解析 SMTP 的生效配置：环境变量 > settings 表 > 默认值，并报告来源。
// 它不缓存：settings 表按需现读，管理员改完下一次解析即生效。
type Resolver struct {
	DB      *gorm.DB
	Secrets *store.SecretCodec
	// LookupEnv 可注入替身；为空时用 os.LookupEnv。
	LookupEnv func(string) (string, bool)
}

// NewResolver 构造解析器。Secrets 可为 nil（此时无法读取加密口令，视为未配置）。
func NewResolver(db *gorm.DB, secrets *store.SecretCodec) *Resolver {
	return &Resolver{DB: db, Secrets: secrets}
}

func (r *Resolver) lookup() func(string) (string, bool) {
	if r.LookupEnv != nil {
		return r.LookupEnv
	}
	return os.LookupEnv
}

// Field 解析一个非敏感设置项的生效值及其来源（env / db / default）。
func (r *Resolver) Field(ctx context.Context, key string) (string, config.Source, error) {
	if env := smtpEnvNames[key]; env != "" {
		if v, ok := r.lookup()(env); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v), config.SourceEnv, nil
		}
	}
	if r.DB != nil {
		settings, err := store.LoadSettings(ctx, r.DB)
		if err != nil {
			return "", config.SourceDefault, err
		}
		if v := strings.TrimSpace(settings[key]); v != "" {
			return v, config.SourceDB, nil
		}
	}
	return smtpDefaults[key], config.SourceDefault, nil
}

// password 解析 SMTP 口令：环境变量优先，其次加密的 settings 值。
// 未配置时返回空串与 SourceDefault，不报错（口令为空是合法状态）。
func (r *Resolver) password(ctx context.Context) (string, config.Source, error) {
	if v, ok := r.lookup()(EnvSMTPPassword); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v), config.SourceEnv, nil
	}
	if r.DB == nil || r.Secrets == nil {
		return "", config.SourceDefault, nil
	}
	plain, ok, err := store.GetSecret(ctx, r.DB, r.Secrets, SettingKeySMTPPassword)
	if err != nil {
		return "", config.SourceDefault, err
	}
	if ok {
		return plain, config.SourceDB, nil
	}
	return "", config.SourceDefault, nil
}

// PasswordConfigured 只报告口令是否已配置及其来源，不接触明文（管理面板用）。
func (r *Resolver) PasswordConfigured(ctx context.Context) (bool, config.Source, error) {
	if v, ok := r.lookup()(EnvSMTPPassword); ok && strings.TrimSpace(v) != "" {
		return true, config.SourceEnv, nil
	}
	if r.DB == nil || r.Secrets == nil {
		return false, config.SourceDefault, nil
	}
	configured, err := store.SecretConfigured(ctx, r.DB, SettingKeySMTPPassword)
	if err != nil {
		return false, config.SourceDefault, err
	}
	if configured {
		return true, config.SourceDB, nil
	}
	return false, config.SourceDefault, nil
}

// Config 解析完整的 SMTP 配置。第二个返回值 ok=false 表示未配置（缺 host 或缺 from），
// 此时不应投递任何邮件。口令为空是允许的（无认证中继）。
func (r *Resolver) Config(ctx context.Context) (SMTPConfig, bool, error) {
	host, _, err := r.Field(ctx, SettingKeySMTPHost)
	if err != nil {
		return SMTPConfig{}, false, err
	}
	portStr, _, err := r.Field(ctx, SettingKeySMTPPort)
	if err != nil {
		return SMTPConfig{}, false, err
	}
	username, _, err := r.Field(ctx, SettingKeySMTPUsername)
	if err != nil {
		return SMTPConfig{}, false, err
	}
	from, _, err := r.Field(ctx, SettingKeySMTPFrom)
	if err != nil {
		return SMTPConfig{}, false, err
	}
	tlsMode, _, err := r.Field(ctx, SettingKeySMTPTLSMode)
	if err != nil {
		return SMTPConfig{}, false, err
	}
	password, _, err := r.password(ctx)
	if err != nil {
		return SMTPConfig{}, false, err
	}
	cfg := SMTPConfig{
		Host:     strings.TrimSpace(host),
		Port:     parsePort(portStr),
		Username: strings.TrimSpace(username),
		Password: password,
		From:     strings.TrimSpace(from),
		TLSMode:  NormalizeTLSMode(tlsMode),
	}
	return cfg, cfg.Host != "" && cfg.From != "", nil
}

// parsePort 解析端口；非法或越界时回落到默认端口，绝不因一个坏值让整条配置不可用。
func parsePort(raw string) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 1 || n > 65535 {
		return DefaultPort
	}
	return n
}

// NormalizeTLSMode 把取值归一化到三个合法模式之一；未知值回落到默认（starttls）。
func NormalizeTLSMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case TLSModeNone:
		return TLSModeNone
	case TLSModeImplicit:
		return TLSModeImplicit
	case TLSModeStartTLS, "":
		return TLSModeStartTLS
	default:
		return TLSModeStartTLS
	}
}

// ValidTLSMode 报告取值是否是一个受支持的 TLS 模式（管理面板保存时校验用）。
func ValidTLSMode(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case TLSModeNone, TLSModeStartTLS, TLSModeImplicit:
		return true
	default:
		return false
	}
}
