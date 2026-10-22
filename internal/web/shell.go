package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/frontend"
	"git.nite07.com/nite/engram/internal/i18n"
)

// shellAsset is the metadata of one embedded SPA static asset.
type shellAsset struct {
	path        string
	hash        string
	etag        string
	contentType string
	data        []byte
}

// SPA manages the embedded production build output of the Svelte SPA.
type Shell struct {
	fs        fs.FS
	rawIndex  []byte
	indexHTML []byte
	// mathjaxURL is the content-hashed URL of the self-hosted MathJax; empty means not injected (resource missing or SetMathJaxURL never called).
	mathjaxURL string
	// version is the program version injected into the entry <head>; empty means not injected, and the frontend footer then shows no version.
	version string
	// shellBlock is the PWA shell markup injected into the entry <head> (manifest/theme-color/icons/pwa.js/theme bootstrap).
	// It is content fixed once at wiring time; from then on each request only rewrites the <html> lang attribute and does not touch this markup.
	shellBlock []byte
	assets     map[string]*shellAsset
}

// LoadShell loads the production build output from the frontend embedded filesystem.
func LoadShell() (*Shell, error) {
	subFS, err := frontend.FS()
	if err != nil {
		return nil, fmt.Errorf("web: load Shell: %w", err)
	}
	return NewShell(subFS)
}

// NewShell builds an SPA asset server from the given fs.FS (supports dependency injection and unit tests).
func NewShell(subFS fs.FS) (*Shell, error) {
	indexData, err := fs.ReadFile(subFS, "index.html")
	if err != nil {
		return nil, fmt.Errorf("web: read shell index.html: %w", err)
	}
	if len(indexData) == 0 {
		return nil, fmt.Errorf("web: shell index.html is empty")
	}

	shell := &Shell{
		fs:        subFS,
		rawIndex:  indexData,
		indexHTML: indexData,
		assets:    make(map[string]*shellAsset),
	}

	err = fs.WalkDir(subFS, ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		cleanPath := strings.TrimPrefix(p, "./")
		cleanPath = strings.TrimPrefix(cleanPath, "/")
		if cleanPath == "index.html" {
			return nil
		}
		data, err := fs.ReadFile(subFS, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		h := hex.EncodeToString(sum[:8])
		etag := `"` + h + `"`
		ct := mime.TypeByExtension(path.Ext(cleanPath))
		if ct == "" {
			switch path.Ext(cleanPath) {
			case ".js", ".mjs":
				ct = "text/javascript; charset=utf-8"
			case ".css":
				ct = "text/css; charset=utf-8"
			case ".svg":
				ct = "image/svg+xml"
			case ".json":
				ct = "application/json"
			case ".woff2":
				ct = "font/woff2"
			case ".woff":
				ct = "font/woff"
			default:
				ct = "application/octet-stream"
			}
		}
		if (strings.HasPrefix(ct, "text/") || ct == "application/javascript") && !strings.Contains(ct, "charset") {
			ct += "; charset=utf-8"
		}

		asset := &shellAsset{
			path:        cleanPath,
			hash:        h,
			etag:        etag,
			contentType: ct,
			data:        data,
		}
		shell.assets[cleanPath] = asset
		if strings.HasPrefix(cleanPath, "assets/") {
			shell.assets[strings.TrimPrefix(cleanPath, "assets/")] = asset
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("web: walk shell assets: %w", err)
	}

	return shell, nil
}

// mathjaxMetaName is the name of the meta in the SPA entry <head> that carries the content-hashed URL of the self-hosted MathJax.
// The frontend loader reads the URL from it and pulls the same-origin script in with a dynamic <script src> — the CSP's script-src 'self'
// already allows same-origin external scripts, so no inline script, nonce or 'unsafe-inline' is needed.
const mathjaxMetaName = "engram-mathjax"

// versionMetaName is the name of the meta in the SPA entry <head> that carries the program version; the frontend footer reads it to show the version.
const versionMetaName = "engram-version"

// SetMathJaxURL injects the content-hashed URL of the self-hosted MathJax into the <head> of the SPA entry.
//
// An empty string, or a resource that is not embedded, injects nothing, and the frontend loader skips loading on an empty URL (consistent with SSR skipping the reference
// when the resource is missing). The entry content changes with the injection, and after the rebuild ServeIndex recomputes the ETag from what it actually writes, so the browser never
// pairs an old reference with new assets.
func (s *Shell) SetMathJaxURL(url string) {
	if url == s.mathjaxURL {
		return
	}
	s.mathjaxURL = url
	s.rebuild()
}

// SetVersion injects the program version into the <head> of the SPA entry, for the frontend footer to show.
//
// An empty string injects nothing (tests, or a build without an injected version), and the frontend shows no version on an empty value. As with SetMathJaxURL,
// an injection change rebuilds the entry content and ServeIndex recomputes the ETag from the bytes actually written, so the browser cannot end up with
// the "old shell + new version" combination.
func (s *Shell) SetVersion(version string) {
	if version == s.version {
		return
	}
	s.version = version
	s.rebuild()
}

// ShellHTML holds the PWA shell elements injected into the SPA entry <head>.
//
// These values are resolved at wiring time: the manifest and /pwa.js are stable URLs, icons use content-hashed URLs, and the theme bootstrap is
// the same constant SSR uses. An empty field skips the corresponding markup — when a resource is missing it is not referenced, just like SSR,
// rather than emitting an empty href. No user-facing copy lives here: the manifest's app name is rendered by the /manifest.webmanifest
// endpoint from settings, and this only references it.
type ShellHTML struct {
	// ManifestURL is the stable path of the manifest (/manifest.webmanifest).
	ManifestURL string
	// ThemeColor is the initial theme-color value; the theme bootstrap then syncs it to light/dark.
	ThemeColor string
	// IconURL is the content-hashed SVG icon (/static/v/<hash>/icons/icon.svg).
	IconURL string
	// AppleTouchIconURL is the content-hashed apple-touch-icon.
	AppleTouchIconURL string
	// ScriptURL is the service worker registration script at a stable URL (/pwa.js).
	ScriptURL string
	// ThemeBootstrap is the inline theme bootstrap (it carries the <script> tag itself). It must come from exactly the same source as SSR: the CSP's
	// script-src hash allowlist is computed from the same compile-time constant, so reusing it needs no policy widening.
	ThemeBootstrap string
}

// SetShell injects the PWA shell markup: manifest, theme-color, icons, registration script and theme bootstrap.
// As with SetMathJaxURL, injecting rebuilds the entry content.
func (s *Shell) SetShell(shell ShellHTML) {
	block := buildShellBlock(shell)
	if bytes.Equal(block, s.shellBlock) {
		return
	}
	s.shellBlock = block
	s.rebuild()
}

// buildShellBlock assembles the shell markup injected into <head>. Attribute values are always escaped: the icon/script URLs come from wiring-time
// resolution, so even if they carried quotes later the document structure would survive. theme-color must come before the theme bootstrap — the bootstrap syncs that
// meta's content, and in the reverse order the first frame reads the old color.
func buildShellBlock(shell ShellHTML) []byte {
	var b strings.Builder
	if shell.ThemeColor != "" {
		b.WriteString(`<meta name="theme-color" content="` + html.EscapeString(shell.ThemeColor) + `"/>`)
	}
	if shell.ThemeBootstrap != "" {
		b.WriteString(shell.ThemeBootstrap)
	}
	if shell.ManifestURL != "" {
		b.WriteString(`<link rel="manifest" href="` + html.EscapeString(shell.ManifestURL) + `"/>`)
	}
	if shell.IconURL != "" {
		b.WriteString(`<link rel="icon" type="image/svg+xml" href="` + html.EscapeString(shell.IconURL) + `"/>`)
	}
	if shell.AppleTouchIconURL != "" {
		b.WriteString(`<link rel="apple-touch-icon" href="` + html.EscapeString(shell.AppleTouchIconURL) + `"/>`)
	}
	if shell.ScriptURL != "" {
		b.WriteString(`<script src="` + html.EscapeString(shell.ScriptURL) + `"></script>`)
	}
	return []byte(b.String())
}

// rebuild rebuilds the entry content from the current injections (MathJax meta, version meta and PWA shell block).
// Every injection point starts here, so none of them assemble from rawIndex and drop the other injections. The ETag is not computed here:
// ServeIndex computes it from what is actually written each time (including the requested language).
func (s *Shell) rebuild() {
	index := s.rawIndex
	if s.mathjaxURL != "" {
		index = injectMathJaxMeta(index, s.mathjaxURL)
	}
	if s.version != "" {
		index = injectVersionMeta(index, s.version)
	}
	if len(s.shellBlock) > 0 {
		index = injectBeforeHeadEnd(index, s.shellBlock)
	}
	s.indexHTML = index
}

// indexHashOf takes the hex of the first 8 bytes of the content's SHA-256 as the strong validator for the entry ETag.
func indexHashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// injectMathJaxMeta inserts one meta line before </head>; when </head> is not found it degrades to appending at the end,
// guaranteeing the meta appears in the document — the frontend loader discovers MathJax entirely through it.
func injectMathJaxMeta(index []byte, url string) []byte {
	meta := []byte(`<meta name="` + mathjaxMetaName + `" content="` + html.EscapeString(url) + `" />`)
	return injectBeforeHeadEnd(index, meta)
}

// injectVersionMeta inserts the meta carrying the program version before </head>. The value comes from build-time injection and still goes through
// html.EscapeString escaping, so a future version string containing quotes cannot break the document structure.
func injectVersionMeta(index []byte, version string) []byte {
	meta := []byte(`<meta name="` + versionMetaName + `" content="` + html.EscapeString(version) + `" />`)
	return injectBeforeHeadEnd(index, meta)
}

// injectBeforeHeadEnd inserts a piece of markup before </head>; when </head> is not found it appends at the end, guaranteeing the markup
// does appear in the document.
func injectBeforeHeadEnd(index, snippet []byte) []byte {
	head := bytes.Index(index, []byte("</head>"))
	if head < 0 {
		out := make([]byte, 0, len(index)+len(snippet))
		out = append(out, index...)
		return append(out, snippet...)
	}
	out := make([]byte, 0, len(index)+len(snippet))
	out = append(out, index[:head]...)
	out = append(out, snippet...)
	out = append(out, index[head:]...)
	return out
}

var (
	// htmlOpenTagRe matches the entry's <html ...> opening tag (the attributes contain no >).
	htmlOpenTagRe = regexp.MustCompile(`(?i)<html(\s[^>]*)?>`)
	// htmlLangAttrRe matches the lang attribute in the opening tag (the value uses double quotes, as in the build output).
	htmlLangAttrRe = regexp.MustCompile(`(?i)\slang\s*=\s*"[^"]*"`)
)

// setHTMLLang changes the lang of the entry <html> opening tag to the given language code, adding the attribute when it is missing. The language code comes from
// the localizer (ASCII values such as zh-CN/en) and still goes through html.EscapeString escaping, so a future language code containing quotes cannot
// break the attribute. Returns the input unchanged when no <html> tag is found.
func setHTMLLang(index []byte, lang string) []byte {
	loc := htmlOpenTagRe.FindIndex(index)
	if loc == nil {
		return index
	}
	tag := string(index[loc[0]:loc[1]])
	attr := ` lang="` + html.EscapeString(lang) + `"`
	if htmlLangAttrRe.MatchString(tag) {
		tag = htmlLangAttrRe.ReplaceAllString(tag, attr)
	} else {
		tag = strings.TrimSuffix(tag, ">") + attr + ">"
	}
	out := make([]byte, 0, len(index)+len(attr))
	out = append(out, index[:loc[0]]...)
	out = append(out, tag...)
	out = append(out, index[loc[1]:]...)
	return out
}

// requestLocale takes the request's resolved language code, for the entry <html lang>: same source as the SSR shell, following the
// ?lang > user setting > Accept-Language > site default precedence. When the localizer
// is missing (tests without the locale middleware), it falls back to the site default language.
func requestLocale(c *gin.Context) string {
	if loc := i18n.FromContext(c.Request.Context()); loc != nil {
		return loc.Locale()
	}
	return i18n.DefaultLocaleCode
}

// StaticShellURLs returns the Vite scripts and stylesheets in the embedded build that are safe to precache.
func (s *Shell) StaticShellURLs() []string {
	urls := make([]string, 0, len(s.assets))
	for key, asset := range s.assets {
		if key != asset.path || !strings.HasPrefix(asset.path, "assets/") {
			continue
		}
		switch path.Ext(asset.path) {
		case ".js", ".mjs", ".css":
			urls = append(urls, "/"+asset.path)
		}
	}
	sort.Strings(urls)
	return urls
}

// IndexHTML returns the raw content of index.html.
func (s *Shell) IndexHTML() []byte {
	return s.indexHTML
}

// ServeAsset serves Vite hashed static assets (/assets/*filepath).
// A hit returns an immutable, long-lived cache; a miss returns 404, and falling back to index.html is strictly forbidden.
func (s *Shell) ServeAsset(c *gin.Context) {
	relPath := strings.TrimPrefix(c.Param("filepath"), "/")
	asset, ok := s.assets["assets/"+relPath]
	if !ok {
		asset, ok = s.assets[relPath]
	}
	if !ok {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}

	if c.GetHeader("If-None-Match") == asset.etag {
		c.Status(http.StatusNotModified)
		return
	}

	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	c.Header("ETag", asset.etag)
	c.Data(http.StatusOK, asset.contentType, asset.data)
}

// ServeIndex serves the SPA HTML entry, using revalidation/no-cache to avoid long-lived caching of stale references.
//
// Each request rewrites the entry's <html lang> per the resolved language, from the same source as the SSR shell. Because
// the response body varies with the language, the ETag is computed from what is actually written — otherwise a language switch would hit a stale 304 cache.
func (s *Shell) ServeIndex(c *gin.Context) {
	body := s.indexHTML
	if lang := requestLocale(c); lang != "" {
		body = setHTMLLang(body, lang)
	}
	etag := `"` + indexHashOf(body) + `"`

	if c.GetHeader("If-None-Match") == etag {
		c.Status(http.StatusNotModified)
		return
	}

	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Header("Cache-Control", "no-cache, must-revalidate")
	c.Header("ETag", etag)
	c.Data(http.StatusOK, "text/html; charset=utf-8", body)
}
