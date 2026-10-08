package mail

// 本文件是邮件类型目录：**一处定义**，偏好页与每个发信方共用它。
//
// 分类依据是**能不能被用户关掉**，不是邮件内容——允许关掉安全邮件等于给人留后门。
// 因此「默认开关」「可否关闭」都由 Class 推导，而不是每个类型各自标注：
// 类型只要归入某个 Class，它的默认值与可否关闭就唯一确定，偏好页与发信方不可能分歧。
//
// 本包只放目录与判定逻辑，不实现任何发信行为。发信方在 wave 2 接入时调用
// Lookup / CanDisable / DefaultEnabled / ResolveEnabled，不复制这里的任何规则。
//
// 命名边界：本文件不得定义 Message、Outbox、ErrNotConfigured、Configured —— 那些是
// 传输层的符号，同包并行开发，重复声明会撞符号。

// Class 是邮件类型的大类；分类依据是能否被用户关闭。
type Class string

const (
	// ClassSecurity 是 A 安全/事务类：默认开、不可关闭、不带退订头。
	ClassSecurity Class = "security"
	// ClassCollab 是 B 协作/授权类：默认开、可关闭。
	ClassCollab Class = "collab"
	// ClassStudy 是 C 学习/运营类：默认关、可关闭。
	ClassStudy Class = "study"
	// ClassAdmin 是 D 管理员通知类：默认开、可关闭，发给管理员。
	ClassAdmin Class = "admin"
)

// Type 是邮件类型的稳定英文标识；同一类型在偏好页、发信方与存储里都用它。
type Type string

const (
	// A 安全/事务：默认开、不可关闭、不带退订头。
	TypePasswordReset     Type = "password_reset"     // 密码重置
	TypeEmailVerification Type = "email_verification" // 邮箱验证与改邮箱确认
	TypeNewDeviceLogin    Type = "new_device_login"   // 新设备/新 IP 登录提醒
	TypeCredentialChanged Type = "credential_changed" // 密码/TOTP/恢复码变更通知
	TypeAccountStatus     Type = "account_status"     // 账号被禁用或删除通知

	// B 协作/授权：默认开、可关闭。
	TypeDeckShared            Type = "deck_shared"             // 卡组分享给你
	TypeDeckPermissionChanged Type = "deck_permission_changed" // 卡组权限被变更或撤销
	TypeInvite                Type = "invite"                  // 邀请码发给被邀请人

	// C 学习/运营：默认关、可关闭。
	TypeReviewReminder Type = "review_reminder" // 复习到期提醒
	TypeStudyDigest    Type = "study_digest"    // 学习摘要
	TypeOptimizeDone   Type = "optimize_done"   // 参数优化完成

	// 曾有两种 C 类类型在 2026-10-07 被删除：连续打卡即将中断（streak_at_risk）与导入导出完成
	// （import_export_done）。删掉的理由不是「暂时没做」，而是**它们没有触发点**：前者没有
	// 「即将中断」的判定规则，后者没有异步导入/导出作业（同步完成时用户正看着结果页，发信
	// 没有意义，这个类型当初就是为将来的异步大文件流程预留的）。目录里的每个类型都必须有
	// 发信方，否则偏好页会长出一个永远不会发的开关。要恢复它们，先把触发点做出来。

	// D 管理员通知：默认开、可关闭，发给管理员。
	TypeJobFailed      Type = "job_failed"       // 作业失败
	TypeMediaDiskAlert Type = "media_disk_alert" // 媒体配额/磁盘告警
)

// Definition 描述一种邮件类型：它属于哪一类，以及偏好页展示它时用的语言包键。
// 默认开关与可否关闭刻意不放进字段——它们由 Class 唯一推导（DefaultEnabled / CanDisable），
// 若每个类型各写一份，两处就会漂移。
type Definition struct {
	// Type 是稳定英文标识，同时是存储里偏好映射的键。
	Type Type
	// Class 是所属大类，决定默认开关与可否关闭。
	Class Class
	// LabelKey 是语言包中该类型显示名的键（mail.prefs.type.<type>）。
	LabelKey string
}

// classOrder 是偏好页展示大类的固定顺序，也是 Catalog() 的排序依据。
var classOrder = []Class{ClassSecurity, ClassCollab, ClassStudy, ClassAdmin}

// catalog 是唯一的目录定义，顺序固定为 A → B → C → D。
// 新增类型只在这里加一行；偏好页与发信方都从这里取，不存在第二份清单。
var catalog = []Definition{
	// A 安全/事务
	{Type: TypePasswordReset, Class: ClassSecurity, LabelKey: "mail.prefs.type.password_reset"},
	{Type: TypeEmailVerification, Class: ClassSecurity, LabelKey: "mail.prefs.type.email_verification"},
	{Type: TypeNewDeviceLogin, Class: ClassSecurity, LabelKey: "mail.prefs.type.new_device_login"},
	{Type: TypeCredentialChanged, Class: ClassSecurity, LabelKey: "mail.prefs.type.credential_changed"},
	{Type: TypeAccountStatus, Class: ClassSecurity, LabelKey: "mail.prefs.type.account_status"},

	// B 协作/授权
	{Type: TypeDeckShared, Class: ClassCollab, LabelKey: "mail.prefs.type.deck_shared"},
	{Type: TypeDeckPermissionChanged, Class: ClassCollab, LabelKey: "mail.prefs.type.deck_permission_changed"},
	{Type: TypeInvite, Class: ClassCollab, LabelKey: "mail.prefs.type.invite"},

	// C 学习/运营
	{Type: TypeReviewReminder, Class: ClassStudy, LabelKey: "mail.prefs.type.review_reminder"},
	{Type: TypeStudyDigest, Class: ClassStudy, LabelKey: "mail.prefs.type.study_digest"},
	{Type: TypeOptimizeDone, Class: ClassStudy, LabelKey: "mail.prefs.type.optimize_done"},

	// D 管理员通知
	{Type: TypeJobFailed, Class: ClassAdmin, LabelKey: "mail.prefs.type.job_failed"},
	{Type: TypeMediaDiskAlert, Class: ClassAdmin, LabelKey: "mail.prefs.type.media_disk_alert"},
}

// byType 是 catalog 的查找索引，在包初始化时一次性建立。
var byType = func() map[Type]Definition {
	m := make(map[Type]Definition, len(catalog))
	for _, d := range catalog {
		m[d.Type] = d
	}
	return m
}()

// Catalog 返回目录的副本，顺序固定为 A → B → C → D（偏好页据此渲染分组）。
// 返回副本而不是内部切片，调用方无法意外改写唯一目录。
func Catalog() []Definition {
	out := make([]Definition, len(catalog))
	copy(out, catalog)
	return out
}

// Lookup 按类型取定义；未登记的类型返回 false。
// 发信方必须先 Lookup 再决定是否发送：未知类型一律不发送，避免拼错类型名后静默投递。
func Lookup(t Type) (Definition, bool) {
	d, ok := byType[t]
	return d, ok
}

// ClassOrder 返回大类的展示顺序副本（A → B → C → D）。
func ClassOrder() []Class {
	out := make([]Class, len(classOrder))
	copy(out, classOrder)
	return out
}

// CanDisable 报告某一大类能否被用户关闭。
// 只有 A 安全/事务类不可关闭：允许关掉安全邮件等于给人留后门。
func CanDisable(c Class) bool {
	return c != ClassSecurity
}

// DefaultEnabled 报告某一大类的默认开关：A/B/D 默认开，C 默认关。
func DefaultEnabled(c Class) bool {
	return c != ClassStudy
}

// ResolveEnabled 是发信方与偏好页共用的唯一判定：在用户显式选择之上套用目录规则。
//
//   - 不可关闭的类（A）恒为 true，用户选择被忽略——即使存储里存在 A=false 也不可能生效；
//   - 可关闭的类优先用用户显式选择（choices[t]），没有选择时回落到该类默认值；
//   - 未登记的类型返回 false：拼错的类型名不应投递。
//
// choices 的键是 Type 的字符串形式（与存储层一致），只含可关闭类型的用户选择。
func ResolveEnabled(choices map[string]bool, t Type) bool {
	def, ok := Lookup(t)
	if !ok {
		return false
	}
	if !CanDisable(def.Class) {
		return true
	}
	if v, ok := choices[string(t)]; ok {
		return v
	}
	return DefaultEnabled(def.Class)
}
