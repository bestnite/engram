package web

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
)

// staticFS 把 internal/web/static 下的全部资源打进二进制。
// 用 all: 前缀，以便以后可能出现的点文件或下划线文件也一并嵌入。
//
// 注意：CSS 产物 tailwind.css 与生成的 *_templ.go 一样是 gitignore 的，构建前必须按
// AGENTS.md §4 的步骤先生成；这里对缺失资源不报错，只让对应 URL 为空串，由模板跳过引用。
//
//go:embed all:static
var staticFS embed.FS

// staticPathPrefix 是内容哈希 URL 的前缀，形如 /static/v/<hash>/<逻辑路径>。
const staticPathPrefix = "/static/v/"

// asset 是一个已嵌入资源的元数据。
type asset struct {
	logical string
	hash    string
	url     string
	data    []byte
}

// Assets 是资源清单：逻辑路径 → 哈希化 URL，同时负责按 URL 提供内容。
type Assets struct {
	files map[string]*asset
}

// LoadAssets 遍历嵌入的 static 目录建立清单。
func LoadAssets() (*Assets, error) {
	a := &Assets{files: make(map[string]*asset)}
	err := fs.WalkDir(staticFS, "static", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		data, err := staticFS.ReadFile(p)
		if err != nil {
			return err
		}
		logical := strings.TrimPrefix(p, "static/")
		a.files[logical] = newAsset(logical, data)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("web: load static assets: %w", err)
	}
	return a, nil
}

// newAsset 计算内容哈希并拼出访问路径。
// 哈希取 sha256 前 4 字节（8 位十六进制）：在单次部署内足以区分内容，且 URL 足够短。
func newAsset(logical string, data []byte) *asset {
	sum := sha256.Sum256(data)
	h := hex.EncodeToString(sum[:4])
	return &asset{
		logical: logical,
		hash:    h,
		url:     staticPathPrefix + h + "/" + logical,
		data:    data,
	}
}

// Has 报告某逻辑路径的资源是否已嵌入（例如 tailwind.css 是否已生成）。
func (a *Assets) Has(logical string) bool {
	_, ok := a.files[logical]
	return ok
}

// ContentHash 返回逻辑路径对应资源的内容哈希；未知资源返回空串。
func (a *Assets) ContentHash(logical string) string {
	if f, ok := a.files[logical]; ok {
		return f.hash
	}
	return ""
}

// body 返回已嵌入资源的原始内容（UTF-8 文本）；未知资源返回空串。
// 供稳定 URL 路由（如 /pwa.js）复用同一份嵌入内容。
func (a *Assets) body(logical string) string {
	if f, ok := a.files[logical]; ok {
		return string(f.data)
	}
	return ""
}

// URL 返回逻辑路径对应的内容哈希 URL；资源不存在时返回空串，模板据此跳过引用。
func (a *Assets) URL(logical string) string {
	if f, ok := a.files[logical]; ok {
		return f.url
	}
	return ""
}

// Serve 处理 /static/v/:hash/*filepath：哈希与当前内容不一致时返回 404，避免旧 URL
// 命中新内容造成缓存错乱；命中则按不可变资源返回，浏览器可长期缓存。
func (a *Assets) Serve(c *gin.Context) {
	logical := strings.TrimPrefix(c.Param("filepath"), "/")
	f, ok := a.files[logical]
	if !ok || f.hash != c.Param("hash") {
		c.Status(http.StatusNotFound)
		return
	}
	contentType := mime.TypeByExtension(path.Ext(logical))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	c.Header("ETag", `"`+f.hash+`"`)
	c.Data(http.StatusOK, contentType, f.data)
}
