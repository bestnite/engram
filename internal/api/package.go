package api

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/store"
)

// 卡组包（.edeck）的 REST 入口（DESIGN.md §7.6、M5-6/M5-7）：
//   GET  /api/v1/decks/:id/package  导出（read）
//   POST /api/v1/decks/import       导入（write）
// 业务逻辑全在 store 层；这里只做参数整形、权限判定与错误映射。

// ExportDeckPackage 导出卡组包并返回内存形态；REST 与 MCP 共用。
func (a *API) ExportDeckPackage(ctx context.Context, userID, deckID uint64, includeProgress, includeMedia, includeReviews bool) (*store.DeckPackage, error) {
	if _, err := a.RequireDeckRole(ctx, userID, deckID, store.RoleReader); err != nil {
		return nil, err
	}
	pkg, err := a.decks.ExportPackage(ctx, userID, deckID, store.PackageOptions{
		IncludeProgress: includeProgress,
		IncludeMedia:    includeMedia,
		IncludeReviews:  includeReviews,
		MediaRoot:       a.mediaRoot,
		Now:             a.now,
	})
	if err != nil {
		return nil, mapPackageError(err)
	}
	return pkg, nil
}

// ImportDeckPackage 解析并导入一个卡组包；target 决定三种目标之一。
// allowOthersProgress 只有在调用者是管理员时才生效（DESIGN.md §7.6 进度导入边界）。
func (a *API) ImportDeckPackage(ctx context.Context, u *store.User, apiKeyID *uint64, r io.Reader, opts store.PackageImportOptions) (*store.PackageImportReport, error) {
	targetKind, targetDeckID, err := store.ParsePackageTarget(opts.Target)
	if err != nil {
		return nil, mapPackageError(err)
	}
	// 目标已有卡组的权限：合并需要 editor，替换是破坏性操作、只允许 owner。
	if targetKind == "into_deck" || targetKind == "replace_deck" {
		want := store.RoleEditor
		if targetKind == "replace_deck" {
			want = store.RoleOwner
		}
		if _, err := a.RequireDeckRole(ctx, u.ID, targetDeckID, want); err != nil {
			return nil, err
		}
	}
	opts.MediaRoot = a.mediaRoot
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
	a.audit(ctx, store.AuditEntry{
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

// mapPackageError 把 store 层的卡组包错误映射成带稳定 code 的 ServiceError。
func mapPackageError(err error) error {
	var pe *store.PackageError
	if asPackageError(err, &pe) {
		status := http.StatusBadRequest
		code := CodeInvalidRequest
		switch pe.Code {
		case store.CodePackageUnsafeEntry, store.CodePackageTooLarge:
			status, code = http.StatusBadRequest, pe.Code
		case store.CodePackageBadFormat:
			status, code = http.StatusBadRequest, CodeInvalidRequest
		case store.CodePackageUnknownKind, store.CodePackageUnsafeMedia:
			status, code = http.StatusBadRequest, pe.Code
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
	deckID, ok := pathID(c, "id")
	if !ok {
		return
	}
	includeProgress := c.Query("include_progress") == "1"
	includeReviews := c.Query("include_reviews") == "1"
	// include_media 默认 on；显式传 0 关闭（DESIGN.md §7.6）。
	includeMedia := c.DefaultQuery("include_media", "1") != "0"

	pkg, err := a.ExportDeckPackage(c.Request.Context(), u.ID, deckID, includeProgress, includeMedia, includeReviews)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	var buf bytes.Buffer
	if err := pkg.WriteZip(&buf); err != nil {
		a.logger.Error("write deck package failed", "deck_id", deckID, "error", err)
		abortError(c, http.StatusInternalServerError, CodeInternal, "")
		return
	}
	name := "deck-" + strconv.FormatUint(deckID, 10) + ".edeck"
	c.Header("Content-Type", "application/vnd.engram.edeck")
	c.Header("Content-Disposition", "attachment; filename=\""+name+"\"")
	c.Data(http.StatusOK, "application/vnd.engram.edeck", buf.Bytes())
}

// handleImportPackage 是 POST /api/v1/decks/import（multipart 上传文件）。
func (a *API) handleImportPackage(c *gin.Context) {
	u, _ := CurrentUser(c)
	fh, err := c.FormFile("file")
	if err != nil {
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
	})
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, report)
}

// formatTargetID 已由 strconv 内联使用，无需单独函数。
