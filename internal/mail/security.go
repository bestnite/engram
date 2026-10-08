package mail

import (
	"context"
	"log/slog"

	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是（A 类事务安全邮件）的入站投递助手。
//
// 三条约束都收敛在这里，调用方（web 触发点）不必各自实现：
//   - 绝不同步发信：只调用 Outbox.Enqueue 写队列表；
//   - 发信失败不让触发操作失败——Send 不返回 error；SMTP 未配置或入队失败时返回 false
//     并记英文日志，绝不向上抛；
//   - A 类不携带退订头：Send 从不写 Headers，因此不会出现 List-Unsubscribe。
//
// Configured 供触发点判断「SMTP 未配置时禁用并说明原因」。

// SecurityNotifier 把 A 类安全/事务邮件写进 outbox。
type SecurityNotifier struct {
	outbox *Outbox
	prefs  *store.EmailPrefStore
	logger *slog.Logger
}

// NewSecurityNotifier 构造 A 类邮件投递助手；prefs 可为 nil（A 类不受偏好影响）。
func NewSecurityNotifier(outbox *Outbox, prefs *store.EmailPrefStore, logger *slog.Logger) *SecurityNotifier {
	if logger == nil {
		logger = slog.Default()
	}
	return &SecurityNotifier{outbox: outbox, prefs: prefs, logger: logger}
}

// Configured 报告 SMTP 是否已配置；触发点据此禁用相关页面/接口并说明原因。
func (n *SecurityNotifier) Configured() bool {
	return n != nil && n.outbox != nil && n.outbox.Configured()
}

// Send 把一封 A 类邮件写进 outbox，返回是否成功入队。
//
// 它刻意不返回 error：登录成功、改密码成功等操作绝不能因为 Enqueue 报错而失败。
// SMTP 未配置、类型未登记、入队失败都只记英文日志并返回 false。
func (n *SecurityNotifier) Send(ctx context.Context, userID uint64, to string, t Type, subject, textBody, htmlBody string) bool {
	if !n.Configured() {
		return false
	}
	if _, ok := Lookup(t); !ok {
		n.logger.Error("mail: unknown security mail type", "user_id", userID, "type", string(t))
		return false
	}
	// A 类恒为 true；这里仍走目录判定，保证「页面能关、后端照发」这类分歧不可能出现。
	if n.prefs != nil {
		choices, err := n.prefs.Choices(ctx, userID)
		if err != nil {
			n.logger.Error("mail: load email preferences failed", "user_id", userID, "type", string(t), "error", err)
		} else if !ResolveEnabled(choices, t) {
			return false
		}
	}
	// Headers 留空：A 类邮件不带退订头。
	if err := n.outbox.Enqueue(ctx, Message{
		To:       to,
		Type:     string(t),
		Subject:  subject,
		TextBody: textBody,
		HTMLBody: htmlBody,
	}); err != nil {
		n.logger.Error("mail: enqueue security notification failed",
			"user_id", userID, "type", string(t), "error", err)
		return false
	}
	return true
}
