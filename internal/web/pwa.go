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

// PWA shell.
//
// Unlike content-hashed static assets, the manifest and service worker must live at stable URLs:
//   - the service worker's default scope comes from the script path, so putting it under /static/v/<hash>/ would only let it control
//     that prefix; controlling the whole site requires serving it from the root path. A hashed path also means the URL changes on every build, and
//     browsers would never update the old registration.
//   - the manifest's app name/description come from the settings table, and the icon URLs are content-hashed, so it cannot be pre-generated statically.
//
// So both resources are rendered dynamically by handlers. The static shell list (content-hashed URLs) is provided by Assets, and
// the service worker's cache version is derived from that list's content, so a change to the list triggers cleanup of the old caches.
const (
	manifestPath      = "/manifest.webmanifest"
	serviceWorkerPath = "/sw.js"
	pwaScriptPath     = "/pwa.js"
)

// pwaShellAssets is the logical path list of the "app shell": math rendering and icons (the SPA's own JS/CSS come from the Vite output,
// appended by StaticShellURLs).
// Only static assets, no review data or API endpoints at all (acceptance: no API responses in the cache).
var pwaShellAssets = []string{
	"js/mathjax/tex-chtml-full.js",
	"icons/icon.svg",
	"icons/icon-maskable.svg",
	"icons/apple-touch-icon.png",
}

// registerPWARoutes mounts the PWA shell routes. These routes are public: the manifest and service worker
// must be fetchable before sign-in, otherwise "add to home screen" would not work from the home page.
func (s *Server) registerPWARoutes(router *gin.Engine) {
	router.GET(manifestPath, s.webManifest)
	router.GET(serviceWorkerPath, s.serviceWorker)
	router.GET(pwaScriptPath, s.servePWAScript)
	// /favicon.ico must not 404: browsers probing the root favicon is a convention,
	// so redirecting to the hashed SVG icon is enough; keep it a public GET with no auth.
	router.GET("/favicon.ico", s.favicon)
}

// favicon redirects /favicon.ico to the content-hashed SVG icon.
// Returns 404 when the icon is not embedded, consistent with other static assets, rather than giving an empty response.
func (s *Server) favicon(c *gin.Context) {
	icon := s.assets.URL("icons/icon.svg")
	if icon == "" {
		c.Status(http.StatusNotFound)
		return
	}
	c.Redirect(http.StatusFound, icon)
}

// manifestIcon is one icon declaration in the manifest.
type manifestIcon struct {
	Src     string `json:"src"`
	Sizes   string `json:"sizes"`
	Type    string `json:"type"`
	Purpose string `json:"purpose,omitempty"`
}

// webManifestBody is the JSON structure of manifest.webmanifest.
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

// webManifest renders the manifest: app name/description come from the settings table's site.name / site.description when present,
// falling back to the existing site-name language-pack key (app.name), so no new language-pack copy is added.
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
	// maskable must be full-bleed on white (a transparent one gets cropped or given black bars by the platform); the adaptive blue/white switch goes to the any icon only.
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

// pwaIdentity returns the app name and description: the site name as mail.Composer resolves it, and the configured
// description or, when unset, the name again.
func (s *Server) pwaIdentity(c *gin.Context, loc *i18n.Localizer) (name, description string) {
	ctx := c.Request.Context()
	name = s.mailer.SiteName(ctx, loc)
	description = name
	if s.db == nil {
		return name, description
	}
	if settings, err := store.LoadSettings(ctx, s.db); err == nil {
		if v := strings.TrimSpace(settings[store.SettingSiteDescription]); v != "" {
			description = v
		}
	}
	return name, description
}

// serviceWorker serves /sw.js: the cache list changes with the build, and this function injects the version number and cleanup logic.
func (s *Server) serviceWorker(c *gin.Context) {
	urls := s.pwaShellURLs()
	c.Header("Content-Type", "application/javascript; charset=utf-8")
	// The service worker itself must not be cached long-term, otherwise a new version would never be picked up.
	c.Header("Cache-Control", "no-cache")
	c.Header("Service-Worker-Allowed", "/")
	c.Status(http.StatusOK)
	if _, err := c.Writer.WriteString(serviceWorkerJS(shellVersion(urls), urls)); err != nil {
		s.logger.Error("write service worker failed", "error", err)
	}
}

// servePWAScript serves /pwa.js: a registration script at a stable URL, referenceable from every page (including the signed-out home page).
func (s *Server) servePWAScript(c *gin.Context) {
	if s.assets == nil || !s.assets.Has("js/pwa.js") {
		c.Status(http.StatusNotFound)
		return
	}
	// Reuses the embedded asset; it also has a content-hashed URL, this just gives the base template a stable entry point.
	c.Header("Cache-Control", "no-cache")
	c.Header("Content-Type", "application/javascript; charset=utf-8")
	c.Status(http.StatusOK)
	if _, err := c.Writer.WriteString(s.assets.body("js/pwa.js")); err != nil {
		s.logger.Error("write pwa script failed", "error", err)
	}
}

// pwaShellURLs returns the hashed URLs of the shell assets (missing ones skipped), with the manifest appended at the end.
// The manifest is part of the static shell too, but it is not a hashed path, so it is appended separately.
func (s *Server) pwaShellURLs() []string {
	urls := make([]string, 0, len(pwaShellAssets)+1)
	for _, logical := range pwaShellAssets {
		if url := s.assets.URL(logical); url != "" {
			urls = append(urls, url)
		}
	}
	urls = append(urls, s.shell.StaticShellURLs()...)
	urls = append(urls, manifestPath)
	return urls
}

// shellVersion derives the version from the cache list's content: change the list and the version changes, and activation uses it to purge old caches.
func shellVersion(urls []string) string {
	h := sha256.New()
	for _, u := range urls {
		h.Write([]byte(u))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)[:4])
}

// serviceWorkerJS generates the service worker script. Every item in the cache list must be a static shell path;
// the function takes no API or review endpoints and cannot write them (the negative acceptance is asserted in tests).
func serviceWorkerJS(version string, urls []string) string {
	var b strings.Builder
	b.WriteString("// Engram service worker: caches only the static shell, never API responses or answer data.\n")
	b.WriteString("// The version is derived from static asset content; activation clears every old version cache.\n")
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

// serviceWorkerBody is the script's fixed part (lifecycle and fetch strategy). It is cache-first for static shell assets
// only; page HTML, /api/ and any non-GET request always go to the network and are never cached.
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

// Only the static shell is cached; /api/, /review and HTML documents are not covered.
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
