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

// This file is the SPA JSON endpoint for the admin panel's "system settings".
//
// Value precedence and validation are exactly the same as the SSR page (admin_settings.go): env vars > settings table > defaults,
// reusing effectiveSetting and the same settingSpec list, so there are never two sets of parsing. Every row in the response carries
// source (env/db/default/computed) and the raw value; localized copy is mapped by the frontend language packs by key.

// adminSettingRow is one row in the settings table. When readonly is true the frontend only displays it and does not submit it.
type adminSettingRow struct {
	Key        string `json:"key"`
	Value      string `json:"value"`
	Source     string `json:"source"`
	Editable   bool   `json:"editable"`
	Sensitive  bool   `json:"sensitive"`
	Configured bool   `json:"configured"`
	// When Unit is non-empty the value must be interpreted per that unit (currently only media usage uses "bytes").
	Unit string `json:"unit,omitempty"`
}

// adminSettingsSection is one settings section (general / media / optimize / sensitive).
type adminSettingsSection struct {
	Name string            `json:"name"`
	Rows []adminSettingRow `json:"rows"`
}

// adminSettingsResponse is the read result for the system settings page.
type adminSettingsResponse struct {
	Sections []adminSettingsSection `json:"sections"`
}

// adminSettingsRequest is the save request: the key is the settings key, the value is the text to write; missing or empty string means no change.
type adminSettingsRequest struct {
	Values map[string]string `json:"values"`
}

// adminSettings returns the system settings sections and rows, each row carrying its value, its source (env or DB) and whether it is editable.
func (s *Server) adminSettings(c *gin.Context) {
	ctx := c.Request.Context()
	loc, ok := s.localizer(c)
	if !ok {
		return
	}

	rows := func(specs []settingSpec) []adminSettingRow {
		out := make([]adminSettingRow, 0, len(specs))
		for _, spec := range specs {
			value, src := s.effectiveSetting(ctx, loc, spec)
			out = append(out, adminSettingRow{
				Key: spec.key, Value: value, Source: string(src), Editable: true,
			})
		}
		return out
	}

	mediaRows := rows(mediaSettingSpecs())
	// The media directory and usage are read-only readings: the path comes from MEDIA_DIR (env var or default) and the usage is computed on the fly.
	dir := ""
	dirSrc := config.SourceDefault
	if s.media != nil {
		dir = s.media.Root()
	}
	if raw := strings.TrimSpace(os.Getenv("MEDIA_DIR")); raw != "" {
		dir, dirSrc = raw, config.SourceEnv
	}
	mediaRows = append(mediaRows, adminSettingRow{
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
	mediaRows = append(mediaRows, adminSettingRow{
		Key: "media_usage", Value: strconv.FormatInt(size, 10), Source: "computed", Editable: false, Unit: "bytes",
	})

	sections := []adminSettingsSection{
		{Name: "general", Rows: rows(generalSettingSpecs())},
		{Name: "media", Rows: mediaRows},
		{Name: "optimize", Rows: rows(optimizeSettingSpecs())},
	}
	if sensitive := s.adminSensitiveRows(ctx); len(sensitive) > 0 {
		sections = append(sections, adminSettingsSection{Name: "sensitive", Rows: sensitive})
	}
	c.JSON(http.StatusOK, adminSettingsResponse{Sections: sections})
}

// adminSensitiveRows lists all sensitive keys in the settings table, showing only "configured/not configured" and never plaintext.
func (s *Server) adminSensitiveRows(ctx context.Context) []adminSettingRow {
	if s.db == nil {
		return nil
	}
	keys, err := store.SensitiveSettingKeys(ctx, s.db)
	if err != nil {
		s.logger.Error("spa admin: list sensitive setting keys failed", "error", err)
		return nil
	}
	rows := make([]adminSettingRow, 0, len(keys))
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
		rows = append(rows, adminSettingRow{
			Key: key, Source: string(src), Editable: true, Sensitive: true, Configured: configured,
		})
	}
	return rows
}

// adminSettingsSave writes the system settings; every writable key is validated by its own branch of the switch below, and an invalid
// value is 400 with nothing written. Sensitive keys are stored AES-GCM encrypted and never echoed back.
func (s *Server) adminSettingsSave(c *gin.Context) {
	u, ok := auth.CurrentUser(c)
	if !ok {
		adminError(c, http.StatusForbidden, "forbidden")
		return
	}
	var req adminSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		adminError(c, http.StatusBadRequest, "invalid_request")
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
			// Blank means no change; the semantics of clearing back to the default value are left to be defined by a later task.
			continue
		}
		switch spec.key {
		case settingKeySiteDefaultLocale:
			if !s.supportedLocale(raw) {
				adminError(c, http.StatusBadRequest, "invalid_locale")
				return
			}
		case media.SettingKeyMediaMaxBytes:
			if n, err := strconv.ParseInt(raw, 10, 64); err != nil || n <= 0 {
				adminError(c, http.StatusBadRequest, "invalid_number")
				return
			}
		case settingKeyMediaAllowedMimes:
			if len(splitMimeList(raw)) == 0 {
				adminError(c, http.StatusBadRequest, "invalid_mime")
				return
			}
		case settingKeyMediaUserQuotaBytes:
			if n, err := strconv.ParseInt(raw, 10, 64); err != nil || n < 0 {
				adminError(c, http.StatusBadRequest, "invalid_number")
				return
			}
		case store.SettingKeyOptimizeMinReviews:
			if n, err := strconv.Atoi(raw); err != nil || n < store.MinOptimizeMinReviews {
				adminError(c, http.StatusBadRequest, "optimize_min_reviews_too_low")
				return
			}
		}
		if err := store.PutSetting(ctx, s.db, spec.key, raw, store.Ptr(u.ID), now); err != nil {
			s.logger.Error("spa admin: save setting failed", "key", spec.key, "error", err)
			adminError(c, http.StatusInternalServerError, "save_failed")
			return
		}
		changed = append(changed, spec.key)
	}

	// Sensitive keys: only a non-empty new value is accepted, stored AES-GCM encrypted, and never echoed back.
	if s.secrets != nil {
		keys, err := store.SensitiveSettingKeys(ctx, s.db)
		if err != nil {
			s.logger.Error("spa admin: list sensitive keys failed", "error", err)
			adminError(c, http.StatusInternalServerError, "save_failed")
			return
		}
		for _, key := range keys {
			raw, present := req.Values[key]
			if !present || strings.TrimSpace(raw) == "" {
				continue
			}
			if err := store.PutSecret(ctx, s.db, s.secrets, key, raw, store.Ptr(u.ID), now); err != nil {
				s.logger.Error("spa admin: save secret failed", "key", key, "error", err)
				adminError(c, http.StatusInternalServerError, "save_failed")
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
