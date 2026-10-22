// Package config is responsible for reading startup configuration.
// The precedence is fixed: environment variables > settings table > built-in defaults; accessors must be able to report where the effective value came from,
// otherwise the admin panel cannot answer "why did my change not take effect".
package config

import (
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
)

// Source identifies where an effective value came from.
type Source string

const (
	SourceEnv     Source = "env"
	SourceDB      Source = "db"
	SourceDefault Source = "default"
)

// Key is the stable English identifier of a setting (AGENTS.md §2.1: identifiers are always English).
// The same Key is used both for the environment variable and as the settings table key.
type Key string

const (
	KeyHTTPAddr            Key = "http_addr"
	KeyBaseURL             Key = "base_url"
	KeyDBDriver            Key = "db_driver"
	KeyDBDSN               Key = "db_dsn"
	KeySessionSecret       Key = "session_secret"
	KeyEncryptionKey       Key = "encryption_key"
	KeyAutoMigrate         Key = "auto_migrate"
	KeyBootstrapAdminEmail Key = "bootstrap_admin_email"
	KeyMediaDir            Key = "media_dir"
	// KeyTrustedProxies is the list of proxy addresses allowed to rewrite ClientIP() (comma-separated IPs or CIDRs).
	// Empty by default = no proxy is trusted.
	KeyTrustedProxies Key = "trusted_proxies"
	// KeyOptimizerPath is the path to the FSRS optimizer adapter binary.
	// When left empty, internal/jobs resolves it: first look for an optimizer next to the service binary, then fall back to the repository build artifact.
	KeyOptimizerPath Key = "optimizer_path"
)

// field describes a setting's environment variable name, whether it is required, and its built-in default.
type field struct {
	env      string
	required bool
	def      string
}

// fields is the single registry of settings: adding one requires changing both this list and .env.example (a unit test enforces the sync).
var fields = map[Key]field{
	KeyHTTPAddr:            {env: "HTTP_ADDR", def: "127.0.0.1:8080"},
	KeyBaseURL:             {env: "BASE_URL", def: "http://localhost:8080"},
	KeyDBDriver:            {env: "DB_DRIVER", required: true},
	KeyDBDSN:               {env: "DB_DSN", required: true},
	KeySessionSecret:       {env: "SESSION_SECRET", required: true},
	KeyEncryptionKey:       {env: "ENCRYPTION_KEY", required: true},
	KeyAutoMigrate:         {env: "AUTO_MIGRATE", def: "1"},
	KeyBootstrapAdminEmail: {env: "BOOTSTRAP_ADMIN_EMAIL"},
	KeyMediaDir:            {env: "MEDIA_DIR", def: "data/media"},
	KeyTrustedProxies:      {env: "TRUSTED_PROXIES"},
	KeyOptimizerPath:       {env: "OPTIMIZER_PATH"},
}

// Value is an accessor's result: the effective value plus its source.
type Value struct {
	Key    Key
	Value  string
	Source Source
}

// Config holds the result of one load. The two kinds of source are kept apart; precedence is applied in Get.
type Config struct {
	env map[Key]string
	db  map[Key]string
}

// Load reads environment variables and layers the settings table overrides on top.
// When lookupEnv is nil, os.LookupEnv is used (tests inject a stand-in). settings may be nil.
// Returns an English error when any required variable is missing; callers should refuse to start on it.
func Load(lookupEnv func(string) (string, bool), settings map[string]string) (*Config, error) {
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	c := &Config{env: make(map[Key]string, len(fields)), db: make(map[Key]string, len(fields))}
	var missing []string
	for key, f := range fields {
		if v, ok := lookupEnv(f.env); ok && strings.TrimSpace(v) != "" {
			c.env[key] = strings.TrimSpace(v)
		} else if f.required {
			missing = append(missing, f.env)
		}
	}
	if len(missing) > 0 {
		// Sorting keeps the error message stable and assertable.
		sort.Strings(missing)
		return nil, fmt.Errorf("missing required environment variable(s): %s", strings.Join(missing, ", "))
	}
	for rawKey, rawValue := range settings {
		key := Key(rawKey)
		// The settings table may hold keys that exist only in the database and do not come from environment variables; only the keys common to both matter here.
		if _, known := fields[key]; !known {
			continue
		}
		if v := strings.TrimSpace(rawValue); v != "" {
			c.db[key] = v
		}
	}
	return c, nil
}

// Get is the only accessor: it returns the effective value and its source (env / db / default).
func (c *Config) Get(key Key) Value {
	if v, ok := c.env[key]; ok {
		return Value{Key: key, Value: v, Source: SourceEnv}
	}
	if v, ok := c.db[key]; ok {
		return Value{Key: key, Value: v, Source: SourceDB}
	}
	return Value{Key: key, Value: fields[key].def, Source: SourceDefault}
}

// String makes it usable directly as a string.
func (v Value) String() string { return v.Value }

// TrustedProxies parses and validates TRUSTED_PROXIES: a comma-separated list of IPs or CIDRs.
//
// Empty by default = no proxy is trusted: gin stops trusting X-Forwarded-For / X-Real-IP and ClientIP() falls back to
// RemoteAddr, so login rate-limit keys, audit IPs and new-device/new-IP alerts are no longer steered by request headers. Only requests
// coming from the listed addresses are allowed to rewrite those results.
//
// Any invalid entry returns an English error naming that entry, and the caller refuses to start on it; it is never silently dropped (AGENTS.md §2.6).
// Deliberately IP/CIDR only: headers like X-Forwarded-Proto are not treated as trust evidence (TLS termination info is decided by BASE_URL).
func (c *Config) TrustedProxies() ([]string, error) {
	raw := c.Get(KeyTrustedProxies).Value
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		entry := strings.TrimSpace(part)
		if entry == "" {
			continue
		}
		if err := validateProxyEntry(entry); err != nil {
			return nil, fmt.Errorf("invalid TRUSTED_PROXIES entry %q: %w", entry, err)
		}
		out = append(out, entry)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// validateProxyEntry accepts only a single IP or CIDR; everything else is rejected.
func validateProxyEntry(entry string) error {
	if strings.Contains(entry, "/") {
		if _, _, err := net.ParseCIDR(entry); err != nil {
			return fmt.Errorf("not a valid CIDR")
		}
		return nil
	}
	if net.ParseIP(entry) == nil {
		return fmt.Errorf("not a valid IP or CIDR")
	}
	return nil
}
