package web

import (
	"bytes"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/store"
)

// 卡组包（.edeck）的浏览器路径（DESIGN.md §7.6；ROADMAP.md M5-9）：
//   GET  /decks/:id/package  导出一个卡组为 .edeck 下载（沿用 store 层导出，不重写）
//   GET  /import             导入页（返回 SPA 应用壳，由客户端路由渲染）
// 导入本身走 REST `POST /api/v1/decks/import`，与 SPA 的内置页共用同一 service 入口。

// registerPackageWebRoutes 挂载卡组包的浏览器入口。与其余卡组路由同一批依赖。
func (s *Server) registerPackageWebRoutes(router *gin.Engine) {
	if s.sessions == nil || s.decks == nil {
		return
	}
	router.GET("/decks/:id/package", s.deckPackageExport)
	// 导入页返回应用壳，由客户端路由渲染；上传走 JSON 端点（spa 侧 client.ts 的 importDeckPackage）。
	router.GET("/import", s.spa.ServeIndex)
}

// packageExportMediaType 是 .edeck 的 MIME（与 REST 导出保持一致）。
const packageExportMediaType = "application/vnd.engram.edeck"

// deckPackageExport 把当前用户有权读取的卡组导出为 .edeck 并下载。
// 权限与 REST 入口同规（reader 即可导出）；导出逻辑复用 store.ExportPackage。
func (s *Server) deckPackageExport(c *gin.Context) {
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	deckID, ok := deckIDParam(c)
	if !ok {
		return
	}
	if _, ok := s.loadDeckForRole(c, user, deckID, store.RoleReader); !ok {
		return
	}
	pkg, err := s.decks.ExportPackage(c.Request.Context(), user.ID, deckID, store.PackageOptions{
		IncludeMedia: true,
		Now:          func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		s.logger.Error("deck package export failed", "deck_id", deckID, "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := pkg.WriteZip(&buf); err != nil {
		s.logger.Error("write deck package failed", "deck_id", deckID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	name := fmt.Sprintf("deck-%d.edeck", deckID)
	c.Header("Content-Type", packageExportMediaType)
	c.Header("Content-Disposition", `attachment; filename="`+name+`"`)
	c.Data(http.StatusOK, packageExportMediaType, buf.Bytes())
}
