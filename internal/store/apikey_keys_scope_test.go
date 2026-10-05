package store

import (
	"errors"
	"testing"
)

// TestAPIKeyKeysScopeSemantics 钉住 F11 引入的第五档 scope `keys`（DESIGN.md §7.2）：
// keys 是独立档位（不蕴含 read/write/review，也不被它们蕴含）；admin 蕴含包括 keys
// 在内的其余各档，反向不成立。归一化后 keys 排在 admin 之前。
func TestAPIKeyKeysScopeSemantics(t *testing.T) {
	has := []struct {
		name   string
		scopes string
		want   string
		ok     bool
	}{
		{"keys covers keys", ScopeKeys, ScopeKeys, true},
		{"admin implies keys", ScopeAdmin, ScopeKeys, true},
		{"keys does not imply admin", ScopeKeys, ScopeAdmin, false},
		{"keys does not imply read", ScopeKeys, ScopeRead, false},
		{"keys does not imply write", ScopeKeys, ScopeWrite, false},
		{"keys does not imply review", ScopeKeys, ScopeReview, false},
		{"read does not imply keys", ScopeRead, ScopeKeys, false},
		{"write,review lacks keys", "write,review", ScopeKeys, false},
		{"read,keys covers keys", "read,keys", ScopeKeys, true},
	}
	for _, tc := range has {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasScope(tc.scopes, tc.want); got != tc.ok {
				t.Errorf("HasScope(%q, %q) = %v, want %v", tc.scopes, tc.want, got, tc.ok)
			}
			key := &APIKey{ID: 1, Scopes: tc.scopes}
			if got := key.HasScope(tc.want); got != tc.ok {
				t.Errorf("(*APIKey).HasScope(%q) = %v, want %v", tc.want, got, tc.ok)
			}
		})
	}

	t.Run("keys is a valid scope", func(t *testing.T) {
		if !IsValidScope(ScopeKeys) {
			t.Errorf("IsValidScope(%q) = false, want true", ScopeKeys)
		}
	})
	t.Run("normalize orders keys before admin", func(t *testing.T) {
		got, err := NormalizeScopes([]string{ScopeAdmin, ScopeKeys, ScopeRead})
		if err != nil {
			t.Fatalf("NormalizeScopes() error = %v", err)
		}
		if got != "read,keys,admin" {
			t.Errorf("NormalizeScopes() = %q, want %q", got, "read,keys,admin")
		}
	})
	t.Run("normalize accepts keys alone", func(t *testing.T) {
		got, err := NormalizeScopes([]string{ScopeKeys})
		if err != nil {
			t.Fatalf("NormalizeScopes() error = %v", err)
		}
		if got != ScopeKeys {
			t.Errorf("NormalizeScopes() = %q, want %q", got, ScopeKeys)
		}
	})
	t.Run("unknown scope still rejected", func(t *testing.T) {
		if _, err := NormalizeScopes([]string{"keys2"}); !errors.Is(err, ErrAPIKeyInvalidScope) {
			t.Errorf("NormalizeScopes(keys2) error = %v, want ErrAPIKeyInvalidScope", err)
		}
	})
}
