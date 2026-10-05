package web

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/store"
	"git.nite07.com/nite/engram/internal/web/views"
)

// 卡组包（.edeck）的浏览器路径（DESIGN.md §7.6；ROADMAP.md M5-9）：
//   GET  /decks/:id/package  导出一个卡组为 .edeck 下载（沿用 store 层导出，不重写）
//   GET  /import             上传页
//   POST /import             导入并展示与 REST 同一份摘要字段
// 业务逻辑全在 store 层；这里只做会话鉴权、体积限制与错误的可读化。

// registerPackageWebRoutes 挂载卡组包的浏览器入口。与其余卡组路由同一批依赖。
func (s *Server) registerPackageWebRoutes(router *gin.Engine) {
	if s.sessions == nil || s.decks == nil {
		return
	}
	router.GET("/decks/:id/package", s.deckPackageExport)
	router.GET("/import", s.importPage)
	// 上传是写操作，过 CSRF 中间件。
	router.POST("/import", s.sessions.CSRFMiddleware(), s.importSubmit)
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

// importOption 描述导入目标下拉的一项。
type importOption struct {
	Value string
	Label string
	// Selected 为 true 时预选中。
	Selected bool
}

// importTargetOptions 返回导入目标选项：新建卡组 / 合并进指定卡组。
func importTargetOptions(loc *i18n.Localizer) []views.ImportOption {
	return []views.ImportOption{
		{Value: store.PackageTargetNewDeck, Label: loc.T("import.target.new_deck"), Selected: true},
		{Value: "into_deck", Label: loc.T("import.target.into_deck")},
	}
}

// importConflictOptions 返回冲突处理选项（update 默认）。
func importConflictOptions(loc *i18n.Localizer) []views.ImportOption {
	return []views.ImportOption{
		{Value: "update", Label: loc.T("import.on_conflict.update"), Selected: true},
		{Value: "skip", Label: loc.T("import.on_conflict.skip")},
		{Value: "fail", Label: loc.T("import.on_conflict.fail")},
	}
}

// importPage 渲染上传页；成功导入后由 importSubmit 带结果再渲染同一模板。
func (s *Server) importPage(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	_, ok = s.requireUser(c)
	if !ok {
		return
	}
	s.renderImportPage(c, loc, http.StatusOK, nil, "")
}

// renderImportPage 渲染上传页与（可选）导入摘要；errMsg 为已本地化的可读错误。
func (s *Server) renderImportPage(c *gin.Context, loc *i18n.Localizer, status int, report *store.PackageImportReport, errMsg string) {
	data := views.ImportPageData{
		Layout:           s.pageLayout(c, loc, "import.title"),
		Heading:          loc.T("import.heading"),
		Intro:            loc.T("import.intro"),
		FormAction:       "/import",
		FileLabel:        loc.T("import.file_label"),
		TargetLabel:      loc.T("import.target_label"),
		DeckIDLabel:      loc.T("import.deck_id_label"),
		DryRunLabel:      loc.T("import.dry_run_label"),
		AllowOthersLabel: loc.T("import.allow_others_label"),
		OnConflictLabel:  loc.T("import.on_conflict_label"),
		SubmitLabel:      loc.T("import.submit"),
		Targets:          importTargetOptions(loc),
		OnConflicts:      importConflictOptions(loc),
		ErrorMessage:     errMsg,
	}
	if u, ok := auth.CurrentUser(c); ok && u.Role == store.RoleAdmin {
		data.AllowOthersShow = true
	}
	if sess, ok := auth.CurrentSession(c); ok {
		data.CSRF = sess.CSRFToken
	}
	if report != nil {
		data.Result = s.importResult(loc, report)
	}
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Status(status)
	if err := views.ImportPage(data).Render(c.Request.Context(), c.Writer); err != nil {
		s.logger.Error("render template failed", "error", err, "path", c.Request.URL.Path)
	}
}

// importResult 把 store 的导入摘要翻成展示行；字段与 REST 响应一一对应，两处同源。
func (s *Server) importResult(loc *i18n.Localizer, report *store.PackageImportReport) *views.ImportResult {
	rows := []views.ImportRow{
		{Label: loc.T("import.field.target"), Value: report.Target},
		{Label: loc.T("import.field.dry_run"), Value: yesNo(loc, report.DryRun)},
		{Label: loc.T("import.field.deck_id"), Value: uintString(report.DeckID)},
		{Label: loc.T("import.field.notes_created"), Value: strconv.Itoa(report.NotesCreated)},
		{Label: loc.T("import.field.notes_updated"), Value: strconv.Itoa(report.NotesUpdated)},
		{Label: loc.T("import.field.notes_skipped"), Value: strconv.Itoa(report.NotesSkipped)},
		{Label: loc.T("import.field.cards_created"), Value: strconv.Itoa(report.CardsCreated)},
		{Label: loc.T("import.field.media_new"), Value: strconv.Itoa(report.MediaNew)},
		{Label: loc.T("import.field.media_missing"), Value: strconv.Itoa(report.MediaMissing)},
		{Label: loc.T("import.field.progress_applied"), Value: strconv.Itoa(report.ProgressApplied)},
		{Label: loc.T("import.field.progress_skipped"), Value: strconv.Itoa(report.ProgressSkipped)},
		{Label: loc.T("import.field.progress_discarded"), Value: yesNo(loc, report.ProgressDiscarded)},
	}
	errs := make([]views.ImportErrorRow, 0, len(report.Errors))
	for _, e := range report.Errors {
		errs = append(errs, views.ImportErrorRow{Entry: e.Entry, Reason: e.Reason})
	}
	return &views.ImportResult{
		Heading:    loc.T("import.result_heading"),
		Rows:       rows,
		Errors:     errs,
		ErrorsHead: loc.T("import.errors_heading"),
		NoErrors:   loc.T("import.no_errors"),
		ColEntry:   loc.T("import.col.entry"),
		ColReason:  loc.T("import.col.reason"),
	}
}

// yesNo 把布尔翻成本地化的「是/否」。
func yesNo(loc *i18n.Localizer, v bool) string {
	if v {
		return loc.T("import.value.yes")
	}
	return loc.T("import.value.no")
}

// uintString 把 0 显示为空串（报告里 deck_id 为 omitempty）。
func uintString(v uint64) string {
	if v == 0 {
		return ""
	}
	return strconv.FormatUint(v, 10)
}

// importSubmit 接收上传的 .edeck：限制体积（与管理员设置的上传上限一致）、
// 复用 ReadPackageArchive 的归档安全防护（在 store.ImportPackage 内部），
// 然后把与 REST 相同的摘要渲染到页面上；坏包给可读错误而不是 500。
func (s *Server) importSubmit(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	limit := s.uploadLimit(ctx)
	// 先限制请求体，再解析 multipart：避免超限文件被读进内存/磁盘。
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)

	fh, err := c.FormFile("file")
	if err != nil {
		s.logger.Info("import upload rejected", "user_id", user.ID, "error", err)
		s.renderImportPage(c, loc, http.StatusBadRequest, nil, loc.T("import.error.no_file"))
		return
	}
	if fh.Size > limit {
		s.renderImportPage(c, loc, http.StatusRequestEntityTooLarge, nil, loc.T("import.error.too_large"))
		return
	}
	f, err := fh.Open()
	if err != nil {
		s.logger.Info("import open upload failed", "user_id", user.ID, "error", err)
		s.renderImportPage(c, loc, http.StatusBadRequest, nil, loc.T("import.error.no_file"))
		return
	}
	defer f.Close()

	target := strings.TrimSpace(c.PostForm("target"))
	if target == "" {
		target = store.PackageTargetNewDeck
	}
	if target == "into_deck" {
		id := strings.TrimSpace(c.PostForm("deck_id"))
		target = "into_deck:" + id
	}
	// 目标卡组判权（DESIGN.md §7.6）：合并进已有卡组需要 editor，替换是破坏性操作
	// （ImportPackage 会先软删目标卡组全部 note）只允许 owner。判权与 REST 入口同规
	// （internal/api/package.go），且必须在 ImportPackage 之前完成 —— store 层不做判权，
	// 少了这一步，任何登录用户都能向他人私有卡组写入或清空（M5-9）。
	// 失败由 loadDeckForRole 写出 403/404 并记 permission.denied 审计，与其它 handler 一致。
	if kind, deckID, err := store.ParsePackageTarget(target); err == nil && kind != store.PackageTargetNewDeck {
		want := store.RoleEditor
		if kind == "replace_deck" {
			want = store.RoleOwner
		}
		if _, ok := s.loadDeckForRole(c, user, deckID, want); !ok {
			return
		}
	}
	opts := store.PackageImportOptions{
		Target:     target,
		DryRun:     c.PostForm("dry_run") == "1",
		OnConflict: strings.TrimSpace(c.PostForm("on_conflict")),
		// 允许导入他人进度仅管理员可勾选（与 REST 同规）。
		AllowOthersProgress: c.PostForm("allow_others_progress") == "1" && user.Role == store.RoleAdmin,
		// F15：导入者当前生效的媒体配额，交给 store 层统一计入并整包拒绝超限（与 REST/MCP/CLI 同规）。
		MediaQuotaBytes: s.userMediaQuota(ctx),
		Now:             func() time.Time { return time.Now().UTC() },
	}
	// LimitReader 兜底：即便 multipart 边界处理有出入，也不会无限解压。
	report, err := s.decks.ImportPackage(ctx, user.ID, io.LimitReader(f, limit), opts)
	if err != nil {
		status, msg := importErrorMessage(loc, err)
		s.logger.Info("import package rejected", "user_id", user.ID, "error", err)
		s.renderImportPage(c, loc, status, nil, msg)
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID: store.Ptr(user.ID), Action: "deck.package_import",
		TargetType: "deck", TargetID: store.Ptr(report.DeckID),
		Detail: map[string]any{
			"target": report.Target, "dry_run": report.DryRun,
			"notes_created": report.NotesCreated, "notes_updated": report.NotesUpdated,
		},
	})
	s.renderImportPage(c, loc, http.StatusOK, report, "")
}

// importErrorMessage 把卡组包错误翻成可读文案与状态码（坏包 → 4xx，绝不 500）。
func importErrorMessage(loc *i18n.Localizer, err error) (int, string) {
	var pe *store.PackageError
	if errors.As(err, &pe) {
		status := http.StatusBadRequest
		if pe.Code == store.CodePackageTooLarge || pe.Code == store.CodePackageQuotaExceeded {
			status = http.StatusRequestEntityTooLarge
		}
		msg := loc.T("import.error." + pe.Code)
		// 逐条列出出错条目，便于用户定位（未知题型会列出 note 下标）。
		if len(pe.Entries) > 0 {
			msg = msg + " (" + strings.Join(pe.Entries, ", ") + ")"
		}
		return status, msg
	}
	return http.StatusBadRequest, loc.T("import.error.generic")
}
