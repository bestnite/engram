package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// 审计动作常量集中定义。值一律英文点分、保持稳定：
// action 是审计检索的键，调用点写裸字符串会让历史数据无法按动作聚合。
// 新增写操作时在这里加常量，不要另起一套命名。
const (
	ActionUserCreate         = "user.create"
	ActionUserLoginSucceeded = "user.login_succeeded"
	ActionUserLoginFailed    = "user.login_failed"
	ActionUserLogout         = "user.logout"
	// 卡片列表/编辑页的写操作。
	ActionNoteUpdate = "note.update"
	ActionNoteDelete = "note.delete"
	ActionNoteTagAdd = "note.tag_add"
	// 批量标签动作：移除与整体设置标签。与 ActionNoteTagAdd 并列，
	// 三条动作名各自稳定，便于按动作聚合历史审计。
	ActionNoteTagRemove = "note.tag_remove"
	ActionNoteTagSet    = "note.tag_set"
	// 新建卡组。
	ActionDeckCreate = "deck.create"
	// 上传媒体。
	ActionMediaUpload = "media.upload"

	// ActionSettingUpdate 是管理面板修改系统设置时写入的审计动作。
	ActionSettingUpdate = "setting.update"
	// ActionMailTemplateUpdate 是管理面板保存或删除邮件模板时写入的
	// 审计动作。模板决定所有用户收到的邮件长什么样，与系统设置同级。
	ActionMailTemplateUpdate = "mail.template_update"

	// ActionInviteCreate 是管理面板创建邀请码时写入的审计动作。
	ActionInviteCreate = "invite.create"
	// ActionInviteRevoke 是管理面板撤销邀请码时写入的审计动作。
	ActionInviteRevoke = "invite.revoke"
	// 撤销评分。
	ActionReviewUndo = "review.undo"
	// 卡组共享：越权请求被拒（谁在什么时候试图做什么被挡下）。
	ActionPermissionDenied = "permission.denied"
	// 首次 OIDC 登录时自动绑定外部身份（写一条 identities）。
	ActionIdentityLink = "identity.link"
	// 管理面板解绑外部身份。
	ActionIdentityUnlink = "identity.unlink"
	// 授权变更（共享页面）：授予 / 改角色 / 撤销。
	ActionDeckRoleChange = "deck.role_change"
	ActionDeckRevoke     = "deck.revoke"
	// L3 分享同意制：邀请、接受、拒绝各一条，以及用户改自己的接收策略。
	ActionDeckShareInvite   = "deck.share_invite"
	ActionDeckShareAccept   = "deck.share_accept"
	ActionDeckShareReject   = "deck.share_reject"
	ActionSharePolicyUpdate = "share.policy_update"
	// 克隆卡组（内容复制到调用者账号）。不是严格意义的共享变更，但同样留痕。
	ActionDeckClone = "deck.clone"
	// 分享链接：创建 / 单个撤销 / 一次撤销全部。
	ActionShareLinkCreate    = "share_link.create"
	ActionShareLinkRevoke    = "share_link.revoke"
	ActionShareLinkRevokeAll = "share_link.revoke_all"
	// 持链接的登录用户按「加入我的卡组」把该卡组加进自己的列表（share_link.join）。
	// 它是一次自助授权：token 是属主主动发出的凭据，按按钮是接收方本人的同意，
	// 因此不需要邀请/接受两步。入伙后这条授权与链接无关，撤销链接不回收它。
	ActionShareLinkJoin = "share_link.join"
	// 卡组每日额度变更（new_per_day / reviews_per_day）。改的是「这个卡组今天还放多少
	// 张出来」，与其他卡组变更一样要留痕。
	ActionDeckCaps = "deck.caps_change"
	// 预设参数优化：触发优化作业与一键回退默认权重。
	ActionPresetOptimize       = "preset.optimize"
	ActionPresetOptimizeRevert = "preset.optimize.revert"
	// ActionPresetCreate / ActionPresetUpdate 是预设页新建与编辑预设时写入的审计动作。
	ActionPresetCreate = "preset.create"
	ActionPresetUpdate = "preset.update"
	// 卡组与预设删除。
	ActionDeckDelete   = "deck.delete"
	ActionPresetDelete = "preset.delete"
	// ActionDeckPreset 是卡组切换调度预设时写入的审计动作：它决定这个卡组按哪套
	// FSRS 参数排程，与每日额度同级，必须留痕。
	ActionDeckPreset = "deck.preset_change"
	// ActionJobCancel 是管理面板取消后台作业时写入的审计动作。
	ActionJobCancel = "job.cancel"

	// TOTP 二次验证：绑定开始 / 启用 / 关闭 / 重新生成恢复码 / 恢复码被消费 / 第二步失败。
	// 恢复码明文永不入审计（detail 只记数量与来源 IP）。
	ActionTOTPBegin              = "totp.begin"
	ActionTOTPEnable             = "totp.enable"
	ActionTOTPDisable            = "totp.disable"
	ActionTOTPRecoveryRegenerate = "totp.recovery_regenerate"
	ActionTOTPRecoveryUsed       = "totp.recovery_used"
	ActionTOTPVerifyFailed       = "totp.verify_failed"

	// 邮件偏好：用户保存可选邮件类型的开关时留痕（detail 只记变更的类型与开关）。
	ActionUserEmailPrefsUpdate = "user.email_prefs_update"

	// 一键退订：用户凭免登录令牌关掉某个可选邮件类型时留痕。
	ActionUserEmailUnsubscribe = "user.email_unsubscribe"

	// OIDC「测试连接」与 SMTP「测试连接」。
	// 两者都是高影响动作：两次「测试连接」会发起管理员指定的出站连接。
	// 成功与失败都要有行——失败同样留下「谁在何时试图连到哪里」的取证线索。
	// 审计行只记动作与目标元信息（测试连接记 host / issuer），
	// 绝不记凭据、口令、token 或邮件正文。
	ActionAdminOIDCTest = "admin.oidc_test"
	ActionAdminSMTPTest = "admin.smtp_test"
)

// AuditEntry 是一次审计写入的入参。
// UserID / APIKeyID / TargetID 可空（nil 落库为 NULL）：未登录的失败登录没有 user_id，
// 系统自动动作没有 api_key_id —— 用 0 冒充会污染“按用户检索”的结果。
type AuditEntry struct {
	UserID   *uint64
	APIKeyID *uint64
	Action   string
	// TargetType 为空表示该动作没有目标对象；此时 TargetID 被忽略。
	TargetType string
	TargetID   *uint64
	// Detail 会被序列化成 JSON 存进 detail_json；nil 时该列留空。
	Detail any
}

// AuditStore 封装 audit_log 表；Write 是全部写操作留痕的唯一入口。
type AuditStore struct {
	db *gorm.DB
}

// NewAuditStore 构造审计存储。
func NewAuditStore(db *gorm.DB) *AuditStore { return &AuditStore{db: db} }

// Write 写一行审计。
// action 为空直接拒绝：没有动作名的行无法被检索，属于调用方 bug，不能静默落库。
func (s *AuditStore) Write(ctx context.Context, e AuditEntry) error {
	if e.Action == "" {
		return errors.New("audit action must not be empty")
	}
	row := AuditLog{
		UserID:    e.UserID,
		APIKeyID:  e.APIKeyID,
		Action:    e.Action,
		CreatedAt: time.Now().UTC(),
	}
	if e.TargetType != "" {
		targetType := e.TargetType
		row.TargetType = &targetType
		row.TargetID = e.TargetID
	}
	if e.Detail != nil {
		raw, err := json.Marshal(e.Detail)
		if err != nil {
			return fmt.Errorf("marshal audit detail: %w", err)
		}
		detail := string(raw)
		row.DetailJSON = &detail
	}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		return fmt.Errorf("write audit log: %w", err)
	}
	return nil
}

// List 按写入顺序倒序返回最近 limit 条审计；供管理面板（M6）与测试查看。
func (s *AuditStore) List(ctx context.Context, limit int) ([]AuditLog, error) {
	if limit <= 0 {
		limit = 50
	}
	var rows []AuditLog
	if err := s.db.WithContext(ctx).Order("id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list audit log: %w", err)
	}
	return rows, nil
}

// CountByAction 返回某动作的审计行数；测试用它断言“每次变更恰好一行”。
func (s *AuditStore) CountByAction(ctx context.Context, action string) (int64, error) {
	var n int64
	if err := s.db.WithContext(ctx).Model(&AuditLog{}).
		Where("action = ?", action).Count(&n).Error; err != nil {
		return 0, fmt.Errorf("count audit log: %w", err)
	}
	return n, nil
}

// AuditFilter 是审计检索的过滤条件；每个字段的零值都表示「不按该维度过滤」。
//
// 时间口径：created_at 一律以 UTC 存储；From 是下界（含），To 是上界（不含）。
// 调用方负责把用户时区的自然日边界换算成这两个 UTC 瞬时值——store 层不猜时区。
type AuditFilter struct {
	// UserID 非零时只返回该用户的审计行；系统动作（user_id 为 NULL）不会被命中。
	UserID uint64
	// Action 非空时精确匹配动作名（取值来自本包的 Action* 常量）。
	Action string
	// TargetType 非空时精确匹配目标类型。
	TargetType string
	// TargetID 非零时精确匹配目标 ID。
	TargetID uint64
	// From 是时间下界（含）；零值表示不限。
	From time.Time
	// To 是时间上界（不含）；零值表示不限。
	To time.Time
	// Limit 是每页行数；<=0 时回退默认值，超过上限时截到上限。
	Limit int
	// Offset 是跳过的行数；负数按 0 处理。
	Offset int
}

const (
	// auditSearchDefaultLimit 是审计检索的默认每页行数。
	auditSearchDefaultLimit = 50
	// auditSearchMaxLimit 是审计检索的硬上限：审计表增长很快，任何调用方都不能一次拉全表。
	auditSearchMaxLimit = 200
)

// auditQuery 按过滤条件构造查询；Count 与 Find 各调一次，避免复用同一 *gorm.DB 会话。
func (s *AuditStore) auditQuery(ctx context.Context, f AuditFilter) *gorm.DB {
	q := s.db.WithContext(ctx).Model(&AuditLog{})
	if f.UserID != 0 {
		q = q.Where("user_id = ?", f.UserID)
	}
	if f.Action != "" {
		q = q.Where("action = ?", f.Action)
	}
	if f.TargetType != "" {
		q = q.Where("target_type = ?", f.TargetType)
	}
	if f.TargetID != 0 {
		q = q.Where("target_id = ?", f.TargetID)
	}
	if !f.From.IsZero() {
		q = q.Where("created_at >= ?", f.From.UTC())
	}
	if !f.To.IsZero() {
		q = q.Where("created_at < ?", f.To.UTC())
	}
	return q
}

// Search 按过滤条件分页返回审计行与命中总数，按 id 倒序（最新在前）。
// 结果行数受 auditSearchMaxLimit 限制，分页用 LIMIT/OFFSET。
func (s *AuditStore) Search(ctx context.Context, f AuditFilter) ([]AuditLog, int64, error) {
	var total int64
	if err := s.auditQuery(ctx, f).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count audit log: %w", err)
	}
	limit := f.Limit
	if limit <= 0 {
		limit = auditSearchDefaultLimit
	}
	if limit > auditSearchMaxLimit {
		limit = auditSearchMaxLimit
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}
	var rows []AuditLog
	if err := s.auditQuery(ctx, f).Order("id DESC").Limit(limit).Offset(offset).Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("search audit log: %w", err)
	}
	return rows, total, nil
}

// DistinctActions 返回库中出现过的动作名（升序），供检索页的动作下拉使用。
func (s *AuditStore) DistinctActions(ctx context.Context) ([]string, error) {
	var actions []string
	if err := s.db.WithContext(ctx).Model(&AuditLog{}).Distinct().Order("action ASC").
		Pluck("action", &actions).Error; err != nil {
		return nil, fmt.Errorf("list audit actions: %w", err)
	}
	return actions, nil
}

// Ptr 返回 v 的地址，用于填充可空字段（user_id / api_key_id / target_id）。
func Ptr[T any](v T) *T { return &v }
