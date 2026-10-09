package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/cardtype"
)

// cardTypesResponse 是题型元数据的响应体；kinds 按 kind 字典序，结果稳定。
type cardTypesResponse struct {
	Kinds []cardtype.Description `json:"kinds"`
}

// cardTypes 返回全部题型的自描述（静态元数据，不含任何用户状态），scope: read。
// 前端据此渲染编辑表单与复习控件，无需再各自硬编码一张题型表。
func (a *API) cardTypes(c *gin.Context) {
	c.JSON(http.StatusOK, cardTypesResponse{Kinds: cardtype.Descriptions()})
}
