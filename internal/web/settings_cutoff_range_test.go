package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A day cutoff of 0 is midnight; only an empty setting (NULL) means the default 04:00, and writing and storage must not conflate the two.
// The SSR /settings/profile form is gone, so profile writes now go through the SPA's PATCH /api/v1/profile.

// patchProfileCutoff submits a profile as a JSON PATCH; hour nil means clearing the cutoff (stored as NULL).
func patchProfileCutoff(t *testing.T, srv *Server, cookies []*http.Cookie, csrf string, hour *int) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"display_name":    "Owner",
		"locale":          "zh-CN",
		"timezone":        "UTC",
		"day_cutoff_hour": hour,
	})
	if err != nil {
		t.Fatalf("marshal profile patch: %v", err)
	}
	return jsonRequest(t, srv, http.MethodPatch, "/api/v1/profile", string(raw), cookies, csrf)
}
