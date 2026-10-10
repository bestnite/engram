package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/cardtype"
)

// CardTypesResponse 是题型元数据的响应体；kinds 按 kind 字典序，结果稳定。
type CardTypesResponse struct {
	Kinds []cardtype.Description `json:"kinds"`
}

// CardTypes 返回全部题型的自描述（静态元数据，不含任何用户状态）；REST 与 MCP 共用。
// 前端据此渲染编辑表单与复习控件，agent 据此知道每个题型有哪些字段、哪些必填。
func (a *API) CardTypes() CardTypesResponse {
	return CardTypesResponse{Kinds: cardtype.Descriptions()}
}

// cardTypes 是 GET /api/v1/card-types，scope: read。
func (a *API) cardTypes(c *gin.Context) {
	c.JSON(http.StatusOK, a.CardTypes())
}
