package web

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"path"
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
	indexHTML []byte
	indexHash string
	indexETag string
	assets    map[string]*spaAsset
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

// HasAsset 报告指定逻辑路径的静态资源是否存在。
func (s *SPA) HasAsset(logical string) bool {
	clean := strings.TrimPrefix(logical, "/")
	_, ok := s.assets[clean]
	return ok
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
