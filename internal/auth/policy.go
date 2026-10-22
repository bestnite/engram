package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Registration policy values; identifiers are always English (AGENTS.md §2.1).
const (
	PolicyOpen   = "open"
	PolicyInvite = "invite"
	PolicyClosed = "closed"
)

// Registration-related keys in the settings table (AGENTS.md §2.1: catalog/settings keys are always English).
const (
	// SettingKeyRegistrationPolicy's value is one of open | invite | closed.
	SettingKeyRegistrationPolicy = "registration_policy"
	// SettingKeyEmailAllowlist's value is the email domain allowlist; an empty value means no restriction.
	SettingKeyEmailAllowlist = "registration_email_domains"
)

// Stable sentinel values for a failed registration decision; the transport layer maps them to stable English codes and localized text.
var (
	// ErrRegistrationClosed means the closed policy rejects all self-service registration.
	ErrRegistrationClosed = errors.New("self-service registration is closed")
	// ErrEmailDomainNotAllowed means that under the open policy the email domain is not in the allowlist.
	ErrEmailDomainNotAllowed = errors.New("email domain is not allowed")
	// ErrInviteRequired means the invite policy requires a valid invite.
	ErrInviteRequired = errors.New("a valid invite is required")
)

// ParseRegistrationPolicy normalizes the raw value from settings.
// An empty or unknown value always falls back to closed — the safest default in a self-hosted setting.
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

// ParseEmailAllowlist parses the email domain allowlist, accepting two spellings:
// a JSON array (["example.com","example.org"]) and comma/whitespace separated (example.com, example.org).
// The settings table's value is JSON text and LoadSettings only decodes JSON strings,
// so the array form arrives here as raw text and has to be split apart here.
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
		// When JSON parsing fails, fall back to separator splitting so one bad value does not void the whole allowlist.
	}
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	return normalizeDomains(fields)
}

// normalizeDomains trims whitespace, lowercases and drops empty entries, preserving the order the caller gave.
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

// EmailDomainAllowed reports whether the email's domain hits the allowlist.
// An empty allowlist means no restriction; matching is exact on the domain (subdomains must be listed explicitly) and case-insensitive.
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

// DecideRegistration decides whether a self-service registration without an invite is allowed, and states the rejection reason.
//
// Under the invite policy, no invite always means rejection (ErrInviteRequired); under open the allowlist is layered on top;
// the closed policy rejects everything. First-admin onboarding does not go through here.
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
