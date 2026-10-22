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

// staticFS embeds all assets under internal/web/static into the binary.
// The all: prefix is used so that dot files or underscore files that may appear later are embedded too.
//
// Note: the CSS artifact tailwind.css is gitignored like the generated *_templ.go files, so before building it must be generated following
// the steps in AGENTS.md §4; a missing asset is not an error here, its URL simply becomes an empty string and templates skip referencing it.
//
//go:embed all:static
var staticFS embed.FS

// staticPathPrefix is the prefix of content-hashed URLs, of the form /static/v/<hash>/<logical path>.
const staticPathPrefix = "/static/v/"

// asset is the metadata of one embedded asset.
type asset struct {
	logical string
	hash    string
	url     string
	data    []byte
}

// Assets is the asset manifest: logical path → hashed URL, and it also serves content by URL.
type Assets struct {
	files map[string]*asset
}

// LoadAssets walks the embedded static directory to build the manifest.
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

// newAsset computes the content hash and assembles the access path.
// The hash takes the first 4 bytes of sha256 (8 hex digits): enough to distinguish content within a single deployment, while keeping the URL short.
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

// Has reports whether the asset for a logical path is embedded (for example whether tailwind.css has been generated).
func (a *Assets) Has(logical string) bool {
	_, ok := a.files[logical]
	return ok
}

// ContentHash returns the content hash of the asset for a logical path; returns an empty string for an unknown asset.
func (a *Assets) ContentHash(logical string) string {
	if f, ok := a.files[logical]; ok {
		return f.hash
	}
	return ""
}

// body returns the raw content of an embedded asset (UTF-8 text); returns an empty string for an unknown asset.
// Lets stable-URL routes (such as /pwa.js) reuse the same embedded content.
func (a *Assets) body(logical string) string {
	if f, ok := a.files[logical]; ok {
		return string(f.data)
	}
	return ""
}

// URL returns the content-hashed URL for a logical path; returns an empty string when the asset does not exist, and templates skip referencing it accordingly.
func (a *Assets) URL(logical string) string {
	if f, ok := a.files[logical]; ok {
		return f.url
	}
	return ""
}

// Serve handles /static/v/:hash/*filepath: a hash that does not match the current content returns 404, preventing an old URL
// from hitting new content and scrambling caches; on a hit it is served as an immutable asset, which browsers may cache long-term.
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
