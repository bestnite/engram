package mail

import (
	"context"
	"log/slog"

	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是（D 类管理员通知邮件）的入站投递助手，与 security.go 的 SecurityNotifier
// 同构：把三条硬约束收敛在一处，调用方（各触发点）不必各自实现。
//
//   - 绝不同步发信：只调用 Outbox.Enqueue 写队列表；
//   - 发信失败不让触发操作失败——Send 不返回 error；SMTP 未配置、入队失败时返回 false
//     并记英文日志，绝不向上抛（注册、作业失败、配额拦截都绝不能因邮件而失败）；
//   - D 类可关闭：按**收件管理员自己**的偏好判定，关掉就不寄；目录判定走 ResolveEnabled，
//     因此「偏好页能关、后端照发」不可能出现。
//
// 退订头是独立任务，这里不写 Headers。
//
// 命名边界：本文件不得定义 Message、Outbox、ErrNotConfigured、Configured —— 那些是传输层
// 的符号，重复声明会撞符号。

// AdminNotifier 把 D 类管理员通知写进 outbox。
type AdminNotifier struct {
	outbox *Outbox
	prefs  *store.EmailPrefStore
	logger *slog.Logger
}

// NewAdminNotifier 构造 D 类投递助手；prefs 可为 nil（此时只看目录默认，不做用户级门禁）。
func NewAdminNotifier(outbox *Outbox, prefs *store.EmailPrefStore, logger *slog.Logger) *AdminNotifier {
	if logger == nil {
		logger = slog.Default()
	}
	return &AdminNotifier{outbox: outbox, prefs: prefs, logger: logger}
}

// Configured 报告 SMTP 是否已配置；触发点据此决定是否渲染 mail.not_configured 说明。
func (n *AdminNotifier) Configured() bool {
	return n != nil && n.outbox != nil && n.outbox.Configured()
}

// Send 把一封 D 类邮件写进 outbox，返回是否成功入队。
//
// 它刻意不返回 error：注册、作业失败、媒体配额拦截等触发操作绝不能因为入队报错而失败。
// SMTP 未配置、类型未登记、收件管理员关掉了该类、入队失败都只记英文日志并返回 false。
func (n *AdminNotifier) Send(ctx context.Context, adminID uint64, to string, t Type, subject, textBody, htmlBody string) bool {
	if !n.Configured() {
		return false
	}
	if _, ok := Lookup(t); !ok {
		n.logger.Error("mail: unknown admin mail type", "admin_id", adminID, "type", string(t))
		return false
	}
	// D 类可关：尊重收件管理员对 D 类各类型的显式选择；读取失败时回落目录默认（开）继续，
	// 但绝不静默——记日志。
	if n.prefs != nil {
		choices, err := n.prefs.Choices(ctx, adminID)
		if err != nil {
			n.logger.Error("mail: load admin email preferences failed", "admin_id", adminID, "type", string(t), "error", err)
		} else if !ResolveEnabled(choices, t) {
			return false
		}
	}
	// Headers 留空：一键退订头属于。
	if err := n.outbox.Enqueue(ctx, Message{
		To:       to,
		Type:     string(t),
		Subject:  subject,
		TextBody: textBody,
		HTMLBody: htmlBody,
	}); err != nil {
		n.logger.Error("mail: enqueue admin notification failed",
			"admin_id", adminID, "type", string(t), "error", err)
		return false
	}
	return true
}
