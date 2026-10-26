package auth

import (
	"reflect"
	"testing"
)

// The openid scope is required for the ID token the login and bind flows validate, so every effective scope list must
// contain it even when the admin saved a partial list. An empty value keeps the built-in default.
func TestSplitOIDCScopesAlwaysIncludesOpenID(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{name: "empty uses the default", raw: "", want: []string{"openid", "email", "profile"}},
		{name: "already present is kept", raw: "openid email", want: []string{"openid", "email"}},
		{name: "missing openid is prepended", raw: "email profile offline_access", want: []string{"openid", "email", "profile", "offline_access"}},
		{name: "comma separated list", raw: "email,profile", want: []string{"openid", "email", "profile"}},
		{name: "wrong casing is normalised", raw: "OpenID email", want: []string{"openid", "email"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := splitOIDCScopes(tc.raw); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("splitOIDCScopes(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}
