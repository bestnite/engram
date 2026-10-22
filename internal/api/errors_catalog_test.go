package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/store"
)

// TestAPIErrorOutputIdenticalAcrossAcceptLanguage asserts that for the same error scenario,
// whether the Accept-Language header is zh-CN, en or another language, the JSON output of the API error response is
// byte-for-byte identical stable English.
func TestAPIErrorOutputIdenticalAcrossAcceptLanguage(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "err_lang_test", store.RoleUser)
	readKey := seedKey(t, env.keys, user.ID, []string{store.ScopeRead}, nil)
	writeKey := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	r := env.router()

	testCases := []struct {
		name       string
		method     string
		path       string
		key        string
		body       string
		wantStatus int
		wantCode   string
	}{
		{
			name:       "unauthorized_missing_key",
			method:     http.MethodGet,
			path:       "/api/v1/decks",
			key:        "",
			wantStatus: http.StatusUnauthorized,
			wantCode:   CodeUnauthorized,
		},
		{
			name:       "invalid_api_key",
			method:     http.MethodGet,
			path:       "/api/v1/decks",
			key:        "fcard_bogus_key",
			wantStatus: http.StatusUnauthorized,
			wantCode:   CodeInvalidAPIKey,
		},
		{
			name:       "scope_required",
			method:     http.MethodPost,
			path:       "/api/v1/decks",
			key:        readKey.Plaintext,
			body:       `{"name":"test deck"}`,
			wantStatus: http.StatusForbidden,
			wantCode:   CodeScopeRequired,
		},
		{
			name:       "invalid_request_blank_name",
			method:     http.MethodPost,
			path:       "/api/v1/decks",
			key:        writeKey.Plaintext,
			body:       `{"name":"   "}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   CodeInvalidRequest,
		},
		{
			name:       "not_found",
			method:     http.MethodGet,
			path:       "/api/v1/decks/999999/notes",
			key:        readKey.Plaintext,
			wantStatus: http.StatusNotFound,
			wantCode:   CodeNotFound,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Request one: zh-CN
			recZh := doRequestWithHeader(r, tc.method, tc.path, tc.key, tc.body, "Accept-Language", "zh-CN,zh;q=0.9")
			// Request two: en
			recEn := doRequestWithHeader(r, tc.method, tc.path, tc.key, tc.body, "Accept-Language", "en-US,en;q=0.9")
			// Request three: another language
			recOther := doRequestWithHeader(r, tc.method, tc.path, tc.key, tc.body, "Accept-Language", "ja,fr;q=0.8")

			if recZh.Code != tc.wantStatus {
				t.Fatalf("zh status = %d, want %d (body %s)", recZh.Code, tc.wantStatus, recZh.Body.String())
			}

			// Byte-for-byte identical
			if !bytes.Equal(recZh.Body.Bytes(), recEn.Body.Bytes()) {
				t.Errorf("error output differ between zh-CN and en:\nzh: %s\nen: %s",
					recZh.Body.String(), recEn.Body.String())
			}
			if !bytes.Equal(recZh.Body.Bytes(), recOther.Body.Bytes()) {
				t.Errorf("error output differ between zh-CN and other:\nzh:    %s\nother: %s",
					recZh.Body.String(), recOther.Body.String())
			}

			// Verify the error envelope and the English message
			var envelope struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(recZh.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("parse error response json: %v", err)
			}
			if envelope.Error.Code != tc.wantCode {
				t.Errorf("error code = %q, want %q", envelope.Error.Code, tc.wantCode)
			}
			expectedBase := defaultErrorMessage(tc.wantCode)
			if !strings.HasPrefix(envelope.Error.Message, expectedBase) {
				t.Errorf("error message %q does not start with expected base %q", envelope.Error.Message, expectedBase)
			}
		})
	}
}

func doRequestWithHeader(r *gin.Engine, method, path, key, body, headerKey, headerVal string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if headerKey != "" {
		req.Header.Set(headerKey, headerVal)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// TestFormatErrorMessageDetailSemantics verifies the append and dedupe rules for dynamic detail:
// 1. An empty detail returns only the English base;
// 2. A detail equal to the English base is not concatenated twice;
// 3. Dynamic detail is appended as usual with " — ";
// 4. A detail already carrying the base prefix is not appended again.
func TestFormatErrorMessageDetailSemantics(t *testing.T) {
	ctx := context.Background()

	// 1. Empty detail
	if got, want := formatErrorMessage(ctx, CodeInvalidRequest, ""), "The request is invalid."; got != want {
		t.Errorf("empty detail = %q, want %q", got, want)
	}
	if got, want := formatErrorMessage(ctx, CodeInvalidRequest, "   "), "The request is invalid."; got != want {
		t.Errorf("whitespace detail = %q, want %q", got, want)
	}

	// 2. Detail identical to the base (avoid duplication)
	if got, want := formatErrorMessage(ctx, CodeInvalidRequest, "The request is invalid."), "The request is invalid."; got != want {
		t.Errorf("duplicate base detail = %q, want %q", got, want)
	}

	// 3. Dynamic detail appended
	detail := "fields are required"
	want := "The request is invalid. — fields are required"
	if got := formatErrorMessage(ctx, CodeInvalidRequest, detail); got != want {
		t.Errorf("dynamic detail = %q, want %q", got, want)
	}

	// 4. Existing prefix not repeated
	alreadyFormatted := "The request is invalid. — fields are required"
	if got := formatErrorMessage(ctx, CodeInvalidRequest, alreadyFormatted); got != alreadyFormatted {
		t.Errorf("already formatted detail = %q, want %q", got, alreadyFormatted)
	}

	// 5. An unregistered code falls back to the code itself + detail
	if got, want := formatErrorMessage(ctx, "custom_code", "some error"), "custom_code — some error"; got != want {
		t.Errorf("unregistered code with detail = %q, want %q", got, want)
	}
}

// TestMCPErrorTextUsesSameEnglishMappingAsREST asserts MCP ErrorText rendering uses the same English mapping as REST.
func TestMCPErrorTextUsesSameEnglishMappingAsREST(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		err      error
		wantCode string
		wantText string
	}{
		{
			err:      newServiceError(http.StatusBadRequest, CodeInvalidRequest, ""),
			wantCode: CodeInvalidRequest,
			wantText: "invalid_request: The request is invalid.",
		},
		{
			err:      newServiceError(http.StatusBadRequest, CodeInvalidRequest, "note not found"),
			wantCode: CodeInvalidRequest,
			wantText: "invalid_request: The request is invalid. — note not found",
		},
		{
			err:      newServiceError(http.StatusForbidden, CodeScopeRequired, "write"),
			wantCode: CodeScopeRequired,
			wantText: "scope_required: This API key does not have the required scope. — write",
		},
		{
			err:      newServiceError(http.StatusConflict, CodeVersionConflict, "resource changed"),
			wantCode: CodeVersionConflict,
			wantText: "version_conflict: The resource was changed by someone else. Reload and try again. — resource changed",
		},
		{
			err:      errors.New("plain internal failure"),
			wantCode: CodeInternal,
			wantText: "internal_error: An internal error occurred. — plain internal failure",
		},
	}

	for _, tc := range cases {
		text := ErrorText(ctx, tc.err)
		if text != tc.wantText {
			t.Errorf("ErrorText(%v) = %q, want %q", tc.err, text, tc.wantText)
		}

		// Assert it corresponds exactly to the text produced by formatErrorMessage
		se := asServiceError(tc.err)
		expectedRESTMsg := formatErrorMessage(ctx, se.Code, se.Message)
		expectedMCPText := se.Code + ": " + expectedRESTMsg
		if text != expectedMCPText {
			t.Errorf("ErrorText does not match REST format: got %q, want %q", text, expectedMCPText)
		}
	}
}
