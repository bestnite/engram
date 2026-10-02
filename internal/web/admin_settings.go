package web

import (
	"context"
	"encoding/json"
	"fmt"
	"gorm.io/gorm"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"example.com/engram/internal/auth"
	"example.com/engram/internal/config"
	"example.com/engram/internal/i18n"
	"example.com/engram/internal/media"
	"example.com/engram/internal/store"
	"example.com/engram/internal/web/views"
)

// 系统设置页与全库导出（DESIGN.md §8.4；AGENTS.md §5 M6-5）。
//
// 取值优先级与环境变量语义复用 internal/config：环境变量 > settings 表 > 默认值，
// settings 表按请求现读，因此管理员改完下一次请求即生效，无需重启。每一行都标出来源。

// settingSpec 描述一个可编辑设置：settings 键、可选环境变量覆盖、默认值来源与文案键。
type settingSpec struct {
	key      string
	envVar   string
	labelKey string
	hintKey  string
	// def 在 loc 下算出默认值（站点名回退语言包，语言回退内置默认，等等）。
	def func(loc *i18n.Localizer) string
}

// generalSettingSpecs 是「通用」区块的设置项。
func generalSettingSpecs() []settingSpec {
	return []settingSpec{
		{
			key: settingKeySiteName, labelKey: "admin.setting.site_name", hintKey: "admin.setting.site_name.hint",
			def: func(loc *i18n.Localizer) string { return loc.T("app.name") },
		},
		{
			key: settingKeySiteDefaultLocale, labelKey: "admin.setting.site_default_locale", hintKey: "admin.setting.site_default_locale.hint",
			def: func(*i18n.Localizer) string { return i18n.DefaultLocaleCode },
		},
	}
}

// mediaSettingSpecs 是「媒体」区块的可编辑设置项，与 internal/web/media.go 共用同一批键。
func mediaSettingSpecs() []settingSpec {
	return []settingSpec{
		{
			key: settingKeyMediaMaxBytes, envVar: envMediaMaxBytes,
			labelKey: "admin.setting.media_max_bytes", hintKey: "admin.setting.media_max_bytes.hint",
			def: func(*i18n.Localizer) string { return strconv.FormatInt(media.DefaultMaxBytes(), 10) },
		},
		{
			key: settingKeyMediaAllowedMimes, envVar: envMediaAllowedMimes,
			labelKey: "admin.setting.media_allowed_mimes", hintKey: "admin.setting.media_allowed_mimes.hint",
			def: func(*i18n.Localizer) string { return strings.Join(media.DefaultAllowedMimes(), ", ") },
		},
		{
			// 每用户媒体总量配额（M2-13）：0 = 不限（默认）。0 沿用项目既有约定（每日上限也用 0 表示不限），
			// 不是一个被自拟的具体数字；未配置时显示 0，管理员填入正数即启用。
			key: settingKeyMediaUserQuotaBytes, envVar: envMediaUserQuotaBytes,
			labelKey: "media.quota.setting.label", hintKey: "media.quota.setting.hint",
			def: func(*i18n.Localizer) string { return "0" },
		},
	}
}

// effectiveSetting 解析一个设置的生效值与其来源：环境变量 > settings 表 > 默认值。
// 复用 internal/config 的 Source 常量与优先级语义；settings 表按请求现读，
// 所以管理员改完下一次请求即生效（M6-5 验收）。
func (s *Server) effectiveSetting(ctx context.Context, loc *i18n.Localizer, spec settingSpec) (string, config.Source) {
	if spec.envVar != "" {
		if raw := strings.TrimSpace(os.Getenv(spec.envVar)); raw != "" {
			return raw, config.SourceEnv
		}
	}
	if s.db != nil {
		if settings, err := store.LoadSettings(ctx, s.db); err == nil {
			if raw := strings.TrimSpace(settings[spec.key]); raw != "" {
				return raw, config.SourceDB
			}
		}
	}
	return spec.def(loc), config.SourceDefault
}

// sourceLabel 把来源翻成语言包文案。
func sourceLabel(loc *i18n.Localizer, src config.Source) string {
	return loc.T("admin.source." + string(src))
}

// settingRow 把一个 spec 渲染成表格行（非敏感、可编辑）。
func (s *Server) settingRow(ctx context.Context, loc *i18n.Localizer, spec settingSpec) views.SettingRow {
	value, src := s.effectiveSetting(ctx, loc, spec)
	return views.SettingRow{
		Label:       loc.T(spec.labelKey),
		Key:         spec.key,
		Value:       value,
		SourceLabel: sourceLabel(loc, src),
		Hint:        loc.T(spec.hintKey),
		Editable:    true,
	}
}

// mediaDirSize 递归统计媒体目录占用；目录不存在时按 0 计（尚未上传过任何文件）。
func mediaDirSize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.Type().IsRegular() {
			info, ierr := d.Info()
			if ierr != nil {
				return ierr
			}
			total += info.Size()
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return 0, err
	}
	return total, nil
}

// humanBytes 把字节数换成易读形式；这里是给管理员看的读数，不做本地化单位换算。
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	value := float64(n)
	for i, u := range units {
		value /= unit
		if value < unit || i == len(units)-1 {
			return strconv.FormatFloat(value, 'f', 2, 64) + " " + u
		}
	}
	return strconv.FormatInt(n, 10) + " B"
}

// adminSettingsPage 渲染系统设置页：通用 / 媒体 / 敏感配置三块，
// 每项标出生效值来源；媒体目录占用是现算的只读读数。
func (s *Server) adminSettingsPage(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	general := make([]views.SettingRow, 0, 2)
	for _, spec := range generalSettingSpecs() {
		general = append(general, s.settingRow(ctx, loc, spec))
	}
	mediaRows := make([]views.SettingRow, 0, 4)
	for _, spec := range mediaSettingSpecs() {
		mediaRows = append(mediaRows, s.settingRow(ctx, loc, spec))
	}
	// 媒体目录与占用是只读读数：路径来自 MEDIA_DIR（环境变量或默认），占用现算。
	dir := loc.T("admin.value.unset")
	dirSrc := config.SourceDefault
	if s.media != nil {
		dir = s.media.Root()
	}
	if raw := strings.TrimSpace(os.Getenv("MEDIA_DIR")); raw != "" {
		dir, dirSrc = raw, config.SourceEnv
	}
	mediaRows = append(mediaRows,
		views.SettingRow{
			Label: loc.T("admin.setting.media_dir"), Value: dir,
			SourceLabel: sourceLabel(loc, dirSrc), Hint: loc.T("admin.setting.media_dir.hint"),
		})
	size := int64(0)
	if s.media != nil {
		if n, err := mediaDirSize(s.media.Root()); err == nil {
			size = n
		} else {
			s.logger.Error("admin: compute media directory size failed", "error", err)
		}
	}
	mediaRows = append(mediaRows,
		views.SettingRow{
			Label: loc.T("admin.setting.media_usage"), Value: humanBytes(size),
			SourceLabel: loc.T("admin.source.computed"), Hint: loc.T("admin.setting.media_usage.hint"),
		})

	sections := []views.AdminSection{
		{Heading: loc.T("admin.section.general"), Intro: loc.T("admin.section.general.intro"), Rows: general},
		{Heading: loc.T("admin.section.media"), Intro: loc.T("admin.section.media.intro"), Rows: mediaRows},
	}
	if sensitive := s.sensitiveSection(ctx, loc); len(sensitive.Rows) > 0 {
		sections = append(sections, sensitive)
	}

	csrf := ""
	if sess, ok := auth.CurrentSession(c); ok {
		csrf = sess.CSRFToken
	}
	data := views.AdminPageData{
		Layout:     s.adminLayout(c, loc, "admin.settings.title", "/admin/settings"),
		Heading:    loc.T("admin.settings.heading"),
		Intro:      loc.T("admin.settings.intro"),
		NavHeading: loc.T("admin.nav.heading"),
		Nav:        s.adminNav(loc, "/admin/settings"),
		Columns: views.AdminColumns{
			Setting: loc.T("admin.column.setting"),
			Value:   loc.T("admin.column.value"),
			Source:  loc.T("admin.column.source"),
		},
		Sections:      sections,
		Notice:        s.settingNotice(loc, c.Query("notice")),
		ShowForm:      true,
		FormAction:    "/admin/settings",
		CSRF:          csrf,
		SaveLabel:     loc.T("admin.action.save"),
		ExportHeading: loc.T("admin.export.heading"),
		ExportLabel:   loc.T("admin.export.label"),
		ExportHref:    "/admin/export",
		ExportHint:    loc.T("admin.export.hint"),
	}
	renderHTML(c, views.AdminPage(data))
}

// sensitiveSection 列出 settings 表里所有敏感键，只显示「已配置/未配置」，绝不回显明文（M6-10）。
func (s *Server) sensitiveSection(ctx context.Context, loc *i18n.Localizer) views.AdminSection {
	section := views.AdminSection{
		Heading: loc.T("admin.section.sensitive"),
		Intro:   loc.T("admin.section.sensitive.intro"),
	}
	if s.db == nil {
		return section
	}
	keys, err := store.SensitiveSettingKeys(ctx, s.db)
	if err != nil {
		s.logger.Error("admin: list sensitive setting keys failed", "error", err)
		return section
	}
	for _, key := range keys {
		configured, err := store.SecretConfigured(ctx, s.db, key)
		if err != nil {
			s.logger.Error("admin: read sensitive setting status failed", "key", key, "error", err)
			continue
		}
		statusKey := "admin.sensitive.not_configured"
		src := config.SourceDefault
		if configured {
			statusKey = "admin.sensitive.configured"
			src = config.SourceDB
		}
		section.Rows = append(section.Rows, views.SettingRow{
			Label:       key,
			Key:         key,
			StatusText:  loc.T(statusKey),
			SourceLabel: sourceLabel(loc, src),
			Hint:        loc.T("admin.sensitive.hint"),
			Sensitive:   true,
			Editable:    true,
		})
	}
	return section
}

// settingNotice 把重定向回带的 notice 码翻成文案；未知码不显示。
func (s *Server) settingNotice(loc *i18n.Localizer, code string) string {
	switch code {
	case "saved":
		return loc.T("admin.notice.saved")
	case "invalid_locale":
		return loc.T("admin.notice.invalid_locale")
	case "invalid_number":
		return loc.T("admin.notice.invalid_number")
	case "invalid_mime":
		return loc.T("admin.notice.invalid_mime")
	case "save_failed":
		return loc.T("admin.notice.save_failed")
	default:
		return ""
	}
}

// adminSettingsSave 写入系统设置。校验通过后逐项 upsert，写审计，再重定向回设置页；
// 重定向后的 GET 会现读数据库，因此页面立刻反映新值（M6-5 免重启验收）。
func (s *Server) adminSettingsSave(c *gin.Context) {
	u, ok := auth.CurrentUser(c)
	if !ok {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	ctx := c.Request.Context()
	now := time.Now().UTC()

	// 通用与媒体设置：字段名即 settings 键。
	specs := append(generalSettingSpecs(), mediaSettingSpecs()...)
	changed := make([]string, 0, len(specs)+1)
	for _, spec := range specs {
		if !c.Request.PostForm.Has(spec.key) {
			continue
		}
		raw := strings.TrimSpace(c.PostForm(spec.key))
		if raw == "" {
			// 留空表示不修改；清空回到默认值的语义留待后续任务明确。
			continue
		}
		switch spec.key {
		case settingKeySiteDefaultLocale:
			if !s.isSupportedLocale(raw) {
				c.Redirect(http.StatusSeeOther, "/admin/settings?notice=invalid_locale")
				return
			}
		case settingKeyMediaMaxBytes:
			if n, err := strconv.ParseInt(raw, 10, 64); err != nil || n <= 0 {
				c.Redirect(http.StatusSeeOther, "/admin/settings?notice=invalid_number")
				return
			}
		case settingKeyMediaAllowedMimes:
			if len(splitMimeList(raw)) == 0 {
				c.Redirect(http.StatusSeeOther, "/admin/settings?notice=invalid_mime")
				return
			}
		case settingKeyMediaUserQuotaBytes:
			// 0 = 不限（默认），负数无意义；空值在上面已按“不修改”跳过。
			if n, err := strconv.ParseInt(raw, 10, 64); err != nil || n < 0 {
				c.Redirect(http.StatusSeeOther, "/admin/settings?notice=invalid_number")
				return
			}
		}
		if err := store.PutSetting(ctx, s.db, spec.key, raw, store.Ptr(u.ID), now); err != nil {
			s.logger.Error("admin: save setting failed", "key", spec.key, "error", err)
			c.Redirect(http.StatusSeeOther, "/admin/settings?notice=save_failed")
			return
		}
		changed = append(changed, spec.key)
	}

	// 敏感键：只接受非空的新值，经 AES-GCM 加密后落库，永不回显（M6-10）。
	if s.secrets != nil {
		keys, err := store.SensitiveSettingKeys(ctx, s.db)
		if err != nil {
			s.logger.Error("admin: list sensitive keys failed", "error", err)
			c.Redirect(http.StatusSeeOther, "/admin/settings?notice=save_failed")
			return
		}
		for _, key := range keys {
			raw := c.PostForm(key)
			if strings.TrimSpace(raw) == "" {
				continue
			}
			if err := store.PutSecret(ctx, s.db, s.secrets, key, raw, store.Ptr(u.ID), now); err != nil {
				s.logger.Error("admin: save secret failed", "key", key, "error", err)
				c.Redirect(http.StatusSeeOther, "/admin/settings?notice=save_failed")
				return
			}
			changed = append(changed, key)
		}
	}

	if len(changed) > 0 {
		sort.Strings(changed)
		s.audit(ctx, store.AuditEntry{
			UserID: store.Ptr(u.ID), Action: store.ActionSettingUpdate,
			TargetType: "setting", Detail: map[string]any{"keys": changed},
		})
	}
	c.Redirect(http.StatusSeeOther, "/admin/settings?notice=saved")
}

// isSupportedLocale 报告 code 是否在语言包支持的语言里。
func (s *Server) isSupportedLocale(code string) bool {
	for _, c := range s.i18n.SupportedCodes() {
		if c == code {
			return true
		}
	}
	return false
}

// adminExport 把全库导出成 JSON 下载：遍历 store.AllModels，逐表流式写出，避免整库驻留内存。
func (s *Server) adminExport(c *gin.Context) {
	c.Header("Content-Type", "application/json; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="engram-export.json"`)
	c.Status(http.StatusOK)
	w := c.Writer
	ctx := c.Request.Context()

	// 手工拼 JSON 顶层对象，保证每张表算完即写、不缓冲整库。
	if _, err := io.WriteString(w, `{"exported_at":`); err != nil {
		return
	}
	if b, err := json.Marshal(time.Now().UTC().Format(time.RFC3339)); err == nil {
		_, _ = w.Write(b)
	}
	if _, err := io.WriteString(w, `,"tables":{`); err != nil {
		return
	}
	// 用 Migrator 解析表名，而不是断言 TableName() 接口：后者会让"忘了写 TableName 的模型"
	// 被静默跳过——那和漏登记模型是同一类 bug（备份少一张表却不报错）。
	first := true
	for _, model := range store.AllModels() {
		stmt := &gorm.Statement{DB: s.db}
		if err := stmt.Parse(model); err != nil || stmt.Schema == nil || stmt.Schema.Table == "" {
			s.logger.Error("admin: export cannot resolve table name", "model", fmt.Sprintf("%T", model), "error", err)
			continue
		}
		table := stmt.Schema.Table
		var rows []map[string]any
		if err := s.db.WithContext(ctx).Model(model).Find(&rows).Error; err != nil {
			s.logger.Error("admin: export table failed", "table", table, "error", err)
			continue
		}
		nameJSON, _ := json.Marshal(table)
		rowsJSON, err := json.Marshal(rows)
		if err != nil {
			s.logger.Error("admin: encode export table failed", "table", table, "error", err)
			continue
		}
		if !first {
			_, _ = io.WriteString(w, ",")
		}
		first = false
		_, _ = w.Write(nameJSON)
		_, _ = io.WriteString(w, ":")
		_, _ = w.Write(rowsJSON)
	}
	_, _ = io.WriteString(w, "}}")
}
