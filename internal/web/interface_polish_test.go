package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// 本文件是界面打磨里仍可从服务端核实、且不依赖 SSR 页面渲染的部分：favicon 重定向、
// SPA 应用壳声明的图标、manifest 与 service worker 的缓存清单。
// 页面级的导航/页脚/对话框观感已由 SPA 客户端路由承担，不再由服务端渲染。

// TestFaviconRedirectsToHashedIcon 是 (f) 的验收：/favicon.ico 不再 404，
// 而是重定向到内容哈希化的 SVG 图标。
func TestFaviconRedirectsToHashedIcon(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)
	rec := getWithCookies(t, srv, "/favicon.ico", nil)
	if rec.Code != http.StatusFound {
		t.Fatalf("GET /favicon.ico status = %d, want 302 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	loc := rec.Header().Get("Location")
	icon := srv.assets.URL("icons/icon.svg")
	if icon == "" {
		t.Fatal("icons/icon.svg is not embedded")
	}
	if loc != icon {
		t.Errorf("GET /favicon.ico Location = %q, want the hashed icon %q", loc, icon)
	}
	if !strings.HasPrefix(loc, staticPathPrefix) {
		t.Errorf("favicon target %q is not a content-hashed static path", loc)
	}
}

// TestPagesDeclareIconLinks 断言 SPA 应用壳在 <head> 声明 SVG favicon 与 apple-touch-icon，
// 两者都走内容哈希路径（(f)）。页面层只剩应用壳，图标由 server.go 注入入口。
func TestPagesDeclareIconLinks(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)
	icon := srv.assets.URL("icons/icon.svg")
	apple := srv.assets.URL("icons/apple-touch-icon.png")
	if icon == "" || apple == "" {
		t.Fatalf("icon assets are not embedded: icon=%q apple=%q", icon, apple)
	}

	body := get(t, srv, "/", nil).Body.String()
	if !strings.Contains(body, `rel="icon" type="image/svg+xml" href="`+icon+`"`) {
		t.Errorf("SPA shell does not declare the hashed SVG icon: %s", snippet(body))
	}
	if !strings.Contains(body, `rel="apple-touch-icon" href="`+apple+`"`) {
		t.Errorf("SPA shell does not declare the hashed apple-touch-icon: %s", snippet(body))
	}
}

// TestManifestDeclaresAdaptiveAndMaskableIcons 断言 manifest 声明两个图标：自适应 any 的
// icon.svg 与白底 maskable 的 icon-maskable.svg，且 maskable 走 512x512（(f)）。
func TestManifestDeclaresAdaptiveAndMaskableIcons(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)
	rec := getWithCookies(t, srv, manifestPath, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200", manifestPath, rec.Code)
	}
	var body struct {
		Icons []struct {
			Src, Sizes, Type, Purpose string
		} `json:"icons"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}

	anyIcon := srv.assets.URL("icons/icon.svg")
	maskableIcon := srv.assets.URL("icons/icon-maskable.svg")
	if anyIcon == "" || maskableIcon == "" {
		t.Fatalf("manifest icon assets are not embedded: any=%q maskable=%q", anyIcon, maskableIcon)
	}

	var haveAny, haveMaskable bool
	for _, ic := range body.Icons {
		if ic.Src == anyIcon && ic.Sizes == "any" && ic.Type == "image/svg+xml" && ic.Purpose == "any" {
			haveAny = true
		}
		if ic.Src == maskableIcon && ic.Sizes == "512x512" && ic.Type == "image/svg+xml" && ic.Purpose == "maskable" {
			haveMaskable = true
		}
	}
	if !haveAny {
		t.Errorf("manifest is missing the adaptive icon (any, any size): %+v", body.Icons)
	}
	if !haveMaskable {
		t.Errorf("manifest is missing the maskable icon (512x512): %+v", body.Icons)
	}
}

// TestServiceWorkerCachesIcons 断言两个图标与 apple-touch-icon 都进了外壳缓存清单（(f)）。
func TestServiceWorkerCachesIcons(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)
	body := getWithCookies(t, srv, serviceWorkerPath, nil).Body.String()
	for _, logical := range []string{"icons/icon.svg", "icons/icon-maskable.svg", "icons/apple-touch-icon.png"} {
		url := srv.assets.URL(logical)
		if url == "" {
			t.Fatalf("asset %q is not embedded", logical)
		}
		if !strings.Contains(body, `"`+url+`"`) {
			t.Errorf("service worker cache list is missing %q (%s)", logical, url)
		}
	}
}
