package i18n

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// 语言包完整度报告（AGENTS.md §5 M8-4）。
//
// 口径（已冻结，页面与测试都以此为准）：基准 key 集合 = 所有语言包 key 集合的**并集**，
// 不是某一门语言。并集下任何一门语言独有的 key 都会被其它语言记为「缺失」，这正是
// 「完整度」要暴露的差异；若以 en 为基准，en 独有的 key 会漏报。两种口径在语言包完全
// 对齐时结果相同（本仓库由 Translator.CheckParity 在加载期强制对齐）。

// LocaleCoverage 是一种语言的翻译覆盖率。
type LocaleCoverage struct {
	// Code 是语言码（文件名去 .yaml）。
	Code string
	// Total 是基准（并集）key 集合的大小；Present 是该语言实际拥有的 key 数。
	Total   int
	Present int
	// Missing 是该语言相对基准缺少的 key，按字典序排列；完整时为 nil。
	Missing []string
}

// Percent 返回覆盖率百分比，向下取整；基准为空时视为 100。
func (c LocaleCoverage) Percent() int {
	if c.Total == 0 {
		return 100
	}
	return c.Present * 100 / c.Total
}

// Complete 表示该语言相对基准没有任何缺失 key。
func (c LocaleCoverage) Complete() bool { return len(c.Missing) == 0 }

// CatalogSets 读取 <dir>/*.yaml 语言包的 key 集合，文件名（去掉 .yaml）即语言码。
// 与 NewFromFS 不同，它**不做 parity 校验**：完整度报告正需要看见「不完整」的语言包，
// 因此这里只解析 id 集合，缺 key 不报错。
func CatalogSets(fsys fs.FS, dir string) (map[string]map[string]bool, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("i18n: read locale dir %q: %w", dir, err)
	}
	sets := make(map[string]map[string]bool)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := fs.ReadFile(fsys, path.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("i18n: read locale file %q: %w", e.Name(), err)
		}
		ids, err := messageIDs(data)
		if err != nil {
			return nil, fmt.Errorf("i18n: parse locale file %q: %w", e.Name(), err)
		}
		sets[strings.TrimSuffix(e.Name(), ".yaml")] = ids
	}
	return sets, nil
}

// Coverage 计算每种语言相对基准 key 集合的覆盖率（口径见本文件顶部注释）。
// 返回顺序按语言码字典序，保证页面渲染稳定。
func Coverage(sets map[string]map[string]bool) []LocaleCoverage {
	union := make(map[string]bool)
	for _, set := range sets {
		for id := range set {
			union[id] = true
		}
	}
	total := len(union)
	codes := make([]string, 0, len(sets))
	for code := range sets {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	out := make([]LocaleCoverage, 0, len(codes))
	for _, code := range codes {
		set := sets[code]
		missing := make([]string, 0)
		for id := range union {
			if !set[id] {
				missing = append(missing, id)
			}
		}
		sort.Strings(missing)
		out = append(out, LocaleCoverage{
			Code: code, Total: total, Present: total - len(missing), Missing: missing,
		})
	}
	return out
}

// Coverage 报告已加载语言包的覆盖率；基准为各语言包 key 集合的并集（口径见 Coverage）。
// 正常实例（CheckParity 通过）恒为 100%；报告页的价值在于新增语言时暴露缺口。
func (t *Translator) Coverage() []LocaleCoverage {
	return Coverage(t.ids)
}
