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

// spaAsset 是一个已嵌入的 SPA 静态资源元数据。
type spaAsset struct {
	path        string
	hash        string
	etag        string
	contentType string
	data        []byte
}

// SPA 管理嵌入的 Svelte SPA 生产构建产物。
type SPA struct {
	fs        fs.FS
	rawIndex  []byte
	indexHTML []byte
	// mathjaxURL 是自托管 MathJax 的内容哈希 URL；为空表示未注入（资源缺失或未调用 SetMathJaxURL）。
	mathjaxURL string
	// version 是注入入口 <head> 的程序版本号；为空表示不注入，前端页脚随之不显示版本。
	version string
	// shellBlock 是注入入口 <head> 的 PWA 外壳标记（manifest/theme-color/图标/pwa.js/主题引导）。
	// 它是装配期设定的一次性内容；之后每个请求只重写 <html> 的 lang 属性，不改动这块标记。
	shellBlock []byte
	assets     map[string]*spaAsset
}

// LoadSPA 从 frontend 嵌入文件系统加载生产构建产物。
func LoadSPA() (*SPA, error) {
	subFS, err := frontend.FS()
	if err != nil {
		return nil, fmt.Errorf("web: load SPA: %w", err)
	}
	return NewSPA(subFS)
}

// NewSPA 从指定的 fs.FS 构造 SPA 资源服务实例（支持依赖注入与单元测试）。
func NewSPA(subFS fs.FS) (*SPA, error) {
	indexData, err := fs.ReadFile(subFS, "index.html")
	if err != nil {
		return nil, fmt.Errorf("web: read spa index.html: %w", err)
	}
	if len(indexData) == 0 {
		return nil, fmt.Errorf("web: spa index.html is empty")
	}

	spa := &SPA{
		fs:        subFS,
		rawIndex:  indexData,
		indexHTML: indexData,
		assets:    make(map[string]*spaAsset),
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

		asset := &spaAsset{
			path:        cleanPath,
			hash:        h,
			etag:        etag,
			contentType: ct,
			data:        data,
		}
		spa.assets[cleanPath] = asset
		if strings.HasPrefix(cleanPath, "assets/") {
			spa.assets[strings.TrimPrefix(cleanPath, "assets/")] = asset
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("web: walk spa assets: %w", err)
	}

	return spa, nil
}

// mathjaxMetaName 是 SPA 入口 <head> 里承载自托管 MathJax 内容哈希 URL 的 meta 名。
// 前端加载器按它读取 URL，再用动态 <script src> 引入同源脚本——CSP 的 script-src 'self'
// 已放行同源外链脚本，无需内联脚本、nonce 或 'unsafe-inline'。
const mathjaxMetaName = "engram-mathjax"

// versionMetaName 是 SPA 入口 <head> 里承载程序版本的 meta 名；前端页脚读它显示版本。
const versionMetaName = "engram-version"

// SetMathJaxURL 把自托管 MathJax 的内容哈希 URL 注入 SPA 入口的 <head>。
//
// 传空串或资源未嵌入时不注入，前端加载器读到空 URL 会跳过加载（与 SSR 缺资源时跳过引用
// 一致）。入口内容随注入变化，重建后 ServeIndex 按实际写出的内容重算 ETag，浏览器不会
// 拿旧引用配新资源。
func (s *SPA) SetMathJaxURL(url string) {
	if url == s.mathjaxURL {
		return
	}
	s.mathjaxURL = url
	s.rebuild()
}

// SetVersion 把程序版本注入 SPA 入口的 <head>，供前端页脚显示。
//
// 传空串时不注入（测试，或未注入版本的构建），前端读到空值即不显示版本。与 SetMathJaxURL
// 一样，注入变化会重建入口内容，ServeIndex 按实际写出的字节现算 ETag，浏览器拿不到
// 「旧壳 + 新版本」的组合。
func (s *SPA) SetVersion(version string) {
	if version == s.version {
		return
	}
	s.version = version
	s.rebuild()
}

// SPAShell 是注入 SPA 入口 <head> 的 PWA 外壳元素。
//
// 这些值在装配期解析：manifest 与 /pwa.js 是稳定 URL，图标走内容哈希 URL，主题引导是
// 与 SSR 共用的同一常量。任何字段为空即跳过对应标记——资源缺失时与 SSR 一样不引用，
// 而不是给出一条空 href。此处不含用户文案：manifest 的应用名由 /manifest.webmanifest
// 端点按 settings 渲染，这里只负责引用它。
type SPAShell struct {
	// ManifestURL 是 manifest 的稳定路径（/manifest.webmanifest）。
	ManifestURL string
	// ThemeColor 是初始 theme-color 内容；主题引导会随后按明暗同步它。
	ThemeColor string
	// IconURL 是内容哈希的 SVG 图标（/static/v/<hash>/icons/icon.svg）。
	IconURL string
	// AppleTouchIconURL 是内容哈希的 apple-touch-icon。
	AppleTouchIconURL string
	// ScriptURL 是稳定 URL 的 service worker 注册脚本（/pwa.js）。
	ScriptURL string
	// ThemeBootstrap 是内联主题引导（自带 <script> 标签）。它必须与 SSR 完全同源：CSP 的
	// script-src hash 白名单按同一个编译期常量计算，复用即无需放宽策略。
	ThemeBootstrap string
}

// SetShell 注入 PWA 外壳标记：manifest、theme-color、图标、注册脚本与主题引导。
// 与 SetMathJaxURL 一样，注入会重建入口内容。
func (s *SPA) SetShell(shell SPAShell) {
	block := buildShellBlock(shell)
	if bytes.Equal(block, s.shellBlock) {
		return
	}
	s.shellBlock = block
	s.rebuild()
}

// buildShellBlock 拼出注入 <head> 的外壳标记。属性值一律转义：图标/脚本 URL 来自装配期
// 解析，将来若含引号也不会破坏文档结构。theme-color 必须排在主题引导之前——引导会同步该
// meta 的内容，顺序反了首帧会读到旧颜色。
func buildShellBlock(shell SPAShell) []byte {
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

// rebuild 依据当前注入项（MathJax meta、版本 meta 与 PWA 外壳块）重建入口内容。
// 每个注入点都从这里出发，避免各自基于 rawIndex 组装而丢掉其余注入。ETag 不在这里算：
// ServeIndex 按每次实际写出的内容（含请求语言）现算。
func (s *SPA) rebuild() {
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

// indexHashOf 取内容 SHA-256 的前 8 字节十六进制，作为入口 ETag 的强校验值。
func indexHashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// injectMathJaxMeta 在 </head> 之前插入一行 meta；找不到 </head> 时退化为追加到文末，
// 保证 meta 一定出现在文档里——前端加载器全靠它发现 MathJax。
func injectMathJaxMeta(index []byte, url string) []byte {
	meta := []byte(`<meta name="` + mathjaxMetaName + `" content="` + html.EscapeString(url) + `" />`)
	return injectBeforeHeadEnd(index, meta)
}

// injectVersionMeta 在 </head> 之前插入承载程序版本的 meta。值来自构建期注入，仍走
// html.EscapeString 转义，避免将来版本串含引号时破坏文档结构。
func injectVersionMeta(index []byte, version string) []byte {
	meta := []byte(`<meta name="` + versionMetaName + `" content="` + html.EscapeString(version) + `" />`)
	return injectBeforeHeadEnd(index, meta)
}

// injectBeforeHeadEnd 把一段标记插到 </head> 之前；找不到 </head> 时追加到文末，保证标记
// 一定出现在文档里。
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
	// htmlOpenTagRe 匹配入口的 <html ...> 起始标签（属性里不含 >）。
	htmlOpenTagRe = regexp.MustCompile(`(?i)<html(\s[^>]*)?>`)
	// htmlLangAttrRe 匹配起始标签里的 lang 属性（值用双引号，与构建产物一致）。
	htmlLangAttrRe = regexp.MustCompile(`(?i)\slang\s*=\s*"[^"]*"`)
)

// setHTMLLang 把入口 <html> 起始标签的 lang 改成给定语言码；没有该属性时补上。语言码由
// 本地化器给出（zh-CN/en 等 ASCII 值），仍走 html.EscapeString 转义，避免将来语言码含引号
// 时破坏属性。找不到 <html> 标签时原样返回。
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

// requestLocale 取请求已解析的语言码，用于入口 <html lang>：与 SSR 外壳同源，遵循
// ?lang > 用户设置 > Accept-Language > 站点默认 的优先级。本地化器
// 缺失时（未挂语言中间件的测试）回退到站点默认语言。
func requestLocale(c *gin.Context) string {
	if loc := i18n.FromContext(c.Request.Context()); loc != nil {
		return loc.Locale()
	}
	return i18n.DefaultLocaleCode
}

// HasAsset 报告指定逻辑路径的静态资源是否存在。
func (s *SPA) HasAsset(logical string) bool {
	clean := strings.TrimPrefix(logical, "/")
	_, ok := s.assets[clean]
	return ok
}

// StaticShellURLs 返回嵌入构建中可安全预缓存的 Vite 脚本与样式表。
func (s *SPA) StaticShellURLs() []string {
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

// IndexHTML 返回 index.html 的原始内容。
func (s *SPA) IndexHTML() []byte {
	return s.indexHTML
}

// ServeAsset 提供 Vite 哈希静态资源访问（/assets/*filepath）。
// 命中时返回不可变长效缓存；缺失时返回 404，严禁回退 index.html。
func (s *SPA) ServeAsset(c *gin.Context) {
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

// ServeIndex 提供 SPA HTML 入口，使用 revalidation/no-cache 避免长期缓存旧引用。
//
// 每个请求按已解析语言重写入口的 <html lang>，与 SSR 外壳同源。因为
// 响应体随语言变化，ETag 也按实际写出的内容计算——否则换语言后会命中旧的 304 缓存。
func (s *SPA) ServeIndex(c *gin.Context) {
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
