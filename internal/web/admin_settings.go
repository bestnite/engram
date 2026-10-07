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

// 系统设置的可编辑项定义（DESIGN.md §8.4；ROADMAP.md M6-5）。
//
// 取值优先级与环境变量语义复用 internal/config：环境变量 > settings 表 > 默认值，
// settings 表按请求现读。SSR 设置页删除后，页面的读写在 /api/v1/admin/settings 的 JSON
// 端点（spa_admin_settings.go）上；这里保留该端点复用的设置项规格、生效值与读数工具。

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
			key: media.SettingKeyMediaMaxBytes, envVar: media.EnvMediaMaxBytes,
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

// optimizeSettingSpecs 是「参数优化」区块的设置项（ROADMAP.md M9-12）。
// 门槛下限由 store.MinOptimizeMinReviews 强制，表单拒绝低于下限的值；存量的低于下限的值
// 在读取路径会被钳到下限（store.OptimizeMinReviews）。
func optimizeSettingSpecs() []settingSpec {
	return []settingSpec{
		{
			key:      store.SettingKeyOptimizeMinReviews,
			labelKey: "admin.setting.optimize_min_reviews", hintKey: "admin.setting.optimize_min_reviews.hint",
			def: func(*i18n.Localizer) string { return strconv.Itoa(store.DefaultOptimizeMinReviews) },
		},
	}
}

// effectiveSetting 解析一个设置的生效值与其来源：环境变量 > settings 表 > 默认值。
// 复用 internal/config 的 Source 常量与优先级语义；settings 表按请求现读。
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
