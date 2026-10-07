package web

import (
	"context"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是 B 类协作邮件里与卡组共享有关的两类（DESIGN.md §4.7）：
//
//	deck_shared              —— 有人把卡组分享给你
//	deck_permission_changed   —— 你在某个卡组里的权限被改动或被撤销
//
// 三条纪律：**不发给自己**（操作者不需要收到「我分享给了你」）、尊重收件人的偏好、任何失败
// 都只记日志——分享本身绝不能因为发信失败而失败。

// deckMailType 判断这次变更该发哪一类：从「没有权限」变成「有权限」是分享，其余都是变更
// （含撤销，此时 newRole 为空串）。
func deckMailType(previousRole, newRole string) mail.Type {
	if previousRole == "" && newRole != "" {
		return mail.TypeDeckShared
	}
	return mail.TypeDeckPermissionChanged
}

// notifyDeckGrantChange 在授权被新建、改动或撤销后通知被授权者。
//
// 它刻意不返回错误：调用点是共享写路径，通知只是副作用。
func (s *Server) notifyDeckGrantChange(c *gin.Context, deck *store.Deck, actor, target *store.User, previousRole, newRole string) {
	if s == nil || deck == nil || actor == nil || target == nil {
		return
	}
	if target.ID == actor.ID {
		// 自己改自己的权限（含属主操作自己）没有通知价值。
		return
	}
	if s.mail == nil || !s.mail.Configured() {
		// 未配置 SMTP 时说一声就够：共享操作本身已经成功，用户不需要在这里看到「邮件未发送」。
		s.logger.Info("deck mail: smtp is not configured; notification skipped", "deck_id", deck.ID)
		return
	}
	ctx := c.Request.Context()
	choices, err := store.NewEmailPrefStore(s.db).Choices(ctx, target.ID)
	if err != nil {
		s.logger.Error("deck mail: read recipient preferences failed", "user_id", target.ID, "error", err)
		return
	}
	typ := deckMailType(previousRole, newRole)
	if !mail.ResolveEnabled(choices, typ) {
		return
	}
	loc := s.userLocalizer(target)
	link := s.securityAbsoluteURL(c, "/decks/"+strconv.FormatUint(deck.ID, 10))
	unsubURL := s.optionalUnsubscribeLink(c, target.ID, typ)
	msg := s.deckGrantMessage(ctx, loc, target.Email, typ, actor.Username, deck.Name, newRole, link, unsubURL)
	if err := s.mail.Enqueue(ctx, msg); err != nil {
		s.logger.Error("deck mail: enqueue failed", "mail_type", string(typ), "user_id", target.ID, "error", err)
	}
}

// deckGrantMessage 组装分享/权限变更邮件。默认正文是纯文本；管理员配了模板才产出 HTML 段。
func (s *Server) deckGrantMessage(ctx context.Context, loc *i18n.Localizer, to string, typ mail.Type,
	inviter, deck, role, link, unsubURL string) mail.Message {
	site := securitySiteName(loc)
	roleLabel := roleLabelFor(loc, role)
	// 撤销时 role 为空：邮件要说「被取消了访问」，而不是「权限改成了空」。
	revoked := strings.TrimSpace(role) == ""

	vars := mail.Vars{
		"site": site, "inviter": inviter, "actor": inviter,
		"deck": deck, "role": roleLabel, "url": link,
	}
	if unsubURL != "" {
		vars["unsubscribe_url"] = unsubURL
	}

	fallbackSubject, fallbackText := "", ""
	if typ == mail.TypeDeckShared {
		fallbackSubject = loc.Tf("mail.deck_shared.subject", map[string]any{"inviter": inviter, "site": site})
		fallbackText = strings.Join([]string{
			loc.T("mail.deck_shared.greeting"),
			"",
			loc.Tf("mail.deck_shared.body", map[string]any{"inviter": inviter, "deck": deck, "role": roleLabel}),
			"",
			loc.T("mail.deck_shared.link_label"),
			link,
		}, "\n")
	} else {
		fallbackSubject = loc.Tf("mail.deck_permission.subject", map[string]any{"site": site})
		line := loc.Tf("mail.deck_permission.granted", map[string]any{"actor": inviter, "deck": deck, "role": roleLabel})
		if revoked {
			line = loc.Tf("mail.deck_permission.revoked", map[string]any{"actor": inviter, "deck": deck})
		}
		fallbackText = strings.Join([]string{
			loc.T("mail.deck_permission.greeting"),
			"",
			line,
			"",
			loc.T("mail.deck_permission.link_label"),
			link,
		}, "\n")
	}

	footerNote, unsubLabel := "", ""
	if unsubURL != "" {
		footerNote = loc.T("mail.footer.note")
		unsubLabel = loc.T("mail.footer.unsubscribe")
	}
	tpl := mail.Resolve(s.mailTemplateLookup(), typ, loc.Locale(), s.siteDefaultLocale(ctx))
	out := mail.RenderOrFallback(mail.RenderInput{
		Type:             typ,
		Site:             site,
		Vars:             vars,
		Subject:          tpl.Subject,
		BodyMD:           tpl.Body,
		FallbackSubject:  fallbackSubject,
		FallbackText:     fallbackText,
		UnsubscribeURL:   unsubURL,
		FooterNote:       footerNote,
		UnsubscribeLabel: unsubLabel,
	})
	msg := mail.Message{To: to, Type: string(typ), Subject: out.Subject, TextBody: out.Text, HTMLBody: out.HTML}
	msg.Headers = mail.UnsubscribeHeaders(typ, unsubURL)
	return msg
}

// roleLabelFor 把 reader/editor 这类标识翻成收件人语言；未知值原样返回（不外泄空串）。
func roleLabelFor(loc *i18n.Localizer, role string) string {
	switch strings.TrimSpace(role) {
	case store.RoleReader:
		return loc.T("mail.role.reader")
	case store.RoleEditor:
		return loc.T("mail.role.editor")
	default:
		return strings.TrimSpace(role)
	}
}

// notifyDeckRevoke 在撤销授权后通知被撤销者；被撤销的用户已经查不到了就静默跳过。
func (s *Server) notifyDeckRevoke(c *gin.Context, deck *store.Deck, actor *store.User, targetID uint64, previousRole string) {
	if s == nil || s.users == nil || deck == nil || actor == nil || targetID == 0 {
		return
	}
	target, err := s.users.ByID(c.Request.Context(), targetID)
	if err != nil || target == nil {
		s.logger.Error("deck mail: load revoked user failed", "user_id", targetID, "error", err)
		return
	}
	s.notifyDeckGrantChange(c, deck, actor, target, previousRole, "")
}
