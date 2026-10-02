package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"

	"git.nite07.com/nite/engram/internal/i18n"
)

// TestErrorCatalogueCoversEveryCode 是 M4-9 的验收测试，与 M2-10 枚举题型标签同构：
//
//  1. 解析源码里**全部** Code* 常量（api 包所有非测试 .go 文件 + store 包卡组包错误码），
//     任何一个 code 在两份语言包里缺 error.<code> 文案，或在 errorMessages 兜底表里
//     缺登记，测试即失败；
//  2. 走生产路径（errorMessage + 请求语言包）断言同一个 code 在 en 与 zh-CN 下产出
//     **不同**的 message，证明 message 真的随 Accept-Language 本地化，而不是硬编码英文。
//
// 解析源码而非读一张手写清单，是为了让“新加一个 Code 常量却忘了补文案”必然被抓住。
func TestErrorCatalogueCoversEveryCode(t *testing.T) {
	apiCodes := codeConstantsInDir(t, ".")
	storeCodes := codeConstantsInFile(t, filepath.Join("..", "store", "package.go"))
	codes := append(apiCodes, storeCodes...)
	if len(apiCodes) < 11 {
		t.Fatalf("found only %d Code* constants in the api package, expected at least 11", len(apiCodes))
	}
	if len(storeCodes) < 4 {
		t.Fatalf("found only %d Code* constants in store/package.go, expected at least 4", len(storeCodes))
	}

	zh := loadCatalog(t, filepath.Join("..", "i18n", "locales", "zh-CN.yaml"))
	en := loadCatalog(t, filepath.Join("..", "i18n", "locales", "en.yaml"))

	tr, err := i18n.New()
	if err != nil {
		t.Fatalf("i18n.New(): %v", err)
	}
	enLoc := tr.Localizer(tr.Pick("", "en", ""))
	zhLoc := tr.Localizer(tr.Pick("", "zh-CN", ""))

	for _, code := range codes {
		key := "error." + code
		if strings.TrimSpace(zh[key]) == "" {
			t.Errorf("code %q: zh-CN.yaml is missing the message %q", code, key)
		}
		if strings.TrimSpace(en[key]) == "" {
			t.Errorf("code %q: en.yaml is missing the message %q", code, key)
		}
		if got := defaultErrorMessage(code); got == "" {
			t.Errorf("code %q is not registered in errorMessages", code)
		} else if en[key] != "" && en[key] != got {
			t.Errorf("code %q: en.yaml message %q differs from the English fallback %q", code, en[key], got)
		}

		enMsg := messageFor(t, enLoc, code)
		zhMsg := messageFor(t, zhLoc, code)
		if enMsg == "" || zhMsg == "" {
			t.Errorf("code %q: empty message (en=%q zh=%q)", code, enMsg, zhMsg)
		}
		if enMsg == key || zhMsg == key {
			t.Errorf("code %q resolved to the raw key instead of a translation (en=%q zh=%q)", code, enMsg, zhMsg)
		}
		if enMsg == zhMsg {
			t.Errorf("code %q yields the same message for en and zh-CN: %q", code, enMsg)
		}
	}
}

// messageFor 走生产路径解析 message：把语言包放进请求 context，再调用 errorMessage。
func messageFor(t *testing.T, loc *i18n.Localizer, code string) string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := httptest.NewRequest("GET", "/api/v1/decks", nil)
	req = req.WithContext(i18n.WithLocalizer(req.Context(), loc))
	c.Request = req
	return errorMessage(c, code)
}

// codeConstantsInDir 解析目录下所有非测试 .go 文件里的 Code* 字符串常量。
func codeConstantsInDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		out = append(out, codeConstantsInFile(t, filepath.Join(dir, e.Name()))...)
	}
	return out
}

// codeConstantsInFile 解析单个 Go 文件里所有形如 CodeX = "..." 的常量值。
func codeConstantsInFile(t *testing.T, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var out []string
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if !strings.HasPrefix(name.Name, "Code") || i >= len(vs.Values) {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				val, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("unquote %s in %s: %v", lit.Value, path, err)
				}
				out = append(out, val)
			}
		}
	}
	return out
}

// loadCatalog 读取语言包为 id → translation 映射。
func loadCatalog(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var msgs []struct {
		ID          string `yaml:"id"`
		Translation string `yaml:"translation"`
	}
	if err := yaml.Unmarshal(data, &msgs); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	out := make(map[string]string, len(msgs))
	for _, m := range msgs {
		out[m.ID] = m.Translation
	}
	return out
}
