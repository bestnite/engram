package config

import (
	"strings"
	"testing"
)

// envStub serves a map as the environment source.
func envStub(values map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		v, ok := values[name]
		return v, ok
	}
}

// requiredEnv is the minimal environment with every required variable present.
func requiredEnv() map[string]string {
	return map[string]string{
		"DB_DRIVER":      "sqlite",
		"DB_DSN":         "data/engram.db",
		"SESSION_SECRET": "CHANGE_ME_BASE64_32_BYTES",
		"ENCRYPTION_KEY": "CHANGE_ME_BASE64_32_BYTES",
	}
}

func TestPrecedenceEnvOverDBOverDefault(t *testing.T) {
	cases := []struct {
		name    string
		env     map[string]string
		db      map[string]string
		key     Key
		want    string
		wantSrc Source
	}{
		{
			name:    "env wins over db",
			env:     map[string]string{"HTTP_ADDR": "env.example.com:1111"},
			db:      map[string]string{"http_addr": "db.example.com:2222"},
			key:     KeyHTTPAddr,
			want:    "env.example.com:1111",
			wantSrc: SourceEnv,
		},
		{
			name:    "db used when env absent",
			env:     map[string]string{},
			db:      map[string]string{"http_addr": "db.example.com:2222"},
			key:     KeyHTTPAddr,
			want:    "db.example.com:2222",
			wantSrc: SourceDB,
		},
		{
			name:    "default when neither present",
			env:     map[string]string{},
			db:      map[string]string{},
			key:     KeyMediaDir,
			want:    "data/media",
			wantSrc: SourceDefault,
		},
		{
			name:    "unknown db key is ignored, not an error",
			env:     map[string]string{},
			db:      map[string]string{"registration_policy": "closed"},
			key:     KeyBootstrapAdminEmail,
			want:    "",
			wantSrc: SourceDefault,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := requiredEnv()
			for k, v := range tc.env {
				env[k] = v
			}
			cfg, err := Load(envStub(env), tc.db)
			if err != nil {
				t.Fatalf("Load() error = %v, want nil", err)
			}
			got := cfg.Get(tc.key)
			if got.Value != tc.want {
				t.Errorf("Get(%s).Value = %q, want %q", tc.key, got.Value, tc.want)
			}
			if got.Source != tc.wantSrc {
				t.Errorf("Get(%s).Source = %q, want %q", tc.key, got.Source, tc.wantSrc)
			}
		})
	}
}

func TestMissingRequiredVariableFailsWithEnglishError(t *testing.T) {
	cases := []struct {
		name    string
		env     map[string]string
		wantSub string
	}{
		{
			name:    "all required missing",
			env:     map[string]string{},
			wantSub: "missing required environment variable(s): DB_DRIVER, DB_DSN, ENCRYPTION_KEY, SESSION_SECRET",
		},
		{
			name: "blank value counts as missing",
			env: map[string]string{
				"DB_DRIVER": "sqlite", "DB_DSN": "   ", "SESSION_SECRET": "x", "ENCRYPTION_KEY": "y",
			},
			wantSub: "missing required environment variable(s): DB_DSN",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Load(envStub(tc.env), nil)
			if err == nil {
				t.Fatalf("Load() error = nil (cfg = %v), want a refusal", cfg)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("Load() error = %q, want it to contain %q", err.Error(), tc.wantSub)
			}
			assertASCIIEnglish(t, err.Error())
		})
	}
}

// assertASCIIEnglish asserts that error text is English (AGENTS.md §2.1): it contains no non-ASCII characters.
func assertASCIIEnglish(t *testing.T, msg string) {
	t.Helper()
	for _, r := range msg {
		if r > 127 {
			t.Errorf("log/error text must be English, found non-ASCII rune %q in %q", r, msg)
		}
	}
}

// TestTrustedProxiesDefaultsToNoTrust asserts that the default means "trust no proxies":
// with TRUSTED_PROXIES unset it returns an empty list and no error.
func TestTrustedProxiesDefaultsToNoTrust(t *testing.T) {
	cfg, err := Load(envStub(requiredEnv()), nil)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	got, err := cfg.TrustedProxies()
	if err != nil {
		t.Fatalf("TrustedProxies() error = %v, want nil", err)
	}
	if len(got) != 0 {
		t.Errorf("TrustedProxies() = %v, want empty (no proxy trusted by default)", got)
	}
}

// TestTrustedProxiesParsesCommaSeparatedList asserts that a comma-separated IP/CIDR list is kept entry by entry:
// leading/trailing whitespace trimmed, empty entries dropped, and the written order preserved.
func TestTrustedProxiesParsesCommaSeparatedList(t *testing.T) {
	env := requiredEnv()
	env["TRUSTED_PROXIES"] = " 127.0.0.1/32 , 192.0.2.0/24,::1 ,, "
	cfg, err := Load(envStub(env), nil)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	got, err := cfg.TrustedProxies()
	if err != nil {
		t.Fatalf("TrustedProxies() error = %v, want nil", err)
	}
	want := []string{"127.0.0.1/32", "192.0.2.0/24", "::1"}
	if len(got) != len(want) {
		t.Fatalf("TrustedProxies() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("TrustedProxies()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestTrustedProxiesRejectsInvalidEntry asserts that an invalid entry fails startup validation and names that entry,
// and is never silently dropped (AGENTS.md §2.6: an error has exactly one reading).
func TestTrustedProxiesRejectsInvalidEntry(t *testing.T) {
	cases := []struct {
		name     string
		value    string
		wantItem string
	}{
		{name: "CIDR mask out of range", value: "203.0.113.0/33", wantItem: "203.0.113.0/33"},
		{name: "not an IP", value: "not-an-ip", wantItem: "not-an-ip"},
		{name: "valid entry followed by invalid", value: "127.0.0.1/32, 999.1.1.1", wantItem: "999.1.1.1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := requiredEnv()
			env["TRUSTED_PROXIES"] = tc.value
			cfg, err := Load(envStub(env), nil)
			if err != nil {
				t.Fatalf("Load() error = %v, want nil (validation is TrustedProxies()' job)", err)
			}
			got, err := cfg.TrustedProxies()
			if err == nil {
				t.Fatalf("TrustedProxies(%q) = %v, want a refusal naming the invalid entry", tc.value, got)
			}
			if !strings.Contains(err.Error(), tc.wantItem) {
				t.Errorf("TrustedProxies(%q) error = %q, want it to name %q", tc.value, err.Error(), tc.wantItem)
			}
			assertASCIIEnglish(t, err.Error())
		})
	}
}
