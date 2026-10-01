package web

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"example.com/flashcard/internal/media"
	"example.com/flashcard/internal/store"
)

// 媒体相关的系统设置键与对应的环境变量覆盖名（DESIGN.md §6.3、§8.4）。
// 环境变量 > settings 表 > 内置默认，与 internal/config 的优先级一致；这里直接读
// settings 表以便管理员改完立即生效，无需重启。
const (
	settingKeyMediaMaxBytes     = "media_max_bytes"
	settingKeyMediaAllowedMimes = "media_allowed_mimes"
	envMediaMaxBytes            = "MEDIA_MAX_BYTES"
	envMediaAllowedMimes        = "MEDIA_ALLOWED_MIMES"
)

// registerMediaRoutes 挂载媒体上传与读取（M2-8）。依赖未装配时跳过。
func (s *Server) registerMediaRoutes(router *gin.Engine) {
	if s.sessions == nil || s.media == nil {
		return
	}
	// 上传是写操作，过 CSRF 中间件；读取只要求登录（M5 授权落地前的最小鉴权）。
	router.POST("/media", s.sessions.CSRFMiddleware(), s.mediaUpload)
	router.GET("/media/:id", s.mediaServe)
}

// uploadLimit 解析生效的上传字节上限：环境变量 > settings 表 > 默认 10 MiB。
func (s *Server) uploadLimit(ctx context.Context) int64 {
	if raw := strings.TrimSpace(os.Getenv(envMediaMaxBytes)); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	if s.db != nil {
		if settings, err := store.LoadSettings(ctx, s.db); err == nil {
			if raw, ok := settings[settingKeyMediaMaxBytes]; ok {
				if n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); err == nil && n > 0 {
					return n
				}
			}
		}
	}
	return media.DefaultMaxBytes()
}

// allowedMimes 解析生效的 mime 白名单：环境变量 > settings 表 > 默认白名单。
func (s *Server) allowedMimes(ctx context.Context) []string {
	if raw := strings.TrimSpace(os.Getenv(envMediaAllowedMimes)); raw != "" {
		return splitMimeList(raw)
	}
	if s.db != nil {
		if settings, err := store.LoadSettings(ctx, s.db); err == nil {
			if raw, ok := settings[settingKeyMediaAllowedMimes]; ok {
				if list := splitMimeList(raw); len(list) > 0 {
					return list
				}
			}
		}
	}
	return media.DefaultAllowedMimes()
}

// splitMimeList 把逗号分隔的 mime 串切成去空白、去空项的小写列表。
func splitMimeList(raw string) []string {
	out := make([]string, 0, 4)
	for _, part := range strings.Split(raw, ",") {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// mediaUpload 处理 multipart 上传：保存到本地存储并返回元数据 JSON。
func (s *Server) mediaUpload(c *gin.Context) {
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		writeMediaError(c, http.StatusBadRequest, "media_missing_file", "no file part named file")
		return
	}
	defer file.Close()

	declared := ""
	if header != nil {
		declared = header.Header.Get("Content-Type")
	}
	saved, err := s.media.Save(ctx, file, media.SaveOptions{
		Limit:        s.uploadLimit(ctx),
		AllowedMimes: s.allowedMimes(ctx),
		DeclaredMime: declared,
		CreatedBy:    store.Ptr(user.ID),
	})
	if err != nil {
		status, code, msg := mediaErrorResponse(err)
		s.logger.Info("media upload rejected", "user_id", user.ID, "code", code, "error", err)
		writeMediaError(c, status, code, msg)
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionMediaUpload,
		TargetType: "media",
		TargetID:   store.Ptr(saved.ID),
		Detail:     map[string]any{"sha256": saved.Sha256, "mime": saved.Mime, "bytes": saved.Bytes},
	})
	c.JSON(http.StatusCreated, gin.H{
		"id":     saved.ID,
		"sha256": saved.Sha256,
		"mime":   saved.Mime,
		"bytes":  saved.Bytes,
		"url":    "/media/" + strconv.FormatUint(saved.ID, 10),
	})
}

// mediaErrorResponse 把存储层错误映射成 HTTP 状态码与稳定 code。
func mediaErrorResponse(err error) (status int, code, msg string) {
	var me *media.Error
	if errors.As(err, &me) {
		switch me.Code {
		case media.CodeTooLarge:
			return http.StatusRequestEntityTooLarge, me.Code, me.Msg
		case media.CodeMimeNotAllowed:
			return http.StatusUnsupportedMediaType, me.Code, me.Msg
		case media.CodeMagicMismatch:
			return http.StatusUnprocessableEntity, me.Code, me.Msg
		}
		return http.StatusBadRequest, me.Code, me.Msg
	}
	return http.StatusInternalServerError, "media_internal_error", "could not store the file"
}

// writeMediaError 输出统一的错误信封。
func writeMediaError(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{"error": gin.H{"code": code, "message": message}})
}

// mediaServe 代理读取媒体：带 sha256 ETag 与 immutable 缓存（DESIGN.md §6.3）。
func (s *Server) mediaServe(c *gin.Context) {
	if _, ok := s.requireUser(c); !ok {
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	m, f, err := s.media.Open(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, media.ErrNotFound) {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		s.logger.Error("open media failed", "media_id", id, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	defer f.Close()

	etag := `"` + m.Sha256 + `"`
	c.Header("ETag", etag)
	c.Header("Cache-Control", "private, max-age=31536000, immutable")
	c.Header("Content-Type", m.Mime)
	// 命中 ETag 直接 304，不重读文件体。
	if c.GetHeader("If-None-Match") == etag {
		c.Status(http.StatusNotModified)
		return
	}
	http.ServeContent(c.Writer, c.Request, "", m.CreatedAt, f)
}
