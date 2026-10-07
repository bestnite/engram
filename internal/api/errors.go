// Package api 提供 /api/v1 的 REST handler 与鉴权、限流中间件
// （ROADMAP.md M4-2、M4-3）。
package api

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/store"
)

// 稳定的错误 code（英文常量，供调用方程序化判断）。
//
// REST 与 MCP 的错误统一返回稳定英文 code 与英文 message，
// 不随请求的 Accept-Language 改变。SPA 前端根据稳定 code 映射本地化提示，不解析后端 message。
// 新增 code 时必须在 errorMessages 登记英文文案。
const (
	CodeUnauthorized   = "unauthorized"
	CodeInvalidAPIKey  = "invalid_api_key"
	CodeScopeRequired  = "scope_required"
	CodeRateLimited    = "rate_limited"
	CodeInvalidRequest = "invalid_request"
	CodeNotFound       = "not_found"
	CodeForbidden      = "forbidden"
	// CodeInsufficientRole 表示用户对卡组有访问权但角色不够（如 reader 试图改卡），
	// 与“完全无访问权”的 CodeForbidden 区分，便于调用方精确判断（M5-1）。
	CodeInsufficientRole = "insufficient_role"
	// CodeScopeNotGrantable 表示请求要创建的 key 含只有管理员才能持有的 scope
	// （当前只有 admin）「admin 只能发给管理员账号」。
	CodeScopeNotGrantable = "scope_not_grantable"
	CodeConflict          = "conflict"
	CodeVersionConflict   = "version_conflict"
	CodeInternal          = "internal_error"
	// CodeDeckNameInvalid 表示建卡组/改名时卡组名不满足长度或字符规则
	// （与卡组包 manifest 同源；store 层 ErrDeckNameInvalid）。
	CodeDeckNameInvalid = "deck_name_invalid"
	// CodeDeckDescriptionInvalid 表示描述超长或含非法字符。
	CodeDeckDescriptionInvalid = "deck_description_invalid"
	// CodeMediaNotReadable 表示 note 写入引用了写入者无法读取的媒体，写入被拒（写前校验）。
	// 与 store.CodeMediaNotReadable 同值。
	CodeMediaNotReadable = "media_not_readable"
	// CodeCSRFFailed 表示 CSRF token 缺失或校验失败。
	CodeCSRFFailed = "csrf_failed"
	// CodeCSRFNoSession 表示无活跃会话进行 CSRF 校验。
	CodeCSRFNoSession = "csrf_no_session"
	// CodeInvalidCredentials 表示登录凭据错误。
	CodeInvalidCredentials = "invalid_credentials"
	// CodeUserDisabled 表示账号已被禁用。
	CodeUserDisabled = "user_disabled"
	// CodeTOTPRequired 表示需要进行 TOTP 二次验证。
	CodeTOTPRequired = "totp_required"
	// CodeTOTPInvalid 表示登录第二步提交的验证码/恢复码无效。
	CodeTOTPInvalid = "totp_invalid"
	// CodeTOTPChallengeExpired 表示第二步凭据缺失或已过期（必须从第一步重新登录）。
	CodeTOTPChallengeExpired = "totp_challenge_expired"
)

// errorBody 是错误包壳的 error 对象：{"error":{"code":"...","message":"..."}}。
type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// errorMessages 是每个稳定 code 的英文文案表。
//
// 它是 “code → 英文文案” 的唯一登记处：REST 与 MCP 的错误出口都经 ErrorMessage / formatErrorMessage 取值。
// 这里同时覆盖 store 层卡组包导入会透出的 code（mapPackageError 会原样返回它们）。
var errorMessages = map[string]string{
	CodeUnauthorized:         "Authentication is required.",
	CodeInvalidAPIKey:        "The API key is invalid, expired, or revoked.",
	CodeScopeRequired:        "This API key does not have the required scope.",
	CodeRateLimited:          "Rate limit exceeded. Try again later.",
	CodeInvalidRequest:       "The request is invalid.",
	CodeNotFound:             "The requested resource was not found.",
	CodeForbidden:            "You do not have access to this resource.",
	CodeInsufficientRole:     "Your role on this deck is not sufficient for this action.",
	CodeScopeNotGrantable:    "Only administrator accounts can be granted the admin scope.",
	CodeConflict:             "The request conflicts with the current state.",
	CodeVersionConflict:      "The resource was changed by someone else. Reload and try again.",
	CodeInternal:             "An internal error occurred.",
	CodeCSRFFailed:           "Invalid or missing CSRF token.",
	CodeCSRFNoSession:        "No active session for CSRF validation.",
	CodeInvalidCredentials:   "Invalid username or password.",
	CodeUserDisabled:         "This account has been disabled.",
	CodeTOTPRequired:         "Two-factor authentication required.",
	CodeTOTPInvalid:          "The two-factor code is not valid.",
	CodeTOTPChallengeExpired: "The two-factor challenge has expired. Sign in again.",

	// 卡组包导入的稳定 code（store.PackageError.Code）。
	store.CodePackageUnsafeEntry:     "The package contains an unsafe entry.",
	store.CodePackageTooLarge:        "The package exceeds the allowed size.",
	store.CodePackageBadFormat:       "The package format is invalid.",
	store.CodePackageUnknownKind:     "The package contains a card type this instance does not know.",
	store.CodePackageUnsafeMedia:     "The package contains media of a disallowed type.",
	store.CodePackageDeckMetaInvalid: "The deck name or description in the package is invalid.",
	store.CodePackageQuotaExceeded:   "The imported media would exceed your media quota.",
	store.CodePackageMediaForbidden:  "The package references media you cannot read.",

	// 写前媒体可读性校验的稳定 code（store.MediaWriteError.Code）。
	CodeMediaNotReadable: "You referenced media you cannot read; upload it first or obtain access.",

	// 建卡组/改名的稳定 code（store.ErrDeckNameInvalid）。
	CodeDeckNameInvalid:        "The deck name is invalid.",
	CodeDeckDescriptionInvalid: "The deck description is invalid.",
}

// defaultErrorMessage 返回 code 的英文兜底文案；未登记的 code 返回空串。
func defaultErrorMessage(code string) string { return errorMessages[code] }

// errorMessage 返回 code 的英文 message。
func errorMessage(c *gin.Context, code string) string {
	return ErrorMessage(c.Request.Context(), code)
}

// ErrorMessage 返回 code 对应的稳定英文文案。
// 未登记的 code 回退为 code 本身。
func ErrorMessage(_ context.Context, code string) string {
	if msg := errorMessages[code]; msg != "" {
		return msg
	}
	return code
}

// formatErrorMessage 组合最终 message：英文基底 + 可选动态细节。
// 细节为空、或恰等于该 code 的英文文案时只返回英文基底，避免出现重复。
func formatErrorMessage(ctx context.Context, code, detail string) string {
	base := ErrorMessage(ctx, code)
	detail = strings.TrimSpace(detail)
	if detail == "" || detail == base || detail == errorMessages[code] {
		return base
	}
	if strings.HasPrefix(detail, base+" — ") {
		return detail
	}
	return base + " — " + detail
}

// ErrorText 把任意 service error 渲染成 "code: message"，message 为稳定英文。
// MCP 工具没有 HTTP 错误包壳，用它保证与 REST 一致的 code + 英文文案。
func ErrorText(ctx context.Context, err error) string {
	se := asServiceError(err)
	return se.Code + ": " + formatErrorMessage(ctx, se.Code, se.Message)
}

// abortError 写出统一错误包壳并中止请求；所有 API 错误都必须经这里返回，避免形态漂移。
//
// message 传空串表示“按 code 取英文文案”；非空时视为调用方给出的动态细节（如字段名、
// 上限值），追加在英文文案之后，绝不替换 code 的文案。
func abortError(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{"error": errorBody{
		Code:    code,
		Message: formatErrorMessage(c.Request.Context(), code, message),
	}})
}

// writeServiceError 把 service 层的 *ServiceError 映射成统一错误包壳；未识别错误按 500 处理。
// REST handler 与 MCP 工具共用同一批 service 方法，这里是 REST 侧的出口。
func writeServiceError(c *gin.Context, err error) {
	se := asServiceError(err)
	abortError(c, se.Status, se.Code, se.Message)
}
