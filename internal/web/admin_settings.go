package web

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"git.nite07.com/nite/engram/internal/config"
	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/media"
	"git.nite07.com/nite/engram/internal/store"
)

// Definitions of the editable system settings items.
//
// Value precedence and env var semantics are reused from internal/config: env vars > settings table > defaults,
// with the settings table read per request. After the SSR settings page was removed, the page's reads and writes live on the JSON
// endpoint /api/v1/admin/settings (admin_settings_api.go); the setting specs, effective values and reading helpers that endpoint reuses are kept here.

// settingSpec describes one editable setting: the settings key, the optional env var override and the default value source.
type settingSpec struct {
	key    string
	envVar string
	// def computes the default value under loc (the site name falls back to the language pack, the locale falls back to the built-in default, and so on).
	def func(loc *i18n.Localizer) string
}

// generalSettingSpecs are the settings items in the "general" section.
func generalSettingSpecs() []settingSpec {
	return []settingSpec{
		{
			key: store.SettingSiteName,
			def: func(loc *i18n.Localizer) string { return loc.T("app.name") },
		},
		{
			key: store.SettingSiteDefaultLocale,
			def: func(*i18n.Localizer) string { return i18n.DefaultLocaleCode },
		},
	}
}

// mediaSettingSpecs are the editable settings items in the "media" section, sharing the same keys as internal/web/media.go.
func mediaSettingSpecs() []settingSpec {
	return []settingSpec{
		{
			key: media.SettingKeyMediaMaxBytes, envVar: media.EnvMediaMaxBytes,
			def: func(*i18n.Localizer) string { return strconv.FormatInt(media.DefaultMaxBytes(), 10) },
		},
		{
			// Per-user total media quota: 0 = unlimited (default). 0 follows the project's existing convention (the daily cap also uses 0 for unlimited),
			// it is not an arbitrarily chosen number; it shows 0 when unconfigured, and the admin enables it by entering a positive number.
			key: settingKeyMediaUserQuotaBytes, envVar: envMediaUserQuotaBytes,
			def: func(*i18n.Localizer) string { return "0" },
		},
	}
}

// optimizeSettingSpecs are the settings items in the "parameter optimization" section.
// The lower bound of the threshold is enforced by store.MinOptimizeMinReviews and the form rejects values below it; existing values below the lower bound
// are clamped to the lower bound on the read path (store.OptimizeMinReviews).
func optimizeSettingSpecs() []settingSpec {
	return []settingSpec{
		{
			key: store.SettingKeyOptimizeMinReviews,
			def: func(*i18n.Localizer) string { return strconv.Itoa(store.DefaultOptimizeMinReviews) },
		},
	}
}

// effectiveSetting resolves a setting's effective value and its source: env vars > settings table > defaults.
// Reuses internal/config's Source constants and precedence semantics; the settings table is read per request.
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

// mediaDirSize recursively totals the media directory's usage; counts as 0 when the directory does not exist (nothing has been uploaded yet).
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

// humanBytes turns a byte count into a readable form; these are readings for the admin, so no localized unit conversion is done.
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
