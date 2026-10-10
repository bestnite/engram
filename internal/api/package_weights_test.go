package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是 FSRS 权重导出 / 导入开关的 REST 端验收：include_weights 查询参数与
// apply_weights 表单字段都必须真的到达 store 层，且缺省时都按「不带、不用」处理。

// weightsJSON 是 21 维的非默认权重，只用来判断有没有被带出或采用。
const weightsJSON = `[0.5,0.51,0.52,0.53,0.54,0.55,0.56,0.57,0.58,0.59,0.6,0.61,0.62,0.63,0.64,0.65,0.66,0.67,0.68,0.69,0.7]`

// readPackageEntry 从 zip 字节里读出一个条目。
func readPackageEntry(t *testing.T, raw []byte, name string) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("open package zip: %v", err)
	}
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", name, err)
		}
		defer rc.Close()
		body, err := io.ReadAll(rc)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return body
	}
	t.Fatalf("package has no %s entry", name)
	return nil
}

// TestExportPackageIncludeWeightsQuery 断言 GET /decks/:id/package 缺省不带权重，
// include_weights=1 时 preset.json 带出权重且 manifest 如实声明。
func TestExportPackageIncludeWeightsQuery(t *testing.T) {
	cases := []struct {
		name        string
		query       string
		wantWeights bool
	}{
		{"default omits weights", "", false},
		{"include_weights=0 omits weights", "?include_weights=0", false},
		{"include_weights=1 exports weights", "?include_weights=1", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t, 60, 60)
			user := seedUser(t, env.db, "pkg_w_"+safeName(tc.name), store.RoleUser)
			k := seedKey(t, env.keys, user.ID, []string{store.ScopeRead}, nil)
			d := seedDeck(t, env.db, user.ID)
			if err := env.db.Model(&store.Preset{}).Where("id = ?", d.PresetID).
				Updates(map[string]any{"weights_json": weightsJSON, "weights_review_count": 42}).Error; err != nil {
				t.Fatalf("seed weights: %v", err)
			}

			req := httptest.NewRequest(http.MethodGet, "/api/v1/decks/"+d.PublicID+"/package"+tc.query, nil)
			req.Header.Set("Authorization", "Bearer "+k.Plaintext)
			rec := httptest.NewRecorder()
			env.router().ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("export status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
			}

			var preset store.PackagePreset
			if err := json.Unmarshal(readPackageEntry(t, rec.Body.Bytes(), "preset.json"), &preset); err != nil {
				t.Fatalf("decode preset.json: %v", err)
			}
			var manifest store.PackageManifest
			if err := json.Unmarshal(readPackageEntry(t, rec.Body.Bytes(), "manifest.json"), &manifest); err != nil {
				t.Fatalf("decode manifest.json: %v", err)
			}
			if got := len(preset.Weights) == 21; got != tc.wantWeights {
				t.Fatalf("preset.json weights = %v, want present=%v", preset.Weights, tc.wantWeights)
			}
			if tc.wantWeights != (preset.WeightsReviewCount != nil) {
				t.Fatalf("weights_review_count = %v, want present=%v", preset.WeightsReviewCount, tc.wantWeights)
			}
			if manifest.IncludeWeights != tc.wantWeights {
				t.Fatalf("manifest.include_weights = %v, want %v", manifest.IncludeWeights, tc.wantWeights)
			}
		})
	}
}

// TestImportPackageApplyWeightsForm 断言 POST /decks/import 缺省不采用包内权重，
// apply_weights=1 才写进新预设，响应里的 weights_applied / weights_discarded 与之一致。
func TestImportPackageApplyWeightsForm(t *testing.T) {
	raw := packageZip(t, map[string]string{
		"manifest.json": `{"format_version":1,"exported_at":"2026-10-02T00:00:00Z","deck":{"name":"imported"},"include_progress":false,"include_media":false,"include_reviews":false,"include_weights":true,"counts":{"notes":1,"cards":1}}`,
		"notes.json":    `[{"kind":"basic","fields":{"front":"q","back":"a"}}]`,
		"cards.json":    `[]`,
		"preset.json":   `{"desired_retention":0.9,"learning_steps":"1m","relearning_steps":"10m","maximum_interval_days":100,"enable_fuzz":true,"weights":` + weightsJSON + `,"weights_optimized_at":"2026-09-01T08:00:00Z","weights_review_count":42}`,
	})
	cases := []struct {
		name          string
		applyWeights  string
		wantApplied   bool
		wantDiscarded bool
	}{
		{"field omitted keeps defaults", "", false, true},
		{"apply_weights=0 keeps defaults", "0", false, true},
		{"apply_weights=1 applies", "1", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t, 60, 60)
			user := seedUser(t, env.db, "pkg_aw_"+safeName(tc.name), store.RoleUser)
			k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)

			var body bytes.Buffer
			w := multipart.NewWriter(&body)
			fw, err := w.CreateFormFile("file", "weights.edeck")
			if err != nil {
				t.Fatalf("create form file: %v", err)
			}
			if _, err := fw.Write(raw); err != nil {
				t.Fatalf("write form file: %v", err)
			}
			if tc.applyWeights != "" {
				if err := w.WriteField("apply_weights", tc.applyWeights); err != nil {
					t.Fatalf("write apply_weights: %v", err)
				}
			}
			if err := w.Close(); err != nil {
				t.Fatalf("close multipart writer: %v", err)
			}
			rec := postImport(t, env, k.Plaintext, &body, w.FormDataContentType())
			if rec.Code != http.StatusOK {
				t.Fatalf("import status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
			}
			var report store.PackageImportReport
			if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
				t.Fatalf("decode report: %v", err)
			}
			if report.WeightsApplied != tc.wantApplied || report.WeightsDiscarded != tc.wantDiscarded {
				t.Fatalf("weights_applied/discarded = %v/%v, want %v/%v",
					report.WeightsApplied, report.WeightsDiscarded, tc.wantApplied, tc.wantDiscarded)
			}

			deck, err := store.NewDeckStore(env.db).ByPublicID(t.Context(), report.DeckPublicID)
			if err != nil {
				t.Fatalf("load imported deck: %v", err)
			}
			var preset store.Preset
			if err := env.db.First(&preset, deck.PresetID).Error; err != nil {
				t.Fatalf("load imported preset: %v", err)
			}
			gotWeights := preset.WeightsJSON != nil && strings.Contains(*preset.WeightsJSON, "0.7")
			if gotWeights != tc.wantApplied {
				t.Fatalf("imported preset weights_json = %v, want present=%v", preset.WeightsJSON, tc.wantApplied)
			}
			if (preset.WeightsReviewCount != nil) != tc.wantApplied {
				t.Fatalf("imported preset weights_review_count = %v, want present=%v", preset.WeightsReviewCount, tc.wantApplied)
			}
		})
	}
}
