package web

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestLoadAssetsBuildsContentHashedURLs asserts embedded assets have content-hashed paths and that the hash is reproducible.
func TestLoadAssetsBuildsContentHashedURLs(t *testing.T) {
	assets, err := LoadAssets()
	if err != nil {
		t.Fatalf("LoadAssets() error = %v", err)
	}
	for _, logical := range []string{"js/mathjax/tex-svg.js", "js/pwa.js", "icons/icon.svg"} {
		if !assets.Has(logical) {
			t.Fatalf("asset %q is not embedded", logical)
		}
		hash := assets.ContentHash(logical)
		if len(hash) != 8 {
			t.Errorf("ContentHash(%q) = %q, want 8 hex chars", logical, hash)
		}
		want := staticPathPrefix + hash + "/" + logical
		if got := assets.URL(logical); got != want {
			t.Errorf("URL(%q) = %q, want %q", logical, got, want)
		}
	}
	if assets.URL("js/does-not-exist.js") != "" {
		t.Errorf("URL() of a missing asset should be empty")
	}
}

// TestAssetHashChangesWithContent is an acceptance point: when the content changes, the hash must change with it.
func TestAssetHashChangesWithContent(t *testing.T) {
	first := newAsset("js/app.js", []byte("console.log(1)"))
	same := newAsset("js/app.js", []byte("console.log(1)"))
	changed := newAsset("js/app.js", []byte("console.log(2)"))

	if first.hash != same.hash {
		t.Errorf("hash is not deterministic: %q != %q", first.hash, same.hash)
	}
	if first.hash == changed.hash {
		t.Errorf("hash did not change when content changed: both %q", first.hash)
	}
	if first.url == changed.url {
		t.Errorf("hashed URL did not change when content changed: both %q", first.url)
	}
}

// TestServeRejectsWrongHashAndUnknownFile covers the negative cases of the static asset route.
func TestServeRejectsWrongHashAndUnknownFile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	assets, err := LoadAssets()
	if err != nil {
		t.Fatalf("LoadAssets() error = %v", err)
	}
	router := gin.New()
	router.GET(staticPathPrefix+":hash/*filepath", assets.Serve)

	validHash := assets.ContentHash("js/pwa.js")
	cases := []struct {
		name string
		path string
	}{
		{"wrong hash", staticPathPrefix + "deadbeef/js/pwa.js"},
		{"unknown file with valid hash", staticPathPrefix + validHash + "/js/nope.js"},
		{"no file part", staticPathPrefix + validHash + "/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if rec.Code != http.StatusNotFound {
				t.Errorf("GET %s status = %d, want 404", tc.path, rec.Code)
			}
		})
	}
}

// TestServeReturnsAssetWithImmutableCaching asserts a hash-matching asset can be cached long-term.
func TestServeReturnsAssetWithImmutableCaching(t *testing.T) {
	gin.SetMode(gin.TestMode)
	assets, err := LoadAssets()
	if err != nil {
		t.Fatalf("LoadAssets() error = %v", err)
	}
	router := gin.New()
	router.GET(staticPathPrefix+":hash/*filepath", assets.Serve)

	want, err := staticFS.ReadFile("static/js/pwa.js")
	if err != nil {
		t.Fatalf("read embedded pwa script: %v", err)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, assets.URL("js/pwa.js"), nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !bytes.Equal(rec.Body.Bytes(), want) {
		t.Error("served body does not match the embedded asset")
	}
	if cacheControl := rec.Header().Get("Cache-Control"); !strings.Contains(cacheControl, "immutable") {
		t.Errorf("Cache-Control = %q, want it to contain immutable", cacheControl)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("Content-Type = %q, want a javascript type", ct)
	}
}
