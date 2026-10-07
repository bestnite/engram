package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/store"
)

// TestErrorRegistryCoversEveryCode 解析源码里全部 Code* 常量（api 包所有非测试 .go 文件 + store 包卡组包错误码），
// 断言每一个 code 都在 errorMessages 注册表里登记了非空的稳定英文文案，且 ErrorMessage 返回该文案。
// 不再要求在 Go 语言包（zh-CN.yaml / en.yaml）里登记 REST/MCP 错误。
func TestErrorRegistryCoversEveryCode(t *testing.T) {
	apiCodes := codeConstantsInDir(t, ".")
	storeCodes := codeConstantsInFile(t, filepath.Join("..", "store", "package.go"))
	codes := append(apiCodes, storeCodes...)
	if len(apiCodes) < 11 {
		t.Fatalf("found only %d Code* constants in the api package, expected at least 11", len(apiCodes))
	}
	if len(storeCodes) < 4 {
		t.Fatalf("found only %d Code* constants in store/package.go, expected at least 4", len(storeCodes))
	}

	for _, code := range codes {
		msg := defaultErrorMessage(code)
		if strings.TrimSpace(msg) == "" {
			t.Errorf("code %q is not registered in errorMessages", code)
		}
		got := ErrorMessage(context.Background(), code)
		if got != msg {
			t.Errorf("ErrorMessage for code %q = %q, want %q", code, got, msg)
		}
	}
}

// TestAPIErrorOutputIdenticalAcrossAcceptLanguage 断言针对相同错误场景，
// 无论请求头 Accept-Language 为 zh-CN、en 或其它语言，API 错误响应的 JSON 输出都是
// 逐字节完全一致的稳定英文。
func TestAPIErrorOutputIdenticalAcrossAcceptLanguage(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "err_lang_test", store.RoleUser)
	readKey := seedKey(t, env.keys, user.ID, []string{store.ScopeRead}, nil)
	writeKey := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	r := env.router()

	testCases := []struct {
		name       string
		method     string
		path       string
		key        string
		body       string
		wantStatus int
		wantCode   string
	}{
		{
			name:       "unauthorized_missing_key",
			method:     http.MethodGet,
			path:       "/api/v1/decks",
			key:        "",
			wantStatus: http.StatusUnauthorized,
			wantCode:   CodeUnauthorized,
		},
		{
			name:       "invalid_api_key",
			method:     http.MethodGet,
			path:       "/api/v1/decks",
			key:        "fcard_bogus_key",
			wantStatus: http.StatusUnauthorized,
			wantCode:   CodeInvalidAPIKey,
		},
		{
			name:       "scope_required",
			method:     http.MethodPost,
			path:       "/api/v1/decks",
			key:        readKey.Plaintext,
			body:       `{"name":"test deck"}`,
			wantStatus: http.StatusForbidden,
			wantCode:   CodeScopeRequired,
		},
		{
			name:       "invalid_request_blank_name",
			method:     http.MethodPost,
			path:       "/api/v1/decks",
			key:        writeKey.Plaintext,
			body:       `{"name":"   "}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   CodeInvalidRequest,
		},
		{
			name:       "not_found",
			method:     http.MethodGet,
			path:       "/api/v1/decks/999999/notes",
			key:        readKey.Plaintext,
			wantStatus: http.StatusNotFound,
			wantCode:   CodeNotFound,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// 请求一：zh-CN
			recZh := doRequestWithHeader(r, tc.method, tc.path, tc.key, tc.body, "Accept-Language", "zh-CN,zh;q=0.9")
			// 请求二：en
			recEn := doRequestWithHeader(r, tc.method, tc.path, tc.key, tc.body, "Accept-Language", "en-US,en;q=0.9")
			// 请求三：其它语言
			recOther := doRequestWithHeader(r, tc.method, tc.path, tc.key, tc.body, "Accept-Language", "ja,fr;q=0.8")

			if recZh.Code != tc.wantStatus {
				t.Fatalf("zh status = %d, want %d (body %s)", recZh.Code, tc.wantStatus, recZh.Body.String())
			}

			// 逐字节完全一致
			if !bytes.Equal(recZh.Body.Bytes(), recEn.Body.Bytes()) {
				t.Errorf("error output differ between zh-CN and en:\nzh: %s\nen: %s",
					recZh.Body.String(), recEn.Body.String())
			}
			if !bytes.Equal(recZh.Body.Bytes(), recOther.Body.Bytes()) {
				t.Errorf("error output differ between zh-CN and other:\nzh:    %s\nother: %s",
					recZh.Body.String(), recOther.Body.String())
			}

			// 校验错误包壳与英文 message
			var envelope struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(recZh.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("parse error response json: %v", err)
			}
			if envelope.Error.Code != tc.wantCode {
				t.Errorf("error code = %q, want %q", envelope.Error.Code, tc.wantCode)
			}
			expectedBase := defaultErrorMessage(tc.wantCode)
			if !strings.HasPrefix(envelope.Error.Message, expectedBase) {
				t.Errorf("error message %q does not start with expected base %q", envelope.Error.Message, expectedBase)
			}
		})
	}
}

func doRequestWithHeader(r *gin.Engine, method, path, key, body, headerKey, headerVal string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if headerKey != "" {
		req.Header.Set(headerKey, headerVal)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// TestFormatErrorMessageDetailSemantics 校验动态细节追加与去重规则：
// 1. 空细节只返回基底英文；
// 2. 细节等于基底英文时不重复拼接；
// 3. 动态细节正常以 " — " 追加；
// 4. 已含基底前缀的细节不重复追加。
func TestFormatErrorMessageDetailSemantics(t *testing.T) {
	ctx := context.Background()

	// 1. 空细节
	if got, want := formatErrorMessage(ctx, CodeInvalidRequest, ""), "The request is invalid."; got != want {
		t.Errorf("empty detail = %q, want %q", got, want)
	}
	if got, want := formatErrorMessage(ctx, CodeInvalidRequest, "   "), "The request is invalid."; got != want {
		t.Errorf("whitespace detail = %q, want %q", got, want)
	}

	// 2. 细节与基底相同（避免重复）
	if got, want := formatErrorMessage(ctx, CodeInvalidRequest, "The request is invalid."), "The request is invalid."; got != want {
		t.Errorf("duplicate base detail = %q, want %q", got, want)
	}

	// 3. 动态细节追加
	detail := "fields are required"
	want := "The request is invalid. — fields are required"
	if got := formatErrorMessage(ctx, CodeInvalidRequest, detail); got != want {
		t.Errorf("dynamic detail = %q, want %q", got, want)
	}

	// 4. 已有前缀不重复
	alreadyFormatted := "The request is invalid. — fields are required"
	if got := formatErrorMessage(ctx, CodeInvalidRequest, alreadyFormatted); got != alreadyFormatted {
		t.Errorf("already formatted detail = %q, want %q", got, alreadyFormatted)
	}

	// 5. 未登记的 code 回退为 code 本身 + 细节
	if got, want := formatErrorMessage(ctx, "custom_code", "some error"), "custom_code — some error"; got != want {
		t.Errorf("unregistered code with detail = %q, want %q", got, want)
	}
}

// TestMCPErrorTextUsesSameEnglishMappingAsREST 断言 MCP ErrorText 渲染使用与 REST 相同的英文映射。
func TestMCPErrorTextUsesSameEnglishMappingAsREST(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		err      error
		wantCode string
		wantText string
	}{
		{
			err:      newServiceError(http.StatusBadRequest, CodeInvalidRequest, ""),
			wantCode: CodeInvalidRequest,
			wantText: "invalid_request: The request is invalid.",
		},
		{
			err:      newServiceError(http.StatusBadRequest, CodeInvalidRequest, "note not found"),
			wantCode: CodeInvalidRequest,
			wantText: "invalid_request: The request is invalid. — note not found",
		},
		{
			err:      newServiceError(http.StatusForbidden, CodeScopeRequired, "write"),
			wantCode: CodeScopeRequired,
			wantText: "scope_required: This API key does not have the required scope. — write",
		},
		{
			err:      newServiceError(http.StatusConflict, CodeVersionConflict, "resource changed"),
			wantCode: CodeVersionConflict,
			wantText: "version_conflict: The resource was changed by someone else. Reload and try again. — resource changed",
		},
		{
			err:      errors.New("plain internal failure"),
			wantCode: CodeInternal,
			wantText: "internal_error: An internal error occurred. — plain internal failure",
		},
	}

	for _, tc := range cases {
		text := ErrorText(ctx, tc.err)
		if text != tc.wantText {
			t.Errorf("ErrorText(%v) = %q, want %q", tc.err, text, tc.wantText)
		}

		// 断言与 formatErrorMessage 产出的文案完全对应
		se := asServiceError(tc.err)
		expectedRESTMsg := formatErrorMessage(ctx, se.Code, se.Message)
		expectedMCPText := se.Code + ": " + expectedRESTMsg
		if text != expectedMCPText {
			t.Errorf("ErrorText does not match REST format: got %q, want %q", text, expectedMCPText)
		}
	}
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
