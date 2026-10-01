// Package api 提供 /api/v1 的 REST handler 与鉴权、限流中间件
// （DESIGN.md §7.2、§7.3；AGENTS.md §5 M4-2、M4-3）。
package api

import (
	"github.com/gin-gonic/gin"
)

// 稳定的错误 code（英文常量，供调用方程序化判断，DESIGN.md §7.3）。
// message 目前是英文原文；本地化消息由后续任务 M4-9 统一接入，届时 code 保持不变。
// TODO(M4-9): resolve the message from the translation catalog by Accept-Language.
const (
	CodeUnauthorized    = "unauthorized"
	CodeInvalidAPIKey   = "invalid_api_key"
	CodeScopeRequired   = "scope_required"
	CodeRateLimited     = "rate_limited"
	CodeInvalidRequest  = "invalid_request"
	CodeNotFound        = "not_found"
	CodeForbidden       = "forbidden"
	CodeConflict        = "conflict"
	CodeVersionConflict = "version_conflict"
	CodeInternal        = "internal_error"
)

// errorBody 是错误包壳的 error 对象：{"error":{"code":"...","message":"..."}}。
type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// abortError 写出统一错误包壳并中止请求；所有 API 错误都必须经这里返回，避免形态漂移。
func abortError(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{"error": errorBody{Code: code, Message: message}})
}
