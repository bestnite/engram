package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// HTTP and MCP share the service errors; checking code by code prevents the mapping from folding again and keeps item details.
func TestPackageErrorMappingPreservesCodeAndStatus(t *testing.T) {
	cases := []struct {
		code   string
		status int
	}{
		{store.CodePackageBadFormat, http.StatusBadRequest},
		{store.CodePackageTooLarge, http.StatusBadRequest},
		{store.CodePackageUnsafeEntry, http.StatusBadRequest},
		{store.CodePackageUnknownKind, http.StatusBadRequest},
		{store.CodePackageFormatObsolete, http.StatusBadRequest},
		{store.CodePackageDeckMetaInvalid, http.StatusBadRequest},
		{store.CodePackageMediaForbidden, http.StatusForbidden},
		{store.CodePackageQuotaExceeded, http.StatusRequestEntityTooLarge},
		{"", http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			err := mapPackageError(&store.PackageError{Code: tc.code, Message: "invalid entry", Entries: []string{"notes[0]"}})
			se := asServiceError(err)
			want := tc.code
			if want == "" {
				want = CodeInvalidRequest
			}
			if se.Code != want || se.Status != tc.status {
				t.Fatalf("mapped error = (%s, %d), want (%s, %d)", se.Code, se.Status, want, tc.status)
			}
			text := ErrorText(context.Background(), err)
			if !strings.HasPrefix(text, want+": ") || !strings.Contains(text, "notes[0]") {
				t.Fatalf("MCP error text lost code or entries: %q", text)
			}
		})
	}
}
