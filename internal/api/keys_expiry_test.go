package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// TestCreateKeyRejectsNonFutureExpiry covers this rule: a requested expires_at must be strictly after the current time.
// An expiry in the past or exactly now would produce a key that is already dead, so it is rejected and nothing is persisted.
// The clock is the test environment's fixed clock, so the cases never depend on the wall clock.
func TestCreateKeyRejectsNonFutureExpiry(t *testing.T) {
	env := newTestEnv(t, 1000, 1000)
	router := env.router()
	user := seedUser(t, env.db, "create_key_expiry", store.RoleUser)
	callerKey := seedKey(t, env.keys, user.ID, []string{store.ScopeKeys}, nil)

	cases := []struct {
		name       string
		body       string
		wantStatus int
		wantExpiry *time.Time
	}{
		{
			name:       "past expiry rejected",
			body:       fmt.Sprintf(`{"name":"past","expires_at":%q}`, env.now.Add(-time.Minute).Format(time.RFC3339Nano)),
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "expiry exactly now rejected",
			body:       fmt.Sprintf(`{"name":"now","expires_at":%q}`, env.now.Format(time.RFC3339Nano)),
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "future expiry accepted and echoed",
			body:       fmt.Sprintf(`{"name":"future","expires_at":%q}`, env.now.Add(time.Hour).Format(time.RFC3339Nano)),
			wantStatus: http.StatusCreated,
			wantExpiry: store.Ptr(env.now.Add(time.Hour)),
		},
		{
			name:       "omitted expiry accepted with nil expiry",
			body:       `{"name":"no-expiry"}`,
			wantStatus: http.StatusCreated,
			wantExpiry: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := keyCountFor(t, env.keys, user.ID)
			code, raw := doJSON(t, router, http.MethodPost, "/api/v1/keys", callerKey.Plaintext, tc.body)
			if code != tc.wantStatus {
				t.Fatalf("POST /api/v1/keys = %d, want %d (body %s)", code, tc.wantStatus, raw)
			}
			added := 0
			if tc.wantStatus == http.StatusBadRequest {
				assertErrorCode(t, raw, CodeInvalidRequest)
			} else {
				added = 1
				var body struct {
					Key struct {
						ExpiresAt *time.Time `json:"expires_at"`
					} `json:"key"`
				}
				if err := json.Unmarshal(raw, &body); err != nil {
					t.Fatalf("create response is not JSON: %v (%s)", err, raw)
				}
				if tc.wantExpiry == nil {
					if body.Key.ExpiresAt != nil {
						t.Errorf("created expiry = %v, want nil", body.Key.ExpiresAt)
					}
				} else if body.Key.ExpiresAt == nil || !body.Key.ExpiresAt.Equal(*tc.wantExpiry) {
					t.Errorf("created expiry = %v, want %v", body.Key.ExpiresAt, tc.wantExpiry)
				}
			}
			if after, want := keyCountFor(t, env.keys, user.ID), before+added; after != want {
				t.Errorf("key count after request = %d, want %d", after, want)
			}
		})
	}
}
