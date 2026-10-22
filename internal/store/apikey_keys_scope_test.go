package store

import (
	"errors"
	"testing"
)

// TestAPIKeyKeysScopeSemantics pins the fifth scope tier `keys`:
// keys is an independent tier (it implies neither read/write/review, nor is it implied by them); admin implies all
// the others including keys, the reverse does not hold. After normalization keys sorts before admin.
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
