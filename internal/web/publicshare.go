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
	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/store"
	"git.nite07.com/nite/engram/internal/web/views"
)

// registerShareBrowseRoutes 挂载免注册只读浏览页（M5-3）。
//
// 浏览页刻意不挂 CSRF、也不要求登录：链接本身（可选加口令）就是凭据。解锁的 POST 同样在
// CSRF 之外——此时没有服务端会话可供绑定 token（与 /login、/register 同一处境，见 DESIGN.md
// §11 的“登录前表单暂不校验”），且它除渲染内容外不产生任何状态变更。
func (s *Server) registerShareBrowseRoutes(router *gin.Engine) {
	if s.shareLinks == nil || s.decks == nil || s.notes == nil {
		return
	}
	router.GET("/s/:token", s.shareBrowseRoute)
	router.POST("/s/:token/unlock", s.shareUnlock)
	// SPA 的同源 JSON 传输层（spa_share.go）：与上面的 SSR 处理器并存，不遮蔽；可达性与媒体授权不变。
	s.registerSPAShareRoutes(router)
}

// shareBrowse 渲染只读浏览页；撤销、过期或不存在一律 404（不区分三者，避免泄漏 token 历史）。
func (s *Server) shareBrowse(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	link, deck, ok := s.resolveShareLink(c)
	if !ok {
		return
	}
	if link.PasswordHash != nil {
		s.renderSharePassword(c, loc, deck, link, http.StatusOK, "")
		return
	}
	s.renderShareContent(c, loc, deck, link, http.StatusOK)
}

// shareUnlock 校验分享链接口令：正确则渲染内容，错误则带着本地化提示重渲染口令表单。
func (s *Server) shareUnlock(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	link, deck, ok := s.resolveShareLink(c)
	if !ok {
		return
	}
	if link.PasswordHash == nil {
		// 无口令链接的解锁请求等价于直接浏览，幂等处理。
		s.renderShareContent(c, loc, deck, link, http.StatusOK)
		return
	}
	matched, err := auth.Verify(*link.PasswordHash, c.PostForm("password"))
	if err != nil {
		s.logger.Error("verify share link password failed", "deck_id", deck.ID, "error", err)
		s.renderSharePassword(c, loc, deck, link, http.StatusUnauthorized, loc.T("share.error_password"))
		return
	}
	if !matched {
		s.logger.Info("share link password rejected", "deck_id", deck.ID)
		s.renderSharePassword(c, loc, deck, link, http.StatusUnauthorized, loc.T("share.error_password"))
		return
	}
	s.renderShareContent(c, loc, deck, link, http.StatusOK)
}

// resolveShareLink 解析路径里的明文 token 并取回卡组；无效时写 404 并返回 false。
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

// renderSharePassword 渲染口令表单（不渲染任何卡片内容，避免绕过口令看到内容）。
func (s *Server) renderSharePassword(c *gin.Context, loc *i18n.Localizer, deck *store.Deck, link *store.ShareLink, status int, errMsg string) {
	data := views.ShareBrowseData{
		Layout:              s.pageLayout(c, loc, "share.browse.title"),
		Heading:             loc.T("share.browse.heading"),
		DeckName:            deck.Name,
		Intro:               loc.T("share.password.intro"),
		PasswordRequired:    true,
		PasswordLabel:       loc.T("share.password.label"),
		PasswordPlaceholder: loc.T("share.password.placeholder"),
		UnlockSubmit:        loc.T("share.password.submit"),
		PasswordError:       errMsg,
		UnlockAction:        "/s/" + c.Param("token") + "/unlock",
	}
	renderHTMLStatus(c, status, views.ShareBrowsePage(data))
}

// renderShareContent 渲染只读内容：卡片正反面 + “登录后开始复习”的提示。
// 页面不含任何进度信息、作者或其它用户数据（DESIGN.md §11 的隐私要求）。
//
// 三个成功入口都汇聚到这里（shareBrowse 无口令、shareUnlock 的无口令幂等分支与口令正确分支），
// 所以「打开成功即登记会话授权」只在这一处实现（L3，DESIGN.md §5）。
func (s *Server) renderShareContent(c *gin.Context, loc *i18n.Localizer, deck *store.Deck, link *store.ShareLink, status int) {
	ctx := c.Request.Context()
	// 访问已经成功（无口令，或口令校验通过）→ 登记「本会话打开过这个卡组」，
	// 浏览器随后对 /media/<sha256> 的请求才放行；匿名访客没有服务端会话，直接跳过。
	s.recordShareGrant(c, deck, link)
	notes, _, err := s.notes.List(ctx, store.NoteListOptions{
		DeckID: deck.ID, Page: 1, PerPage: store.DefaultNotePageSize, Status: store.NoteStatusActive,
	})
	if err != nil {
		s.logger.Error("list notes for share browse failed", "deck_id", deck.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	rendered := make([]views.ShareBrowseNote, 0, len(notes))
	for i := range notes {
		note, err := s.shareNoteView(notes[i])
		if err != nil {
			// 单张坏卡不阻断浏览：跳过并记英文日志，其余内容照常展示。
			s.logger.Error("render shared note failed", "note_id", notes[i].ID, "error", err)
			continue
		}
		rendered = append(rendered, note)
	}
	data := views.ShareBrowseData{
		Layout:       s.pageLayout(c, loc, "share.browse.title"),
		Heading:      loc.T("share.browse.heading"),
		DeckName:     deck.Name,
		Intro:        loc.T("share.browse.intro"),
		ReviewPrompt: loc.T("share.browse.review_prompt"),
		LoginLabel:   loc.T("share.browse.login"),
		LoginHref:    "/login",
		FrontLabel:   loc.T("share.browse.front"),
		BackLabel:    loc.T("share.browse.back"),
		Notes:        rendered,
		EmptyText:    loc.T("share.browse.empty"),
	}
	renderHTMLStatus(c, status, views.ShareBrowsePage(data))
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

// shareNoteView 把一个 note 的所有 card 渲染成正反面 HTML（与编辑页预览同一套渲染管线）。
func (s *Server) shareNoteView(note store.Note) (views.ShareBrowseNote, error) {
	fields, err := store.ParseFields(note.FieldsJSON)
	if err != nil {
		return views.ShareBrowseNote{}, err
	}
	t, ok := cardtype.Lookup(note.Kind)
	if !ok {
		return views.ShareBrowseNote{}, cardtype.UnknownKindError(note.Kind)
	}
	cards, err := cardtype.Cards(cardtype.Note{Kind: note.Kind, Fields: fields})
	if err != nil {
		return views.ShareBrowseNote{}, err
	}
	var frontParts, backParts []string
	for _, card := range cards {
		front, err := t.Render(card, cardtype.SideFront)
		if err != nil {
			return views.ShareBrowseNote{}, err
		}
		back, err := t.Render(card, cardtype.SideBack)
		if err != nil {
			return views.ShareBrowseNote{}, err
		}
		frontHTML, err := renderSide(front)
		if err != nil {
			return views.ShareBrowseNote{}, err
		}
		backHTML, err := renderSide(back)
		if err != nil {
			return views.ShareBrowseNote{}, err
		}
		frontParts = append(frontParts, frontHTML)
		backParts = append(backParts, backHTML)
	}
	return views.ShareBrowseNote{
		FrontHTML: strings.Join(frontParts, "\n"),
		BackHTML:  strings.Join(backParts, "\n"),
	}, nil
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
