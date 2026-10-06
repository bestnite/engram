package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/store"
)

// PWA 外壳（M8-2，DESIGN.md §8.5）。
//
// 与内容哈希化的静态资源不同，manifest 与 service worker 必须是稳定 URL：
//   - service worker 的默认作用域由脚本路径决定，放在 /static/v/<hash>/ 下只能控制
//     该前缀；要控制整站必须从根路径提供。哈希化路径也意味着每次构建 URL 都变，
//     浏览器不会更新旧的注册。
//   - manifest 的应用名/描述来自 settings 表，且图标 URL 带内容哈希，无法预先静态生成。
//
// 因此这两个资源由 handler 动态渲染。静态外壳清单（内容哈希 URL）由 Assets 提供，
// service worker 的缓存版本号取自该清单的内容，清单变化即触发旧缓存清理。
const (
	manifestPath      = "/manifest.webmanifest"
	serviceWorkerPath = "/sw.js"
	pwaScriptPath     = "/pwa.js"
)

// pwaShellAssets 是“应用外壳”的逻辑路径：CSS、交互脚本、数学渲染与图标。
// 只包含静态资源，不含任何答题数据或 API 端点（M8-2 验收：缓存里没有 API 响应）。
var pwaShellAssets = []string{
	"css/tailwind.css",
	"js/htmx.min.js",
	"js/review.js",
	"js/mathjax/tex-svg.js",
	"icons/icon.svg",
	"icons/icon-maskable.svg",
	"icons/apple-touch-icon.png",
}

// registerPWARoutes 挂载 PWA 外壳路由。这些路由是公开的：manifest 与 service worker
// 必须在登录前就能取到，否则“添加到主屏幕”在首页不可用。
func (s *Server) registerPWARoutes(router *gin.Engine) {
	router.GET(manifestPath, s.webManifest)
	router.GET(serviceWorkerPath, s.serviceWorker)
	router.GET(pwaScriptPath, s.servePWAScript)
	// /favicon.ico 不能 404（DESIGN.md §8.5）：浏览器对根路径 favicon 的探测是惯例，
	// 重定向到哈希化的 SVG 图标即可；保持公开 GET、无鉴权。
	router.GET("/favicon.ico", s.favicon)
}

// favicon 把 /favicon.ico 重定向到内容哈希化的 SVG 图标（M8-7）。
// 图标未嵌入时回 404，与其它静态资源一致，而不是给出一个空响应。
func (s *Server) favicon(c *gin.Context) {
	icon := s.assets.URL("icons/icon.svg")
	if icon == "" {
		c.Status(http.StatusNotFound)
		return
	}
	c.Redirect(http.StatusFound, icon)
}

// manifestIcon 是 manifest 里的一项图标声明。
type manifestIcon struct {
	Src     string `json:"src"`
	Sizes   string `json:"sizes"`
	Type    string `json:"type"`
	Purpose string `json:"purpose,omitempty"`
}

// webManifestBody 是 manifest.webmanifest 的 JSON 结构。
type webManifestBody struct {
	Name            string         `json:"name"`
	ShortName       string         `json:"short_name"`
	Description     string         `json:"description"`
	StartURL        string         `json:"start_url"`
	Scope           string         `json:"scope"`
	Display         string         `json:"display"`
	BackgroundColor string         `json:"background_color"`
	ThemeColor      string         `json:"theme_color"`
	Icons           []manifestIcon `json:"icons"`
}

// webManifest 渲染 manifest：应用名/描述优先取 settings 表的 site.name / site.description，
// 缺省回退到已有的站点名语言包 key（app.name），不新增语言包文案。
func (s *Server) webManifest(c *gin.Context) {
	loc := i18n.FromContext(c.Request.Context())
	if loc == nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	name, description := s.pwaIdentity(c, loc)

	body := webManifestBody{
		Name:            name,
		ShortName:       name,
		Description:     description,
		StartURL:        "/",
		Scope:           "/",
		Display:         "standalone",
		BackgroundColor: "#f8fafc",
		ThemeColor:      "#2563eb",
	}
	if icon := s.assets.URL("icons/icon.svg"); icon != "" {
		body.Icons = append(body.Icons, manifestIcon{
			Src:     icon,
			Sizes:   "any",
			Type:    "image/svg+xml",
			Purpose: "any",
		})
	}
	// maskable 必须白底满幅（透明会被平台裁掉或补黑边）；自适应蓝白切换只给 any 图标。
	if maskable := s.assets.URL("icons/icon-maskable.svg"); maskable != "" {
		body.Icons = append(body.Icons, manifestIcon{
			Src:     maskable,
			Sizes:   "512x512",
			Type:    "image/svg+xml",
			Purpose: "maskable",
		})
	}

	c.Header("Content-Type", "application/manifest+json; charset=utf-8")
	c.Header("Cache-Control", "no-cache")
	c.Status(http.StatusOK)
	if err := json.NewEncoder(c.Writer).Encode(body); err != nil {
		s.logger.Error("render manifest failed", "error", err)
	}
}

// pwaIdentity 返回应用名与描述：settings 表覆盖优先，缺省用站点名 key。
func (s *Server) pwaIdentity(c *gin.Context, loc *i18n.Localizer) (name, description string) {
	name = loc.T("app.name")
	description = name
	if s.db == nil {
		return name, description
	}
	settings, err := store.LoadSettings(c.Request.Context(), s.db)
	if err != nil {
		// settings 表可能尚未迁移；缺省值已足够渲染 manifest。
		return name, description
	}
	if v := strings.TrimSpace(settings["site.name"]); v != "" {
		name = v
	}
	if v := strings.TrimSpace(settings["site.description"]); v != "" {
		description = v
	}
	return name, description
}

// serviceWorker 提供 /sw.js：缓存清单随构建变化，版本号与清理逻辑由此函数注入。
func (s *Server) serviceWorker(c *gin.Context) {
	urls := s.pwaShellURLs()
	c.Header("Content-Type", "application/javascript; charset=utf-8")
	// service worker 自身不可长期缓存，否则新版本永远不被取到。
	c.Header("Cache-Control", "no-cache")
	c.Header("Service-Worker-Allowed", "/")
	c.Status(http.StatusOK)
	if _, err := c.Writer.WriteString(serviceWorkerJS(shellVersion(urls), urls)); err != nil {
		s.logger.Error("write service worker failed", "error", err)
	}
}

// servePWAScript 提供 /pwa.js：稳定 URL 的注册脚本，所有页面（含未登录首页）都可引用。
func (s *Server) servePWAScript(c *gin.Context) {
	if s.assets == nil || !s.assets.Has("js/pwa.js") {
		c.Status(http.StatusNotFound)
		return
	}
	// 复用已嵌入资源；它同时也有内容哈希 URL，这里只是给基础模板一个稳定入口。
	c.Header("Cache-Control", "no-cache")
	c.Header("Content-Type", "application/javascript; charset=utf-8")
	c.Status(http.StatusOK)
	if _, err := c.Writer.WriteString(s.assets.body("js/pwa.js")); err != nil {
		s.logger.Error("write pwa script failed", "error", err)
	}
}

// pwaShellURLs 返回外壳资源的哈希化 URL（缺失资源跳过），末尾补上 manifest。
// manifest 也是静态外壳的一部分，但它不是 hash 化路径，单独追加。
func (s *Server) pwaShellURLs() []string {
	urls := make([]string, 0, len(pwaShellAssets)+1)
	for _, logical := range pwaShellAssets {
		if url := s.assets.URL(logical); url != "" {
			urls = append(urls, url)
		}
	}
	if s.spa != nil {
		urls = append(urls, s.spa.StaticShellURLs()...)
	}
	urls = append(urls, manifestPath)
	return urls
}

// shellVersion 由缓存清单内容派生版本号：清单变则版本变，activation 阶段据此清理旧缓存。
func shellVersion(urls []string) string {
	h := sha256.New()
	for _, u := range urls {
		h.Write([]byte(u))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)[:4])
}

// serviceWorkerJS 生成 service worker 脚本。缓存清单里的每一项都必须是静态外壳路径；
// 该函数不接收、也不可能写入 API 或答题端点（M8-2 的负面验收在测试里断言）。
func serviceWorkerJS(version string, urls []string) string {
	var b strings.Builder
	b.WriteString("// Engram service worker (M8-2)：只缓存静态外壳，绝不缓存 API 响应或答题数据。\n")
	b.WriteString("// 版本号由静态资源内容派生；激活时清理所有旧版本缓存。\n")
	b.WriteString("var SW_VERSION = \"")
	b.WriteString(version)
	b.WriteString("\";\n")
	b.WriteString("var STATIC_CACHE = \"engram-static-\" + SW_VERSION;\n")
	b.WriteString("var STATIC_ASSETS = [\n")
	for _, u := range urls {
		b.WriteString("  \"")
		b.WriteString(u)
		b.WriteString("\",\n")
	}
	b.WriteString("];\n\n")
	b.WriteString(serviceWorkerBody)
	return b.String()
}

// serviceWorkerBody 是脚本的固定部分（生命周期与 fetch 策略）。只对静态外壳做
// cache-first；页面 HTML、/api/ 与任何非 GET 请求一律走网络，不落缓存。
const serviceWorkerBody = `self.addEventListener("install", function (event) {
  event.waitUntil(
    caches.open(STATIC_CACHE).then(function (cache) {
      return cache.addAll(STATIC_ASSETS).catch(function () {});
    })
  );
  self.skipWaiting();
});

self.addEventListener("activate", function (event) {
  event.waitUntil(
    caches.keys().then(function (names) {
      return Promise.all(
        names
          .filter(function (name) {
            return name.indexOf("engram-static-") === 0 && name !== STATIC_CACHE;
          })
          .map(function (name) {
            return caches.delete(name);
          })
      );
    }).then(function () {
      return self.clients.claim();
    })
  );
});

// 只有静态外壳命中缓存；/api/、/review 与 HTML 文档都不在此列。
function isStaticShell(url) {
  var path = url.pathname;
  var isViteAsset = path.indexOf("/assets/") === 0 &&
    (path.endsWith(".js") || path.endsWith(".mjs") || path.endsWith(".css"));
  return path.indexOf("/static/") === 0 || isViteAsset || path === "/manifest.webmanifest";
}

self.addEventListener("fetch", function (event) {
  var req = event.request;
  if (req.method !== "GET") {
    return;
  }
  var url = new URL(req.url);
  if (url.origin !== self.location.origin || !isStaticShell(url)) {
    return;
  }
  event.respondWith(
    caches.match(req).then(function (cached) {
      if (cached) {
        return cached;
      }
      return fetch(req).then(function (res) {
        if (res && res.ok) {
          var copy = res.clone();
          caches.open(STATIC_CACHE).then(function (cache) {
            cache.put(req, copy);
          });
        }
        return res;
      });
    })
  );
});
`
