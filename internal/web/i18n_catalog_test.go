package web

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// 本文件把 i18n 门禁加严到 Go 侧（M8-3）。
//
// 模板层只能保证模板里没有硬编码文案；它看不见 handler 里
// `loc.T("...")` 引用的 key 是否真的存在。go-i18n 缺 key 时只在运行期记一条日志并回退成
// key 本身，页面上会出现裸 key —— 只有真的点到那个页面才会发现。这个测试在编译期就拦住：
// 把 internal/web 里所有静态书写的翻译 key 与两份语言包逐一对照，缺一个即失败。
//
// 只匹配「字面量直接作为第一个参数」的调用（`.T("k")` / `.Tf("k", ...)`）；动态拼接
// （`loc.T("cardtype."+kind)`、`loc.T(stageKey(x))`）不在此列 —— 它们由各自的枚举测试覆盖
// （例如 internal/i18n 的题型标签测试）。

// translationCallRe 匹配 .T("key") 与 .Tf("key", ...) 里的静态 key。
// 结尾要求紧跟逗号或右括号，这样 `loc.T("prefix." + x)` 这类动态拼接不会被误当成
// 一个完整 key（它们的枚举完整性由各自的测试覆盖）。
var translationCallRe = regexp.MustCompile(`\.Tf?\("([A-Za-z0-9_.]+)"\s*[,)]`)

// TestHandlerTranslationKeysExistInBothCatalogs 断言 handler 引用的每个 key 都在中英
// 两套语言包里存在。
func TestHandlerTranslationKeysExistInBothCatalogs(t *testing.T) {
	keys := handlerTranslationKeys(t)
	if len(keys) == 0 {
		t.Fatal("no translation keys found in internal/web; the scanner regex is broken")
	}
	catalogs := map[string]map[string]bool{}
	for _, code := range []string{"zh-CN", "en"} {
		catalogs[code] = catalogKeys(t, filepath.Join("..", "i18n", "locales", code+".yaml"))
	}
	for _, key := range keys {
		for _, code := range []string{"zh-CN", "en"} {
			if !catalogs[code][key] {
				t.Errorf("locale %s is missing the message %q referenced from internal/web", code, key)
			}
		}
	}
}

// TestCatalogsHaveIdenticalKeys 是 parity 检查的 Go 侧副本：两份语言包的 key 集合必须
// 完全一致，多一个或少一个都失败并点名。
func TestCatalogsHaveIdenticalKeys(t *testing.T) {
	zh := catalogKeys(t, filepath.Join("..", "i18n", "locales", "zh-CN.yaml"))
	en := catalogKeys(t, filepath.Join("..", "i18n", "locales", "en.yaml"))
	for key := range zh {
		if !en[key] {
			t.Errorf("en.yaml is missing the key %q", key)
		}
	}
	for key := range en {
		if !zh[key] {
			t.Errorf("zh-CN.yaml is missing the key %q", key)
		}
	}
}

// handlerTranslationKeys 扫描 internal/web 下非测试的 .go 文件，收集静态翻译 key。
func handlerTranslationKeys(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	seen := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, m := range translationCallRe.FindAllStringSubmatch(string(data), -1) {
			seen[m[1]] = true
		}
	}
	out := make([]string, 0, len(seen))
	for key := range seen {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// catalogKeys 读取一份 YAML 语言包的 message id 集合。
func catalogKeys(t *testing.T, path string) map[string]bool {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read catalog %s: %v", path, err)
	}
	var msgs []struct {
		ID string `yaml:"id"`
	}
	if err := yaml.Unmarshal(data, &msgs); err != nil {
		t.Fatalf("parse catalog %s: %v", path, err)
	}
	out := make(map[string]bool, len(msgs))
	for _, m := range msgs {
		if m.ID != "" {
			out[m.ID] = true
		}
	}
	if len(out) == 0 {
		t.Fatalf("catalog %s parsed to zero messages", path)
	}
	return out
}
