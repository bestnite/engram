package web

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/cardtype"
	"git.nite07.com/nite/engram/internal/store"
)

// registerShareBrowseRoutes 挂载免注册只读浏览页（M5-3）。
//
// GET /s/:token 返回应用壳，由客户端路由渲染只读浏览页；内容与口令解锁走 api/v1/share/*
// 的 JSON 端点（spa_share.go）。链接本身（可选加口令）就是凭据，因此不要求登录。
func (s *Server) registerShareBrowseRoutes(router *gin.Engine) {
	if s.shareLinks == nil || s.decks == nil || s.notes == nil {
		return
	}
	router.GET("/s/:token", s.shareBrowseRoute)
	// SPA 的同源 JSON 传输层（spa_share.go）；可达性与媒体授权不变。
	s.registerSPAShareRoutes(router)
}

// resolveShareLink 解析路径里的明文 token 并取回卡组；无效时写 404 并返回 false。
// 撤销、过期或不存在一律 404，不区分三者，避免泄漏 token 历史。
func (s *Server) resolveShareLink(c *gin.Context) (*store.ShareLink, *store.Deck, bool) {
	link, err := s.shareLinks.Resolve(c.Request.Context(), c.Param("token"), time.Now().UTC())
	if err != nil {
		// ErrShareLinkNotFound 与其它错误统一按 404 处理：不向匿名访问者泄露内部错误细节。
		c.AbortWithStatus(http.StatusNotFound)
		return nil, nil, false
	}
	deck, err := s.decks.ByID(c.Request.Context(), link.DeckID)
	if err != nil {
		c.AbortWithStatus(http.StatusNotFound)
		return nil, nil, false
	}
	return link, deck, true
}

// recordShareGrant 把「该服务端会话打开过该卡组」登记下来（L3，DESIGN.md §5）：
// 登录用户通过分享链接打开的卡组计入其会话级可见集合，媒体读取（GET /media/<sha256>）据此放行。
//
// 过期时刻取链接过期与会话过期的较早者（store.ShareSessionStore.Grant）。登记失败只记一条英文日志、
// 不阻断浏览：页面内容已经可以渲染，失败的后果是图片拿不到，不该把整个浏览页变成 500。
// 匿名访客没有服务端会话，无处登记——他们本就读不到媒体（/media 要求登录），跳过即可。
func (s *Server) recordShareGrant(c *gin.Context, deck *store.Deck, link *store.ShareLink) {
	if s.shareSessions == nil {
		return
	}
	sess, ok := auth.CurrentSession(c)
	if !ok {
		return
	}
	if err := s.shareSessions.Grant(c.Request.Context(), sess.ID, deck.ID, link.ExpiresAt, sess.ExpiresAt); err != nil {
		s.logger.Error("record share session grant failed", "deck_id", deck.ID, "error", err)
	}
}

// shareNoteView 把一个 note 的所有 card 渲染成正反面 HTML（与预览同一套渲染管线）。
// 返回清洗后的正反面 HTML；单张卡的任一渲染失败即返回错误，由调用方决定跳过与否。
func (s *Server) shareNoteView(note store.Note) (front, back string, err error) {
	fields, err := store.ParseFields(note.FieldsJSON)
	if err != nil {
		return "", "", err
	}
	t, ok := cardtype.Lookup(note.Kind)
	if !ok {
		return "", "", cardtype.UnknownKindError(note.Kind)
	}
	cards, err := cardtype.Cards(cardtype.Note{Kind: note.Kind, Fields: fields})
	if err != nil {
		return "", "", err
	}
	var frontParts, backParts []string
	for _, card := range cards {
		f, err := t.Render(card, cardtype.SideFront)
		if err != nil {
			return "", "", err
		}
		b, err := t.Render(card, cardtype.SideBack)
		if err != nil {
			return "", "", err
		}
		frontHTML, err := renderSide(f)
		if err != nil {
			return "", "", err
		}
		backHTML, err := renderSide(b)
		if err != nil {
			return "", "", err
		}
		frontParts = append(frontParts, frontHTML)
		backParts = append(backParts, backHTML)
	}
	return strings.Join(frontParts, "\n"), strings.Join(backParts, "\n"), nil
}

// renderHTMLStatus 写出整页 HTML 并带指定状态码（口令错误要返回 401 而不是 200）。
// 渲染失败时响应头可能已发出，只能记一条英文日志（与 renderHTML 的约定一致）。
func renderHTMLStatus(c *gin.Context, status int, comp templ.Component) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Status(status)
	if err := comp.Render(c.Request.Context(), c.Writer); err != nil {
		slog.Error("render template failed", "error", err, "path", c.Request.URL.Path)
	}
}
