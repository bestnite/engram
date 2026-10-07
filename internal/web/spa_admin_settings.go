package web

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/config"
	"git.nite07.com/nite/engram/internal/media"
	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是管理面板「系统设置」的 SPA JSON 端点（ROADMAP.md M6-5）。
//
// 取值优先级与校验与 SSR 页（admin_settings.go）完全相同：环境变量 > settings 表 > 默认值，
// 复用 effectiveSetting 与同一个 settingSpec 列表，因此不会出现两套解析。响应里每行都带
// source（env/db/default/computed）与原始值，本地化文案由前端语言包按 key 映射。

// spaAdminSettingRow 是设置表格里的一行。readonly 为 true 时前端只展示不提交。
type spaAdminSettingRow struct {
	Key        string `json:"key"`
	Value      string `json:"value"`
	Source     string `json:"source"`
	Editable   bool   `json:"editable"`
	Sensitive  bool   `json:"sensitive"`
	Configured bool   `json:"configured"`
	// Unit 非空时值需要按单位解释（当前只有媒体占用用 "bytes"）。
	Unit string `json:"unit,omitempty"`
}

// spaAdminSettingsSection 是一个设置分区（general / media / optimize / sensitive）。
type spaAdminSettingsSection struct {
	Name string               `json:"name"`
	Rows []spaAdminSettingRow `json:"rows"`
}

// spaAdminSettingsResponse 是系统设置页的读取结果。
type spaAdminSettingsResponse struct {
	Sections []spaAdminSettingsSection `json:"sections"`
}

// spaAdminSettingsRequest 是保存请求：键即 settings 键，值为待写文本；缺失或空串表示不修改。
type spaAdminSettingsRequest struct {
	Values map[string]string `json:"values"`
}

// spaAdminSettings 返回系统设置的分区与行，口径与 adminSettingsPage 一致。
func (s *Server) spaAdminSettings(c *gin.Context) {
	ctx := c.Request.Context()
	loc, ok := s.localizer(c)
	if !ok {
		return
	}

	rows := func(specs []settingSpec) []spaAdminSettingRow {
		out := make([]spaAdminSettingRow, 0, len(specs))
		for _, spec := range specs {
			value, src := s.effectiveSetting(ctx, loc, spec)
			out = append(out, spaAdminSettingRow{
				Key: spec.key, Value: value, Source: string(src), Editable: true,
			})
		}
		return out
	}

	mediaRows := rows(mediaSettingSpecs())
	// 媒体目录与占用是只读读数：路径来自 MEDIA_DIR（环境变量或默认），占用现算。
	dir := ""
	dirSrc := config.SourceDefault
	if s.media != nil {
		dir = s.media.Root()
	}
	if raw := strings.TrimSpace(os.Getenv("MEDIA_DIR")); raw != "" {
		dir, dirSrc = raw, config.SourceEnv
	}
	mediaRows = append(mediaRows, spaAdminSettingRow{
		Key: "media_dir", Value: dir, Source: string(dirSrc), Editable: false,
	})
	size := int64(0)
	if s.media != nil {
		if n, err := mediaDirSize(s.media.Root()); err == nil {
			size = n
		} else {
			s.logger.Error("spa admin: compute media directory size failed", "error", err)
		}
	}
	mediaRows = append(mediaRows, spaAdminSettingRow{
		Key: "media_usage", Value: strconv.FormatInt(size, 10), Source: "computed", Editable: false, Unit: "bytes",
	})

	sections := []spaAdminSettingsSection{
		{Name: "general", Rows: rows(generalSettingSpecs())},
		{Name: "media", Rows: mediaRows},
		{Name: "optimize", Rows: rows(optimizeSettingSpecs())},
	}
	if sensitive := s.spaAdminSensitiveRows(ctx); len(sensitive) > 0 {
		sections = append(sections, spaAdminSettingsSection{Name: "sensitive", Rows: sensitive})
	}
	c.JSON(http.StatusOK, spaAdminSettingsResponse{Sections: sections})
}

// spaAdminSensitiveRows 列出 settings 表里所有敏感键，只显示「已配置/未配置」，绝不含明文。
func (s *Server) spaAdminSensitiveRows(ctx context.Context) []spaAdminSettingRow {
	if s.db == nil {
		return nil
	}
	keys, err := store.SensitiveSettingKeys(ctx, s.db)
	if err != nil {
		s.logger.Error("spa admin: list sensitive setting keys failed", "error", err)
		return nil
	}
	rows := make([]spaAdminSettingRow, 0, len(keys))
	for _, key := range keys {
		configured, err := store.SecretConfigured(ctx, s.db, key)
		if err != nil {
			s.logger.Error("spa admin: read sensitive setting status failed", "key", key, "error", err)
			continue
		}
		src := config.SourceDefault
		if configured {
			src = config.SourceDB
		}
		rows = append(rows, spaAdminSettingRow{
			Key: key, Source: string(src), Editable: true, Sensitive: true, Configured: configured,
		})
	}
	return rows
}

// spaAdminSettingsSave 写入系统设置；校验与 adminSettingsSave 逐条相同（同一 switch），
// 非法值 400 且不写库。敏感键经 AES-GCM 加密落库，永不回显。
func (s *Server) spaAdminSettingsSave(c *gin.Context) {
	u, ok := auth.CurrentUser(c)
	if !ok {
		spaAdminError(c, http.StatusForbidden, "forbidden")
		return
	}
	var req spaAdminSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		spaAdminError(c, http.StatusBadRequest, "invalid_request")
		return
	}
	ctx := c.Request.Context()
	now := time.Now().UTC()

	specs := append(append(generalSettingSpecs(), mediaSettingSpecs()...), optimizeSettingSpecs()...)
	changed := make([]string, 0, len(specs)+1)
	for _, spec := range specs {
		raw, present := req.Values[spec.key]
		if !present {
			continue
		}
		raw = strings.TrimSpace(raw)
		if raw == "" {
			// 留空表示不修改；清空回到默认值的语义留待后续任务明确。
			continue
		}
		switch spec.key {
		case settingKeySiteDefaultLocale:
			if !s.supportedLocale(raw) {
				spaAdminError(c, http.StatusBadRequest, "invalid_locale")
				return
			}
		case media.SettingKeyMediaMaxBytes:
			if n, err := strconv.ParseInt(raw, 10, 64); err != nil || n <= 0 {
				spaAdminError(c, http.StatusBadRequest, "invalid_number")
				return
			}
		case settingKeyMediaAllowedMimes:
			if len(splitMimeList(raw)) == 0 {
				spaAdminError(c, http.StatusBadRequest, "invalid_mime")
				return
			}
		case settingKeyMediaUserQuotaBytes:
			if n, err := strconv.ParseInt(raw, 10, 64); err != nil || n < 0 {
				spaAdminError(c, http.StatusBadRequest, "invalid_number")
				return
			}
		case store.SettingKeyOptimizeMinReviews:
			if n, err := strconv.Atoi(raw); err != nil || n < store.MinOptimizeMinReviews {
				spaAdminError(c, http.StatusBadRequest, "optimize_min_reviews_too_low")
				return
			}
		}
		if err := store.PutSetting(ctx, s.db, spec.key, raw, store.Ptr(u.ID), now); err != nil {
			s.logger.Error("spa admin: save setting failed", "key", spec.key, "error", err)
			spaAdminError(c, http.StatusInternalServerError, "save_failed")
			return
		}
		changed = append(changed, spec.key)
	}

	// 敏感键：只接受非空的新值，经 AES-GCM 加密后落库，永不回显（M6-10）。
	if s.secrets != nil {
		keys, err := store.SensitiveSettingKeys(ctx, s.db)
		if err != nil {
			s.logger.Error("spa admin: list sensitive keys failed", "error", err)
			spaAdminError(c, http.StatusInternalServerError, "save_failed")
			return
		}
		for _, key := range keys {
			raw, present := req.Values[key]
			if !present || strings.TrimSpace(raw) == "" {
				continue
			}
			if err := store.PutSecret(ctx, s.db, s.secrets, key, raw, store.Ptr(u.ID), now); err != nil {
				s.logger.Error("spa admin: save secret failed", "key", key, "error", err)
				spaAdminError(c, http.StatusInternalServerError, "save_failed")
				return
			}
			changed = append(changed, key)
		}
	}

	if len(changed) > 0 {
		s.audit(ctx, store.AuditEntry{
			UserID: store.Ptr(u.ID), Action: store.ActionSettingUpdate,
			TargetType: "setting", Detail: map[string]any{"keys": changed, "via": "spa"},
		})
	}
	c.Status(http.StatusNoContent)
}
