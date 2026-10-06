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
	"sort"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/frontend"
)

// spaAsset 是一个已嵌入的 SPA 静态资源元数据（DESIGN.md §8.5）。
type spaAsset struct {
	path        string
	hash        string
	etag        string
	contentType string
	data        []byte
}

// SPA 管理嵌入的 Svelte SPA 生产构建产物（DESIGN.md §8.5、§10.2）。
type SPA struct {
	fs        fs.FS
	rawIndex  []byte
	indexHTML []byte
	indexHash string
	indexETag string
	// mathjaxURL 是自托管 MathJax 的内容哈希 URL；为空表示未注入（资源缺失或未调用 SetMathJaxURL）。
	mathjaxURL string
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

	indexSum := sha256.Sum256(indexData)
	indexHash := hex.EncodeToString(indexSum[:8])
	indexETag := `"` + indexHash + `"`

	spa := &SPA{
		fs:        subFS,
		rawIndex:  indexData,
		indexHTML: indexData,
		indexHash: indexHash,
		indexETag: indexETag,
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
// 已放行同源外链脚本，无需内联脚本、nonce 或 'unsafe-inline'（DESIGN.md §6.1、§8.5、§11）。
const mathjaxMetaName = "engram-mathjax"

// SetMathJaxURL 把自托管 MathJax 的内容哈希 URL 注入 SPA 入口的 <head>。
//
// 传空串或资源未嵌入时不注入，前端加载器读到空 URL 会跳过加载（与 SSR 缺资源时跳过引用
// 一致）。注入后入口内容变化，ETag 必须重算，否则浏览器会拿旧引用配新资源（DESIGN.md §8.5）。
func (s *SPA) SetMathJaxURL(url string) {
	if url == s.mathjaxURL {
		return
	}
	s.mathjaxURL = url
	if url == "" {
		s.indexHTML = s.rawIndex
	} else {
		s.indexHTML = injectMathJaxMeta(s.rawIndex, url)
	}
	sum := sha256.Sum256(s.indexHTML)
	s.indexHash = hex.EncodeToString(sum[:8])
	s.indexETag = `"` + s.indexHash + `"`
}

// injectMathJaxMeta 在 </head> 之前插入一行 meta；找不到 </head> 时退化为追加到文末，
// 保证 meta 一定出现在文档里——前端加载器全靠它发现 MathJax。
func injectMathJaxMeta(index []byte, url string) []byte {
	meta := []byte(`<meta name="` + mathjaxMetaName + `" content="` + html.EscapeString(url) + `" />`)
	head := bytes.Index(index, []byte("</head>"))
	if head < 0 {
		out := make([]byte, 0, len(index)+len(meta))
		out = append(out, index...)
		return append(out, meta...)
	}
	out := make([]byte, 0, len(index)+len(meta))
	out = append(out, index[:head]...)
	out = append(out, meta...)
	out = append(out, index[head:]...)
	return out
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
// 命中时返回不可变长效缓存；缺失时返回 404，严禁回退 index.html（DESIGN.md §8.5）。
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

// ServeIndex 提供 SPA HTML 入口，使用 revalidation/no-cache 避免长期缓存旧引用（DESIGN.md §8.5）。
func (s *SPA) ServeIndex(c *gin.Context) {
	if c.GetHeader("If-None-Match") == s.indexETag {
		c.Status(http.StatusNotModified)
		return
	}

	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Header("Cache-Control", "no-cache, must-revalidate")
	c.Header("ETag", s.indexETag)
	c.Data(http.StatusOK, "text/html; charset=utf-8", s.indexHTML)
}
