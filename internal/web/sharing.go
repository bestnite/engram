package web

import (
	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// registerSharingRoutes 挂载共享管理页。
//
// 共享管理页只对 owner 开放（判定走 auth.DeckAccess）。GET 返回应用壳；所有写操作都走
// /api/v1/decks/:id/sharing 下的 JSON 端点，并过 CSRF 中间件。
// 依赖未装配时跳过，保证 M0 阶段的测试仍能构造 Server。
func (s *Server) registerSharingRoutes(router *gin.Engine) {
	if s.sessions == nil || s.decks == nil || s.grants == nil || s.users == nil {
		return
	}
	// GET /decks/:id/sharing 返回应用壳，由客户端路由渲染共享管理页，数据走下面的 JSON 端点。
	router.GET("/decks/:id/sharing", s.sharingPageRoute)
	// SPA 的共享读写端点（sharing_api.go）：数据仍走同一批 store 方法。
	router.GET("/api/v1/decks/:id/sharing", s.sharingGet)
	router.POST("/api/v1/decks/:id/sharing/grants", s.sessions.CSRFMiddleware(), s.sharingGrant)
	router.PATCH("/api/v1/decks/:id/sharing/grants/:userID", s.sessions.CSRFMiddleware(), s.sharingGrant)
	router.DELETE("/api/v1/decks/:id/sharing/grants/:userID", s.sessions.CSRFMiddleware(), s.sharingRevoke)
	// 分享同意制（L3）：待接受的邀请、接受/拒绝，以及用户自己的接收策略。
	s.registerShareInviteRoutes(router)
	router.POST("/api/v1/decks/:id/sharing/links", s.sessions.CSRFMiddleware(), s.sharingLinkCreate)
	router.DELETE("/api/v1/decks/:id/sharing/links/revoke/:digest", s.sessions.CSRFMiddleware(), s.sharingLinkRevoke)
	router.DELETE("/api/v1/decks/:id/sharing/links", s.sessions.CSRFMiddleware(), s.sharingLinkRevokeAll)
}

// sharingPageRoute 提供 GET /decks/:id/sharing：返回应用壳，
// 由客户端路由渲染共享管理页，数据仍走既有 JSON 端点。
//
// 判权与迁移前的 SSR 共享页逐项一致：先要求已登录会话（匿名重定向登录页），再按 owner 角色
// 判定卡组归属——editor/reader 与陌生用户仍回 403，不因切壳而把共享壳交给无权用户。
func (s *Server) sharingPageRoute(c *gin.Context) {
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	deckID, ok := s.deckIDParam(c)
	if !ok {
		return
	}
	if _, ok := s.loadDeckForRole(c, user, deckID, store.RoleOwner); !ok {
		return
	}
	s.shell.ServeIndex(c)
}

// shortDigest 截断 token 摘要用于列表展示；摘要不可反推，展示前缀不构成密钥泄漏。
func shortDigest(digest string) string {
	if len(digest) <= 12 {
		return digest
	}
	return digest[:12] + "…"
}

// shareLinkPasswordHasher 是分享链接口令的哈希器。
// 口令是“给人转发时顺手加的一道锁”，不是账号主密码，因此复用项目统一的 argon2id 编码格式，
// 参数从 DefaultParams 起步；将来升级强度只影响新口令，旧哈希自带参数仍可验证。
var shareLinkPasswordHasher = auth.DefaultPasswordHasher()
