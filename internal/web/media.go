package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/media"
	"git.nite07.com/nite/engram/internal/mediatype"
	"git.nite07.com/nite/engram/internal/store"
)

// 媒体相关的系统设置键与对应的环境变量覆盖名。
// 环境变量 > settings 表 > 内置默认，与 internal/config 的优先级一致；这里直接读
// settings 表以便管理员改完立即生效，无需重启。
const (
	settingKeyMediaAllowedMimes = "media_allowed_mimes"
	// settingKeyMediaUserQuotaBytes 是每用户媒体总量配额（字节），0/未配置 = 不限。
	// 与 envMediaUserQuotaBytes 一起指向 internal/media 的同一份解析（上传链与导入链共用）。
	settingKeyMediaUserQuotaBytes = media.SettingKeyMediaUserQuotaBytes
	envMediaAllowedMimes          = "MEDIA_ALLOWED_MIMES"
	envMediaUserQuotaBytes        = media.EnvMediaUserQuotaBytes
)

// registerMediaRoutes 挂载媒体上传与读取。依赖未装配时跳过。
func (s *Server) registerMediaRoutes(router *gin.Engine) {
	if s.sessions == nil || s.media == nil {
		return
	}
	// 上传是写操作，过 CSRF 中间件；读取只要求登录（M5 授权落地前的最小鉴权）。
	router.POST("/media", s.sessions.CSRFMiddleware(), s.mediaUpload)
	router.GET("/media/:sha", s.mediaServe)
	// 编辑器使用的卡组内上传入口。写入要求卡组的 editor 角色，读者无法把媒体
	// 塞进别人的卡组；权限判定与其它写路径共用 auth.DeckAccess（单一实现）。
	if s.access != nil {
		router.POST("/decks/:id/media", s.sessions.CSRFMiddleware(), s.deckMediaUpload)
	}
}

// deckMediaUpload 是编辑器使用的上传入口：先确认当前用户对卡组至少有 editor 角色，
// 再走与 /media 相同的存储与校验逻辑。
func (s *Server) deckMediaUpload(c *gin.Context) {
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	deckID, ok := s.deckIDParam(c)
	if !ok {
		return
	}
	if _, ok := s.loadDeckForRole(c, user, deckID, store.RoleEditor); !ok {
		return
	}
	s.storeUpload(c, user)
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

// userMediaQuota 解析生效的每用户媒体总量配额（字节）：环境变量 > settings 表 > 0。
// 0（含未配置）表示不限——默认关闭是刻意的：不替管理员选一个没人同意过的数字。
// 解析实现与卡组包导入链共用 internal/media.ResolveUserQuotaBytes，避免两处口径漂移。
func (s *Server) userMediaQuota(ctx context.Context) int64 {
	return media.ResolveUserQuotaBytes(ctx, s.db)
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

// mediaUpload 是通用上传入口：登录用户即可上传（授权落地前的行为，保持不变）。
func (s *Server) mediaUpload(c *gin.Context) {
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	s.storeUpload(c, user)
}

// storeUpload 读取 multipart 里的 file 字段、落盘并返回元数据 JSON；错误按稳定 code 映射。
// /media 与 /decks/:id/media 两个入口共用本函数，保证校验逻辑只有一份。
func (s *Server) storeUpload(c *gin.Context, user *store.User) {
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
	fileLimit := media.ResolveMaxBytes(ctx, s.db)
	// 先校验再落盘：把文件读进内存（受单文件上限约束，多读 1 字节以发现超限），
	// 全部拒绝判断都在这之后进行，通过后才交给存储层写临时文件 + rename，避免"写了一半
	// 才发现超限"。内存占用以单文件上限为界，不随上传并发之外的规模增长。
	raw, err := io.ReadAll(io.LimitReader(file, fileLimit+1))
	if err != nil {
		s.logger.Info("media upload read failed", "user_id", user.ID, "error", err)
		writeMediaError(c, http.StatusBadRequest, "media_read_failed", "could not read the uploaded file")
		return
	}
	if int64(len(raw)) > fileLimit {
		status, code, msg := mediaErrorResponse(media.ErrTooLarge)
		s.logger.Info("media upload rejected", "user_id", user.ID, "code", code)
		writeMediaError(c, status, code, msg)
		return
	}
	// 每用户总量配额：只在配置了正数限额时检查；0/未配置 = 不限。
	if quota := s.userMediaQuota(ctx); quota > 0 {
		if !s.checkMediaQuota(c, user, raw, quota) {
			return
		}
	}
	saved, err := s.media.Save(ctx, bytes.NewReader(raw), media.SaveOptions{
		Limit:        fileLimit,
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
		Detail:     map[string]any{"sha256": saved.Sha256, "mime": saved.Mime, "bytes": saved.Bytes},
	})
	// 对外标识就是 sha256：响应不再有自增 id，url 直接由哈希拼成。
	c.JSON(http.StatusCreated, gin.H{
		"sha256": saved.Sha256,
		"mime":   saved.Mime,
		"bytes":  saved.Bytes,
		"url":    "/media/" + saved.Sha256,
	})
}

// checkMediaQuota 在落盘前检查每用户总量配额：已用量 + 本次新增量 > 限额即拒绝，
// 写稳定 code media_quota_exceeded 与点名限额/已用量的本地化文案，返回 false。
//
// 已用量按“该用户 note 引用到的媒体去重求和”计（口径见 internal/store.UserMediaUsage）。
// 若本次文件与该用户已计费的某个 blob 同 sha256，则新增量为 0（去重不重复收费）。
func (s *Server) checkMediaQuota(c *gin.Context, user *store.User, raw []byte, quota int64) bool {
	ctx := c.Request.Context()
	usage, err := store.UserMediaUsage(ctx, s.db, user.ID)
	if err != nil {
		s.logger.Error("media quota usage lookup failed", "user_id", user.ID, "error", err)
		writeMediaError(c, http.StatusInternalServerError, "media_internal_error", "could not store the file")
		return false
	}
	sum := sha256.Sum256(raw)
	sha := hex.EncodeToString(sum[:])
	extra := int64(len(raw))
	if usage.Sha256[sha] {
		extra = 0
	}
	if usage.Bytes+extra <= quota {
		return true
	}
	s.logger.Info("media upload rejected",
		"user_id", user.ID, "code", media.CodeQuotaExceeded, "used", usage.Bytes, "quota", quota)
	writeMediaError(c, http.StatusRequestEntityTooLarge, media.CodeQuotaExceeded,
		s.mediaQuotaMessage(c, usage.Bytes, quota))
	// 配额被触及时通知管理员（D 类）。发信失败绝不影响这次上传的错误响应。
	s.NotifyMediaAlert(ctx, user, usage.Bytes, quota)
	return false
}

// mediaQuotaMessage 组装点名限额与已用量的本地化文案（用户可见文案走语言包）。
// 本地化器缺失时回退英文兜底，绝不把数字藏起来。
func (s *Server) mediaQuotaMessage(c *gin.Context, used, quota int64) string {
	loc, ok := s.localizer(c)
	if !ok || loc == nil {
		return "media quota exceeded"
	}
	return loc.Tf("media.quota.exceeded", map[string]any{
		"used":  humanBytes(used),
		"limit": humanBytes(quota),
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

// mediaShaParamRE 是 GET /media/:sha 的路径参数形状：恰好 64 位小写十六进制。
// 不合形的参数直接 404，且在此之前不查库——避免拿任意字符串去命中 media 表。
var mediaShaParamRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// mediaServe 代理读取媒体：带 sha256 ETag 与 immutable 缓存。
//
// 鉴权：登录之外还要「有卡组访问权」——判定三选一：① media_uploaders 里存在指向我的
// 记录（我提供过这份字节，去重命中也算）；② media_notes 映射里存在指向我可见卡组内、未软删的
// note；③（L3）映射指向我当前会话经分享链接打开过、且授权未过期的卡组内、未软删的 note。
// 谓词与列表/队列/统计同源（store.MediaAccessibleToUser → mediaReadableByUser），
// 无权限与不存在统一 404。
func (s *Server) mediaServe(c *gin.Context) {
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	sha := c.Param("sha")
	// 形状校验先于任何查询：短于 64 位、含非十六进制字符、或旧的数字 id 一律 404。
	if !mediaShaParamRE.MatchString(sha) {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	// 分享授权挂在服务端会话上（L3）：把当前会话 id 显式交给 store 层，
	// 让「本会话通过分享链接打开过的卡组」也计入可读范围。未登录到不了这里（requireUser 已拦截）。
	sessionID := ""
	if sess, ok := auth.CurrentSession(c); ok {
		sessionID = sess.ID
	}
	allowed, err := store.MediaAccessibleToUser(c.Request.Context(), s.db, user.ID, sessionID, sha)
	if err != nil {
		s.logger.Error("media access check failed", "media_sha", sha, "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	if !allowed {
		// 与「媒体不存在」同一个 404：不向无权限者泄露某个 sha 是否存在。
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	m, f, err := s.media.Open(c.Request.Context(), sha)
	if err != nil {
		if errors.Is(err, media.ErrNotFound) {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		s.logger.Error("open media failed", "media_sha", sha, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	defer f.Close()

	etag := `"` + m.Sha256 + `"`
	c.Header("ETag", etag)
	c.Header("Cache-Control", "private, max-age=31536000, immutable")
	// 读侧兼容库里可能存在的历史污染行：只有字节判定白名单内的类型才按声明内联，
	// 其余（如被旧导入链写入的 text/html、image/svg+xml）一律降级为下载，避免同源存储型 XSS。
	contentType := m.Mime
	if !mediatype.Allowed(m.Mime, mediatype.DefaultAllowedMimes()) {
		contentType = "application/octet-stream"
		c.Header("Content-Disposition", "attachment")
	}
	// 纵深防御：显式 Content-Type 不会被 http.ServeContent 覆盖，但 nosniff 仍能挡住
	// 把响应体当脚本执行的旧浏览器/代理——修前这里完全没有该头。
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Type", contentType)
	// 命中 ETag 直接 304，不重读文件体。
	if c.GetHeader("If-None-Match") == etag {
		c.Status(http.StatusNotModified)
		return
	}
	http.ServeContent(c.Writer, c.Request, "", m.CreatedAt, f)
}
