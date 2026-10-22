// Package mail provides the optional email delivery layer.
//
// Three invariants:
//   - Email is an optional component. When SMTP is not configured, Enqueue returns ErrNotConfigured
//     and never drops the mail silently; flows that depend on email disable themselves and say why,
//     instead of pretending the mail went out.
//   - Never send synchronously on the request path. Enqueue only writes the outbox queue table and
//     wakes the background worker.
//   - A delivery failure must not fail the operation that triggered it. After a successful Enqueue,
//     the worker writes the delivery result back with backoff retries.
//
// This file holds only the public types and the SMTP config resolution; see outbox.go for the
// outbox/worker and smtp.go for the SMTP transport.
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

// ErrNotConfigured means SMTP is not configured; flows that depend on email must disable themselves
// and say why. It is a testable sentinel error (errors.Is), so callers can tell "not configured"
// apart from "delivery failed".
var ErrNotConfigured = errors.New("mail is not configured")

// Message is one email to deliver. Type is a free-form string whose catalog is defined elsewhere;
// this package does not interpret it.
type Message struct {
	To       string
	Type     string
	Subject  string
	TextBody string
	HTMLBody string
	// Headers are extra mail headers (for example List-Unsubscribe); may be nil.
	Headers map[string]string
}

// TLS modes of the SMTP transport; the values are English and stable (AGENTS.md §2.1).
const (
	// TLSModeNone is plaintext (common for an intranet relay or port 25).
	TLSModeNone = "none"
	// TLSModeStartTLS is a plaintext connection upgraded via STARTTLS (port 587, the default).
	TLSModeStartTLS = "starttls"
	// TLSModeImplicit is TLS from the moment of connecting (port 465).
	TLSModeImplicit = "implicit"
)

// The stable English key names of the SMTP settings. Like OIDC they live in the settings table; the
// password key ends in _password, so store.IsSensitiveSettingKey treats it as a secret (encrypted at
// rest, and the UI only shows configured/not configured).
const (
	SettingKeySMTPHost     = "smtp_host"
	SettingKeySMTPPort     = "smtp_port"
	SettingKeySMTPUsername = "smtp_username"
	SettingKeySMTPPassword = "smtp_password"
	SettingKeySMTPFrom     = "smtp_from"
	SettingKeySMTPTLSMode  = "smtp_tls_mode"
)

// Environment variable override names. Precedence: environment variable > settings table > default.
const (
	EnvSMTPHost     = "SMTP_HOST"
	EnvSMTPPort     = "SMTP_PORT"
	EnvSMTPUsername = "SMTP_USERNAME"
	EnvSMTPPassword = "SMTP_PASSWORD"
	EnvSMTPFrom     = "SMTP_FROM"
	EnvSMTPTLSMode  = "SMTP_TLS_MODE"
)

// smtpEnvNames maps a setting key to an environment variable name; unregistered keys have no
// environment variable override.
var smtpEnvNames = map[string]string{
	SettingKeySMTPHost:     EnvSMTPHost,
	SettingKeySMTPPort:     EnvSMTPPort,
	SettingKeySMTPUsername: EnvSMTPUsername,
	SettingKeySMTPFrom:     EnvSMTPFrom,
	SettingKeySMTPTLSMode:  EnvSMTPTLSMode,
}

// smtpDefaults are the built-in defaults when nothing is configured. host/from default to empty
// (i.e. "not configured"), while port and TLS mode get usable defaults so an admin only has to fill
// in host and from.
var smtpDefaults = map[string]string{
	SettingKeySMTPHost:     "",
	SettingKeySMTPPort:     "587",
	SettingKeySMTPUsername: "",
	SettingKeySMTPFrom:     "",
	SettingKeySMTPTLSMode:  TLSModeStartTLS,
}

// DefaultPort is the default SMTP port when none is configured.
const DefaultPort = 587

// SMTPConfig is the resolved SMTP transport configuration (it contains the plaintext password and is
// only for delivery and test connections).
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	TLSMode  string
}

// Resolver resolves the effective SMTP configuration: environment variable > settings table >
// default, and reports the source. It caches nothing: the settings table is read on demand, so an
// admin's change takes effect on the next resolution.
type Resolver struct {
	DB      *gorm.DB
	Secrets *store.SecretCodec
	// LookupEnv can be given a stand-in; when nil, os.LookupEnv is used.
	LookupEnv func(string) (string, bool)
}

// NewResolver builds a resolver. Secrets may be nil (then the encrypted password cannot be read and
// SMTP counts as not configured).
func NewResolver(db *gorm.DB, secrets *store.SecretCodec) *Resolver {
	return &Resolver{DB: db, Secrets: secrets}
}

func (r *Resolver) lookup() func(string) (string, bool) {
	if r.LookupEnv != nil {
		return r.LookupEnv
	}
	return os.LookupEnv
}

// Field resolves the effective value of one non-sensitive setting and its source (env / db / default).
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

// password resolves the SMTP password: the environment variable wins, then the encrypted settings
// value. When it is not configured it returns the empty string and SourceDefault without an error (an
// empty password is a legitimate state).
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

// PasswordConfigured only reports whether the password is configured and where it comes from, without
// touching the plaintext (for the admin panel).
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

// Config resolves the complete SMTP configuration. A second return value ok=false means it is not
// configured (host or from missing), and no mail should be delivered then. An empty password is
// allowed (an unauthenticated relay).
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

// parsePort parses the port; an invalid or out-of-range value falls back to the default port -- a
// single bad value must never make the whole configuration unusable.
func parsePort(raw string) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 1 || n > 65535 {
		return DefaultPort
	}
	return n
}

// NormalizeTLSMode normalizes a value to one of the three valid modes; an unknown value falls back to
// the default (starttls).
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

// ValidTLSMode reports whether a value is a supported TLS mode (used for validation when the admin
// panel saves).
func ValidTLSMode(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case TLSModeNone, TLSModeStartTLS, TLSModeImplicit:
		return true
	default:
		return false
	}
}
