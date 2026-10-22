package web

import (
	"encoding/json"
	"net/http"
	"testing"

	"git.nite07.com/nite/engram/internal/i18n"
)

// This file is the acceptance test for the admin panel's "jobs / language pack" pages cutover to the SPA.

// TestAdminJobsAndI18nCutover covers the cutover of both pages and the non-admin gate.
func TestAdminJobsAndI18nCutover(t *testing.T) {
	for _, path := range []string{"/admin/jobs", "/admin/i18n"} {
		t.Run(path, func(t *testing.T) {
			srv, db, _, cookies, _ := newNotesServer(t)

			assertShell(t, getWithCookies(t, srv, path, cookies))

			_, strangerCookies, _ := createUserAndLogin(t, srv, db, "job_stranger")
			if rec := getWithCookies(t, srv, path, strangerCookies); rec.Code != http.StatusForbidden {
				t.Fatalf("non-admin GET %s = %d, want 403", path, rec.Code)
			}

		})
	}
}

// TestAdminJobsJSON asserts the shape of the jobs JSON: with no runner wired up it returns an empty list rather than 500.
func TestAdminJobsJSON(t *testing.T) {
	srv, _, _, cookies, _ := newNotesServer(t)

	rec := getJSON(t, srv, "/api/v1/admin/jobs", cookies, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET jobs = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var got adminJobsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode jobs: %v", err)
	}
	if got.Jobs == nil || len(got.Jobs) != 0 || got.Total != 0 || got.Page != 1 {
		t.Fatalf("empty jobs response = %+v, want empty list on page 1", got)
	}
}

// TestAdminI18nJSON asserts that the language pack JSON report faithfully carries each locale's coverage and missing keys.
func TestAdminI18nJSON(t *testing.T) {
	srv, _, _, cookies, _ := newNotesServer(t)
	srv.coverageOverride = func() []i18n.LocaleCoverage {
		return []i18n.LocaleCoverage{
			{Code: "en", Total: 3, Present: 3},
			{Code: "zh-CN", Total: 3, Present: 2, Missing: []string{"a.three"}},
		}
	}

	rec := getJSON(t, srv, "/api/v1/admin/i18n", cookies, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET i18n = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var got adminI18nResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode i18n: %v", err)
	}
	if got.AllComplete {
		t.Error("all_complete = true, want false with a missing key")
	}
	if len(got.Locales) != 2 {
		t.Fatalf("locales = %d, want 2", len(got.Locales))
	}
	zh := got.Locales[1]
	if zh.Code != "zh-CN" || zh.Complete || zh.Percent != 66 || len(zh.Missing) != 1 || zh.Missing[0] != "a.three" {
		t.Errorf("zh coverage = %+v, want 66%% incomplete with a.three missing", zh)
	}
	if !got.Locales[0].Complete || got.Locales[0].Percent != 100 {
		t.Errorf("en coverage = %+v, want 100%% complete", got.Locales[0])
	}
}
