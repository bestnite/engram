package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/media"
	"git.nite07.com/nite/engram/internal/store"
)

// 卡组包（.edeck）的 REST 入口：
//   GET  /api/v1/decks/:id/package  导出（read）
//   POST /api/v1/decks/import       导入（write）
// 业务逻辑全在 store 层；这里只做参数整形、权限判定与错误映射。

// ExportDeckPackage 导出卡组包并返回内存形态；REST、MCP 与 CLI 共用。
// 调用方只填 Include* 开关；MediaRoot 与时钟由服务端决定，传入的值会被覆盖。
func (a *API) ExportDeckPackage(ctx context.Context, userID, deckID uint64, opts store.PackageOptions) (*store.DeckPackage, error) {
	if _, err := a.RequireDeckRole(ctx, userID, deckID, store.RoleReader); err != nil {
		return nil, err
	}
	opts.MediaRoot = a.mediaRoot
	opts.Now = a.now
	pkg, err := a.decks.ExportPackage(ctx, userID, deckID, opts)
	if err != nil {
		return nil, mapPackageError(err)
	}
	return pkg, nil
}

// ImportDeckPackage 解析并导入一个卡组包；target 决定三种目标之一。
// allowOthersProgress 只有在调用者是管理员时才生效（进度导入边界）。
func (a *API) ImportDeckPackage(ctx context.Context, u *store.User, apiKeyID *uint64, r io.Reader, opts store.PackageImportOptions) (*store.PackageImportReport, error) {
	// 客户端给的卡组标识是对外 id；store 只认数字主键，故先在这里解析。
	targetKind, targetDeckID, target, err := a.resolveImportTarget(ctx, opts.Target)
	if err != nil {
		return nil, err
	}
	opts.Target = target
	// 目标已有卡组的权限：合并需要 editor，替换是破坏性操作、只允许 owner。
	if targetDeckID != 0 {
		want := store.RoleEditor
		if targetKind == "replace_deck" {
			want = store.RoleOwner
		}
		if _, err := a.RequireDeckRole(ctx, u.ID, targetDeckID, want); err != nil {
			return nil, err
		}
	}
	opts.MediaRoot = a.mediaRoot
	// 把导入者当前生效的媒体配额交给 store 层；四条入口（REST/MCP/CLI/Web）都走本方法，
	// 因此共用一处解析，不会有人绕过配额检查。
	opts.MediaQuotaBytes = media.ResolveUserQuotaBytes(ctx, a.db)
	if opts.Now == nil {
		opts.Now = a.now
	}
	if u.Role != store.RoleAdmin {
		// 非管理员无论如何都不能导入他人进度（管理员设置的另一半在调用方读设置后置位）。
		opts.AllowOthersProgress = false
	}
	report, err := a.decks.ImportPackage(ctx, u.ID, r, opts)
	if err != nil {
		return nil, mapPackageError(err)
	}
	recordAudit(ctx, a.auditor, a.logger, store.AuditEntry{
		UserID:     store.Ptr(u.ID),
		APIKeyID:   apiKeyID,
		Action:     "deck.package_import",
		TargetType: "deck",
		TargetID:   store.Ptr(report.DeckID),
		Detail: map[string]any{
			"target": report.Target, "dry_run": report.DryRun,
			"notes_created": report.NotesCreated, "notes_updated": report.NotesUpdated,
			"progress_applied": report.ProgressApplied, "progress_discarded": report.ProgressDiscarded,
		},
	})
	return report, nil
}

// resolveImportTarget 把客户端传来的目标串翻成 store 的形态。
// 客户端给的卡组标识是对外 id，store 只认数字主键；返回种类、目标卡组数字主键
// （new_deck 为 0）与 store 形态的目标串。
func (a *API) resolveImportTarget(ctx context.Context, raw string) (kind string, deckID uint64, target string, err error) {
	kind, ref, err := store.ParsePackageTargetRef(raw)
	if err != nil {
		return "", 0, "", mapPackageError(err)
	}
	if kind == store.PackageTargetNewDeck {
		return kind, 0, store.FormatPackageTarget(kind, 0), nil
	}
	d, derr := a.decks.ByPublicID(ctx, ref)
	if derr != nil {
		return "", 0, "", newServiceError(http.StatusNotFound, CodeNotFound, "deck not found")
	}
	return kind, d.ID, store.FormatPackageTarget(kind, d.ID), nil
}

// mapPackageError 把 store 层的卡组包错误映射成带稳定 code 的 ServiceError。
//
// 每个包错误都产出它自己的稳定 code（原样透出 store.PackageError.Code），不再把
// package_bad_format 折叠成笼统的 invalid_request——否则 error.package_* 这批语言包键
// 永无产出，用户也分不清“格式无效”/“题型不支持”/“媒体类型不允许”。
// HTTP 状态保持既有语义不变：配额不足 413、引用不可读媒体 403，其余包错误 400。
func mapPackageError(err error) error {
	var pe *store.PackageError
	if asPackageError(err, &pe) {
		status := http.StatusBadRequest
		switch pe.Code {
		case store.CodePackageQuotaExceeded:
			// 与上传链的 media_quota_exceeded 同一 HTTP 语义：配额不足按 413 返回。
			status = http.StatusRequestEntityTooLarge
		case store.CodePackageMediaForbidden:
			// 与单卡写入的 media_not_readable 同一语义：引用了自己读不到的媒体，按 403 拒绝。
			status = http.StatusForbidden
		}
		code := pe.Code
		if code == "" {
			// 兜底：未带 code 的包错误保持既有 invalid_request 口径。
			code = CodeInvalidRequest
		}
		msg := pe.Message
		if len(pe.Entries) > 0 {
			msg = msg + ": " + strings.Join(pe.Entries, ", ")
		}
		return newServiceError(status, code, msg)
	}
	if err == nil {
		return nil
	}
	return newServiceError(http.StatusInternalServerError, CodeInternal, err.Error())
}

// asPackageError 是 errors.As 的小包装，避免在 api 包各处重复 import errors。
func asPackageError(err error, target **store.PackageError) bool {
	for err != nil {
		if pe, ok := err.(*store.PackageError); ok {
			*target = pe
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// handleExportPackage 是 GET /api/v1/decks/:id/package。
func (a *API) handleExportPackage(c *gin.Context) {
	u, _ := CurrentUser(c)
	ctx := c.Request.Context()
	publicID, ok := pathPublicID(c, "id")
	if !ok {
		return
	}
	d, err := a.decks.ByPublicID(ctx, publicID)
	if err != nil {
		abortNotFound(c)
		return
	}
	pkg, err := a.ExportDeckPackage(ctx, u.ID, d.ID, store.PackageOptions{
		IncludeProgress: c.Query("include_progress") == "1",
		IncludeReviews:  c.Query("include_reviews") == "1",
		IncludeWeights:  c.Query("include_weights") == "1",
		// include_media 默认 on；显式传 0 关闭。
		IncludeMedia: c.DefaultQuery("include_media", "1") != "0",
	})
	if err != nil {
		writeServiceError(c, err)
		return
	}
	var buf bytes.Buffer
	if err := pkg.WriteZip(&buf); err != nil {
		a.logger.Error("write deck package failed", "deck_id", d.ID, "error", err)
		abortError(c, http.StatusInternalServerError, CodeInternal, "")
		return
	}
	// 下载名用卡组标题，用户在下载目录里能认出是哪个卡组；标题清洗不出可用名字时退回对外 id。
	c.Header("Content-Type", "application/vnd.engram.edeck")
	c.Header("Content-Disposition", AttachmentDisposition(DeckPackageFilename(d.Name, publicID)))
	c.Data(http.StatusOK, "application/vnd.engram.edeck", buf.Bytes())
}

// isRequestBodyTooLarge 判断 multipart 解析失败是否由 http.MaxBytesReader 触发。
// gin 会把 MaxBytesError 包在解析错误里，errors.As 能透过包装找到它。
func isRequestBodyTooLarge(err error) bool {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		return true
	}
	return strings.Contains(err.Error(), "http: request body too large")
}

// handleImportPackage 是 POST /api/v1/decks/import（multipart 上传文件）。
func (a *API) handleImportPackage(c *gin.Context) {
	u, _ := CurrentUser(c)
	// 先按管理员配置的上传上限限制请求体，再解析 multipart：gin 会按 MaxMultipartMemory 把
	// 超出内存的 part 落到临时盘，不先设上限就给了内存/磁盘放大。
	// 超限稳定 413，code 沿用卡组包既有的 package_too_large。
	limit := media.ResolveMaxBytes(c.Request.Context(), a.db)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
	fh, err := c.FormFile("file")
	if err != nil {
		if isRequestBodyTooLarge(err) {
			abortError(c, http.StatusRequestEntityTooLarge, store.CodePackageTooLarge, "")
			return
		}
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "")
		return
	}
	f, err := fh.Open()
	if err != nil {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "")
		return
	}
	defer f.Close()

	dryRun := c.PostForm("dry_run") == "1" || c.PostForm("dry_run") == "true"
	report, err := a.ImportDeckPackage(c.Request.Context(), u, CurrentAPIKeyID(c), f, store.PackageImportOptions{
		Target:     c.PostForm("target"),
		DryRun:     dryRun,
		OnConflict: c.PostForm("on_conflict"),
		// 允许导入他人进度是管理员设置项；这里只认管理员显式勾选。
		AllowOthersProgress: c.PostForm("allow_others_progress") == "1" && u.Role == store.RoleAdmin,
		SkipMissingMedia:    c.PostForm("skip_missing_media") == "1",
		ApplyWeights:        c.PostForm("apply_weights") == "1",
	})
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, report)
}

// formatTargetID 已由 strconv 内联使用，无需单独函数。
