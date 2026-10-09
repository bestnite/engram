package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/media"
	"git.nite07.com/nite/engram/internal/store"
	"git.nite07.com/nite/engram/internal/urlfetch"
)

// 从公开 HTTPS 直链导入卡组包（POST /api/v1/decks/import-url）。
//
// 与文件导入的区别只有「包字节从哪里来」：下载完成后一律交给同一个 ImportDeckPackage，
// 因此包解析、zip 上限、媒体配额、进度归属与审计都只有一份实现。下载本身由叶子包
// internal/urlfetch 完成（地址校验、公网地址钉死、字节上限），本文件只做参数整形与错误映射。

// PackageFetcher 下载一个卡组包直链；实现见 internal/urlfetch，测试注入受控实现。
type PackageFetcher interface {
	Fetch(ctx context.Context, rawURL string, maxBytes int64) (io.ReadCloser, error)
}

const (
	// importURLBodyLimit 是 import-url 请求体的上限：请求体只有一个短 JSON，几 KB 足够。
	// 先限体再解析，避免畸形/超大的 JSON 被整体读进内存。
	importURLBodyLimit = 64 << 10
	// importURLRateLimit 是每个用户在该窗口内允许的直链导入次数。
	// 它比按 key 的写限流更严：本入口会让服务器发起一次出站请求，代价高于普通写操作。
	importURLRateLimit  = 10
	importURLRateWindow = time.Minute
)

// importURLRequest 是 POST /api/v1/decks/import-url 的请求体。
// 字段与文件导入的选项一一对应，只是包字节换成 URL。
type importURLRequest struct {
	URL                 string `json:"url"`
	Target              string `json:"target"`
	DryRun              bool   `json:"dry_run"`
	OnConflict          string `json:"on_conflict"`
	AllowOthersProgress bool   `json:"allow_others_progress"`
	SkipMissingMedia    bool   `json:"skip_missing_media"`
}

// ImportDeckPackageURL 从公开 HTTPS 直链下载并导入一个卡组包。
//
// 顺序很关键：先解析目标卡组并判权，确认请求不会被拒之后才发起网络下载。若先下载再判权，
// 无权用户就能拿本入口把服务器当出站跳板。下载之后复用 ImportDeckPackage，后者会再解析
// 一次目标并再判一次权——重复但幂等，换来的是「权限判定先于取字节」这一不变式。
func (a *API) ImportDeckPackageURL(ctx context.Context, u *store.User, apiKeyID *uint64, rawURL string, opts store.PackageImportOptions) (*store.PackageImportReport, error) {
	// 目标卡组权限：与文件导入同一判定，且在下载之前。
	targetKind, targetDeckID, _, err := a.resolveImportTarget(ctx, opts.Target)
	if err != nil {
		return nil, err
	}
	if targetDeckID != 0 {
		want := store.RoleEditor
		if targetKind == "replace_deck" {
			want = store.RoleOwner
		}
		if _, err := a.RequireDeckRole(ctx, u.ID, targetDeckID, want); err != nil {
			return nil, err
		}
	}
	// 用户级限流：REST 与 MCP 都经本方法，共用同一条按用户计数的池。放在判权之后、
	// 下载之前，使「无权请求不消耗配额」与「配额用尽的请求绝不发起下载」同时成立。
	if !a.importURL.Allow(urlImportLimitKey(u.ID)) {
		return nil, newServiceError(http.StatusTooManyRequests, CodeRateLimited, "")
	}
	// 下载：地址形态、公网地址钉死与字节上限都在 fetcher 内；上限与文件导入同源。
	limit := media.ResolveMaxBytes(ctx, a.db)
	body, err := a.fetcher.Fetch(ctx, rawURL, limit)
	if err != nil {
		return nil, mapFetchError(err)
	}
	defer body.Close()
	return a.ImportDeckPackage(ctx, u, apiKeyID, body, opts)
}

// mapFetchError 把下载错误映射成带稳定 code 的 ServiceError。
//
// 每种失败都产出自己的 code（URL 非法 / 非公网地址 / 下载失败 / 不是包），
// 超限沿用文件导入的 package_too_large（413），保证「体积上限」两处口径一致。
func mapFetchError(err error) error {
	var fe *urlfetch.Error
	if errors.As(err, &fe) {
		switch fe.Kind {
		case urlfetch.KindInvalidURL:
			return newServiceError(http.StatusBadRequest, CodeImportURLInvalid, "")
		case urlfetch.KindBlockedHost:
			return newServiceError(http.StatusBadRequest, CodeImportURLBlocked, "")
		case urlfetch.KindNotPackage:
			return newServiceError(http.StatusBadRequest, CodeImportURLNotPackage, "")
		case urlfetch.KindTooLarge:
			return newServiceError(http.StatusRequestEntityTooLarge, store.CodePackageTooLarge, "")
		case urlfetch.KindFetchFailed:
			return newServiceError(http.StatusBadGateway, CodeImportURLFetchFailed, "")
		}
	}
	return newServiceError(http.StatusBadGateway, CodeImportURLFetchFailed, "")
}

// handleImportPackageURL 是 POST /api/v1/decks/import-url（application/json）。
// scope 与 CSRF 由路由上的 Auth/RequireScope 中间件处理，与文件导入完全一致。
func (a *API) handleImportPackageURL(c *gin.Context) {
	u, _ := CurrentUser(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, importURLBodyLimit)
	var req importURLRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		if isRequestBodyTooLarge(err) {
			abortError(c, http.StatusRequestEntityTooLarge, CodeInvalidRequest, "request body too large")
			return
		}
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "")
		return
	}
	report, err := a.ImportDeckPackageURL(c.Request.Context(), u, CurrentAPIKeyID(c), req.URL, store.PackageImportOptions{
		Target:     req.Target,
		DryRun:     req.DryRun,
		OnConflict: req.OnConflict,
		// 允许导入他人进度是管理员设置项；这里只认管理员显式勾选。
		AllowOthersProgress: req.AllowOthersProgress && u.Role == store.RoleAdmin,
		SkipMissingMedia:    req.SkipMissingMedia,
	})
	if err != nil {
		// 限流在 service 层触发（REST 与 MCP 共用同一计数池）；这里只把 429 的
		// Retry-After 头补回响应，告知客户端按窗口长度后再试。
		if se := asServiceError(err); se.Code == CodeRateLimited {
			c.Header("Retry-After", strconv.Itoa(int(importURLRateWindow.Seconds())))
		}
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, report)
}

// urlImportLimitKey 是直链导入限流的桶键：按用户归一，让 REST 与 MCP 落到同一条键上。
func urlImportLimitKey(userID uint64) string {
	return "user:" + strconv.FormatUint(userID, 10)
}
