package i18n

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// keyLiteralRe 抓 .go 文件里的字符串字面量；与语言包键集合求交后，得到「代码直接按名字读的键」。
var keyLiteralRe = regexp.MustCompile(`"([a-zA-Z][a-zA-Z0-9._-]*)"`)

// keyPrefixConcatRe 抓「引号里的点号前缀 + 拼接」形态，例如 "mail.notice.credential." + kind。
// 这类键名在源码里凑不出完整字符串，只能靠前缀识别整个家族。
var keyPrefixConcatRe = regexp.MustCompile(`"([a-zA-Z][a-zA-Z0-9_.]*\.)"\s*\+`)

// TestCatalogOnlyContainsKeysTheCodeCanRead 把「语言包该有哪些键」变成一条可执行的规则：
// 每个条目都必须有 Go 代码读得到它——键名在某个 .go 文件里出现（注释与测试也算），
// 或落在某个「点号前缀 + 拼接」家族下。
//
// 为什么需要它：这两份 YAML 曾经整份镜像 SPA 语言包（1062 条里 864 条 Go 从不读），于是
// 「语言包里有这条」被误当成「用户能看到这条文案」，连注释都开始引用其实已无人解析的键。
// 现在多一个无人读的条目就会红。
//
// 扫描是静态近似，只认字面量与「引号点号前缀 +」两种形态，不认 fmt.Sprintf 之类。
// 红了先分清「这个键确实没人读」与「扫描器该扩展」，别直接放宽这条用例。
func TestCatalogOnlyContainsKeysTheCodeCanRead(t *testing.T) {
	literals, families := scanKeyReferences(t, filepath.Join("..", ".."))
	for _, name := range []string{"locales/zh-CN.yaml", "locales/en.yaml"} {
		catalog := catalogKeys(t, name)
		var unread []string
		for key, translation := range catalog {
			if strings.TrimSpace(translation) == "" || translation == key {
				t.Errorf("%s: key %q has no real translation", name, key)
			}
			if !keyIsRead(key, literals, families) {
				unread = append(unread, key)
			}
		}
		sort.Strings(unread)
		if len(unread) > 0 {
			t.Errorf("%s: %d key(s) no Go code reads — delete them, or extend the scanner if they are composed another way: %v",
				name, len(unread), unread)
		}
	}
}

// scanKeyReferences 读取仓库里全部 .go 文件的文本，返回字面量集合与拼接前缀集合。
// 注释同样计入：这只会让某个键被判成「有人读」，不会反过来把活键判成死的。
func scanKeyReferences(t *testing.T, root string) (map[string]bool, []string) {
	t.Helper()
	literals := map[string]bool{}
	families := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "dist", "data", "screenshots":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(raw)
		for _, m := range keyLiteralRe.FindAllStringSubmatch(text, -1) {
			literals[m[1]] = true
		}
		for _, m := range keyPrefixConcatRe.FindAllStringSubmatch(text, -1) {
			families[m[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan sources: %v", err)
	}
	// 扫描器自身失效时不要假绿：一个键都没抓到说明路径或形态判断坏了。
	if len(literals) == 0 || len(families) == 0 {
		t.Fatalf("scanner found nothing (literals=%d families=%d); check the walk root", len(literals), len(families))
	}
	out := make([]string, 0, len(families))
	for f := range families {
		out = append(out, f)
	}
	return literals, out
}

// keyIsRead 报告某个键是否被代码读到：按名字直接读，或落在某个拼接家族下。
func keyIsRead(key string, literals map[string]bool, families []string) bool {
	if literals[key] {
		return true
	}
	for _, family := range families {
		if strings.HasPrefix(key, family) {
			return true
		}
	}
	return false
}
