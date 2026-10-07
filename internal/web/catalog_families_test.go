package web

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/i18n"
)

// 这几处键是运行时拼出来的（"mail.notice.credential." + kind 之类），成员写成调用点上的字面量参数：
// 漏登记不会编译失败，也不会在测试里报错，只会在用户收到的邮件正文里印出键名本身。
// 所以逐个扫调用点，断言每个成员都有真译文——新增一种凭据变更类型而忘了补语言包会当场变红。
var (
	credentialKindRe = regexp.MustCompile(`notifyCredentialChanged\([^)]*?"([a-z_]+)"\)`)
	accountStatusRe  = regexp.MustCompile(`notifyAccountStatus\([^)]*?"([a-z_]+)"\)`)
	// registerInputErrorCode 的返回值就是 auth.error.<code> 的后缀；函数体本身是
	// 一串 `if … { return "<code>" }`，按 `return "…"` 取出全部 code。
	registerCodeRe = regexp.MustCompile(`return "([a-z_]+)"`)
)

func TestComposedKeysFromCallSitesAreRegistered(t *testing.T) {
	tr, err := i18n.New()
	if err != nil {
		t.Fatalf("i18n.New: %v", err)
	}
	locales := map[string]*i18n.Localizer{
		"zh-CN": tr.Localizer(tr.Pick("zh-CN", "", "")),
		"en":    tr.Localizer(tr.Pick("en", "", "")),
	}

	credentialKinds := matchInSources(t, credentialKindRe)
	accountStatuses := matchInSources(t, accountStatusRe)
	registerCodes := matchInBody(t, "auth.go", "func registerInputErrorCode(", registerCodeRe)

	// 空集合会让下面每个断言都空转，所以先证明扫描抓到了东西。
	if len(credentialKinds) == 0 || len(accountStatuses) == 0 || len(registerCodes) == 0 {
		t.Fatalf("scan found nothing (credentials=%v statuses=%v register_codes=%v)",
			credentialKinds, accountStatuses, registerCodes)
	}

	for prefix, members := range map[string][]string{
		"mail.notice.credential.": credentialKinds,
		"mail.notice.account.":    accountStatuses,
		"auth.error.":             registerCodes,
	} {
		for _, member := range members {
			key := prefix + member
			for code, loc := range locales {
				if got := loc.T(key); got == key || strings.TrimSpace(got) == "" {
					t.Errorf("%s: composed key %q has no translation (call site uses %q)", code, key, member)
				}
			}
		}
	}
}

// matchInSources 把包目录下所有非测试 .go 文件的文本拼起来跑一次正则。
func matchInSources(t *testing.T, re *regexp.Regexp) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	var text strings.Builder
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		text.Write(raw)
		text.WriteString("\n")
	}
	return uniqueMatches(re.FindAllStringSubmatch(text.String(), -1))
}

// matchInBody 只在某个函数体（从 marker 到下一个顶格的右花括号）里跑正则，避免匹配到别处的 return。
func matchInBody(t *testing.T, file, marker string, re *regexp.Regexp) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(".", file))
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	src := string(raw)
	start := strings.Index(src, marker)
	if start < 0 {
		t.Fatalf("%s no longer contains %q; the scan needs updating", file, marker)
	}
	rest := src[start:]
	end := strings.Index(rest, "\n}\n")
	if end < 0 {
		t.Fatalf("%s: could not find the end of %q", file, marker)
	}
	return uniqueMatches(re.FindAllStringSubmatch(rest[:end], -1))
}

func uniqueMatches(matches [][]string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, m := range matches {
		if len(m) > 1 && !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}
