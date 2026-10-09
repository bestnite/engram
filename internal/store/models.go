// Package store 持有 GORM 模型与数据访问。
// 同一套模型同时服务业务、GORM 与 JSON/CSV 序列化，不做 DTO 映射层（AGENTS.md §2.4）。
//
// 双库兼容：不使用任何 PG 专有类型（jsonb / serial / array），
// JSON 一律存 TEXT；字符串型列级默认值（如 'user' / 'basic'）不写进 DDL —— 未加引号的
// 默认值在 PostgreSQL 里可能被当成同名函数或关键字，两库行为不一致；这些默认值由 store
// 层写入时在 Go 侧显式给出。数值与布尔默认值两库语义一致，可以写在 DDL 里。
package store

import (
	"time"

	"gorm.io/gorm"
)

// User 是本地账号；内置账号为默认身份来源，外部身份见 Identity。
type User struct {
	ID              uint64     `gorm:"primaryKey" json:"-"`
	PublicID        string     `gorm:"column:public_id;uniqueIndex" json:"id"`
	Username        string     `gorm:"not null;uniqueIndex" json:"username"`
	Email           string     `gorm:"not null;uniqueIndex" json:"email"`
	EmailVerifiedAt *time.Time `json:"email_verified_at,omitempty"`
	// argon2id；纯 OIDC 账号可以为 NULL。
	PasswordHash *string `gorm:"column:password_hash" json:"-"`
	DisplayName  string  `gorm:"not null" json:"display_name"`
	Role         string  `gorm:"not null" json:"role"`   // admin | user
	Status       string  `gorm:"not null" json:"status"` // active | disabled
	Locale       string  `gorm:"not null" json:"locale"`
	Timezone     string  `gorm:"not null" json:"timezone"`
	// NULL 使用默认 04:00；指针保留显式午夜 0，且不让数据库默认覆盖它。
	DayCutoffHour *int `gorm:"column:day_cutoff_hour" json:"day_cutoff_hour"`
	// ReminderHour 是用户选择的本地发送小时（0–23，复习提醒与周报共用）；NULL 表示未设置，
	// 回落到全局默认 reminder.DefaultSendHour。必须是可空指针：0 是合法值（午夜），用普通
	// int 加默认值会分不清「未设置」与「午夜」，且 GORM 会把零值当成未提供（AGENTS.md §2.3 第 9 条
	// 记的是同一个坑：带 default 标签的列，零值字段会被数据库默认覆盖）。
	ReminderHour *int `gorm:"column:reminder_hour" json:"reminder_hour,omitempty"`
	// ShareAcceptFrom 是「谁可以把卡组分享给我」：anyone | whitelist | nobody。
	// NULL 按 anyone 处理（与上线前的行为一致：那时所有人都能被分享）。
	ShareAcceptFrom *string    `gorm:"column:share_accept_from" json:"share_accept_from,omitempty"`
	CreatedAt       time.Time  `gorm:"not null" json:"created_at"`
	LastSeenAt      *time.Time `json:"last_seen_at,omitempty"`
}

// TableName 固定表名，避免复数化规则在不同 GORM 版本下漂移。
func (User) TableName() string { return "users" }

// Identity 是绑定的外部身份（provider + subject 唯一）。
type Identity struct {
	ID       uint64    `gorm:"primaryKey" json:"-"`
	PublicID string    `gorm:"column:public_id;uniqueIndex" json:"id"`
	UserID   uint64    `gorm:"not null;index" json:"user_id"`
	Provider string    `gorm:"not null;uniqueIndex:idx_identities_provider_subject" json:"provider"`
	Subject  string    `gorm:"not null;uniqueIndex:idx_identities_provider_subject" json:"subject"`
	Email    *string   `json:"email,omitempty"`
	LinkedAt time.Time `gorm:"not null" json:"linked_at"`
}

func (Identity) TableName() string { return "identities" }

// Invite 是一次性邀请（注册策略为 invite 时使用）。
type Invite struct {
	ID        uint64     `gorm:"primaryKey" json:"-"`
	PublicID  string     `gorm:"column:public_id;uniqueIndex" json:"id"`
	Token     string     `gorm:"not null;uniqueIndex" json:"token"`
	Email     *string    `json:"email,omitempty"`
	Role      string     `gorm:"not null" json:"role"`
	CreatedBy *uint64    `json:"created_by,omitempty"`
	CreatedAt time.Time  `gorm:"not null" json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	UsedAt    *time.Time `json:"used_at,omitempty"`
	UsedBy    *uint64    `json:"used_by,omitempty"`
}

func (Invite) TableName() string { return "invites" }

// Setting 是管理员可改的系统设置；value 是 JSON 编码文本。
type Setting struct {
	Key       string    `gorm:"primaryKey" json:"key"`
	Value     string    `gorm:"not null" json:"value"`
	UpdatedBy *uint64   `json:"updated_by,omitempty"`
	UpdatedAt time.Time `gorm:"not null" json:"updated_at"`
}

func (Setting) TableName() string { return "settings" }

// Preset 是一组调度参数，挂在 deck 上，多个 deck 可共用。
type Preset struct {
	ID                  uint64  `gorm:"primaryKey" json:"-"`
	PublicID            string  `gorm:"column:public_id;uniqueIndex" json:"id"`
	OwnerUserID         uint64  `gorm:"not null;index" json:"owner_user_id"`
	Name                string  `gorm:"not null" json:"name"`
	DesiredRetention    float64 `gorm:"not null;default:0.9" json:"desired_retention"`
	LearningSteps       string  `gorm:"not null" json:"learning_steps"`
	RelearningSteps     string  `gorm:"not null" json:"relearning_steps"`
	MaximumIntervalDays int     `gorm:"not null;default:36500" json:"maximum_interval_days"`
	// EnableFuzz 带数据库默认值 true，按 AGENTS.md §2.3 第 9 条用 *bool：
	// nil 表示未指定（落库走数据库默认 true），非 nil 时显式写入——包括显式的 false。
	// 读取有效值请用 FuzzEnabled()，不要直接解引用。
	EnableFuzz         *bool      `gorm:"not null;default:true" json:"enable_fuzz"`
	WeightsJSON        *string    `gorm:"column:weights_json" json:"weights_json,omitempty"`
	WeightsOptimizedAt *time.Time `json:"weights_optimized_at,omitempty"`
	WeightsReviewCount *int       `json:"weights_review_count,omitempty"`
	// GradeMappingJSON 是机器判分的「分数→评分档位」映射（JSON）。
	// NULL 表示用内置默认映射（全对 Good / 部分对 Hard / 全错 Again）。
	// 可空 TEXT，不设数据库默认值：默认行为由 cardtype.DefaultGradeMapping 在 Go 侧给出。
	GradeMappingJSON *string   `gorm:"column:grade_mapping_json" json:"grade_mapping_json,omitempty"`
	CreatedAt        time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt        time.Time `gorm:"not null" json:"updated_at"`
}

func (Preset) TableName() string { return "presets" }

// Deck 是扁平卡组（不做卡组树）。
// NewPerDay / ReviewsPerDay 是卡组级每日上限，0 表示不限。
// 两列都是带数据库默认值的整型，零值由 GORM 省略、由数据库默认值补齐；
// 显式把上限设为 0 请走 DeckStore.SetCaps（map 更新会写入 0）。
type Deck struct {
	ID            uint64     `gorm:"primaryKey" json:"-"`
	PublicID      string     `gorm:"column:public_id;uniqueIndex" json:"id"`
	OwnerUserID   uint64     `gorm:"not null;index" json:"owner_user_id"`
	Name          string     `gorm:"not null" json:"name"`
	Description   string     `gorm:"not null" json:"description"`
	NewPerDay     int        `gorm:"not null;default:20" json:"new_per_day"`
	ReviewsPerDay int        `gorm:"not null;default:200" json:"reviews_per_day"`
	PresetID      uint64     `gorm:"not null;index" json:"preset_id"`
	ArchivedAt    *time.Time `json:"archived_at,omitempty"`
	CreatedAt     time.Time  `gorm:"not null" json:"created_at"`
}

func (Deck) TableName() string { return "decks" }

// Note 描述"一个事实"，只含内容不含任何用户进度。
type Note struct {
	ID         uint64 `gorm:"primaryKey" json:"-"`
	PublicID   string `gorm:"column:public_id;uniqueIndex" json:"id"`
	DeckID     uint64 `gorm:"not null;uniqueIndex:idx_notes_deck_external_ref" json:"deck_id"`
	Kind       string `gorm:"not null" json:"kind"`
	FieldsJSON string `gorm:"column:fields_json;not null" json:"fields_json"`
	TagsJSON   string `gorm:"column:tags_json;not null" json:"tags_json"`
	// 由调用方定义的幂等键；NULL 不参与唯一性（两库都允许多个 NULL）。
	ExternalRef   *string        `gorm:"uniqueIndex:idx_notes_deck_external_ref" json:"external_ref,omitempty"`
	Source        *string        `json:"source,omitempty"` // manual | api | import
	ReferenceRefs *string        `gorm:"column:reference_refs" json:"reference_refs,omitempty"`
	CreatedBy     *uint64        `json:"created_by,omitempty"`
	CreatedAt     time.Time      `gorm:"not null" json:"created_at"`
	UpdatedAt     time.Time      `gorm:"not null" json:"updated_at"`
	DeletedAt     gorm.DeletedAt `gorm:"index" json:"deleted_at,omitempty"`
}

func (Note) TableName() string { return "notes" }

// Card 是 note 在某种呈现形式下的实例；调度作用于 card。
type Card struct {
	ID          uint64         `gorm:"primaryKey" json:"-"`
	PublicID    string         `gorm:"column:public_id;uniqueIndex" json:"id"`
	NoteID      uint64         `gorm:"not null;index;uniqueIndex:idx_cards_note_template" json:"note_id"`
	Template    string         `gorm:"not null;uniqueIndex:idx_cards_note_template" json:"template"`
	Ordinal     int            `gorm:"not null;default:0" json:"ordinal"`
	SuspendedAt *time.Time     `json:"suspended_at,omitempty"`
	CreatedAt   time.Time      `gorm:"not null" json:"created_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"deleted_at,omitempty"`
}

func (Card) TableName() string { return "cards" }

// CardState 是某个用户对某张 card 的 FSRS 状态；主键 (card_id, user_id) 是共享卡组的基石。
type CardState struct {
	CardID        uint64     `gorm:"primaryKey" json:"card_id"`
	UserID        uint64     `gorm:"primaryKey;index:idx_states_user_due,priority:1" json:"user_id"`
	State         string     `gorm:"not null" json:"state"` // new | learning | review | relearning
	DueAt         *time.Time `gorm:"index:idx_states_user_due,priority:2" json:"due_at,omitempty"`
	StepIndex     int        `gorm:"not null;default:0" json:"step_index"`
	Stability     *float64   `json:"stability,omitempty"`
	Difficulty    *float64   `json:"difficulty,omitempty"`
	Reps          int        `gorm:"not null;default:0" json:"reps"`
	Lapses        int        `gorm:"not null;default:0" json:"lapses"`
	ScheduledDays int        `gorm:"not null;default:0" json:"scheduled_days"`
	ElapsedDays   int        `gorm:"not null;default:0" json:"elapsed_days"`
	LastReviewAt  *time.Time `json:"last_review_at,omitempty"`
	Version       int        `gorm:"not null;default:0" json:"version"` // 乐观锁
}

func (CardState) TableName() string { return "card_states" }

// Review 是 append-only 复习日志，参数优化的唯一燃料；每个字段从第一天就写全。
type Review struct {
	ID              uint64    `gorm:"primaryKey" json:"-"`
	PublicID        string    `gorm:"column:public_id;uniqueIndex" json:"id"`
	CardID          uint64    `gorm:"not null;index:idx_reviews_card,priority:1" json:"card_id"`
	UserID          uint64    `gorm:"not null;index:idx_reviews_user_day,priority:1" json:"user_id"`
	Rating          int       `gorm:"not null" json:"rating"`       // 1=Again 2=Hard 3=Good 4=Easy
	GradeSource     string    `gorm:"not null" json:"grade_source"` // self | typed | llm
	GradeDetailJSON *string   `gorm:"column:grade_detail_json" json:"grade_detail_json,omitempty"`
	ReviewedAt      time.Time `gorm:"not null;index:idx_reviews_card,priority:2" json:"reviewed_at"`
	ReviewDay       string    `gorm:"not null;index:idx_reviews_user_day,priority:2" json:"review_day"`
	ElapsedMS       *int      `gorm:"column:elapsed_ms" json:"elapsed_ms,omitempty"`
	DurationDays    *float64  `json:"duration_days,omitempty"`
	StateBefore     int       `gorm:"not null" json:"state_before"` // 0=New 1=Learning 2=Review 3=Relearning
	// StepIndexBefore 是本次评分前 card_states.step_index 的快照（剩余学习步骤数）。
	// 评分前的 FSRS 学习步骤游标没有别的来源，而 fsrs.Rollback 会把 step_index 归零；
	// 存下它 Undo 才能精确还原步骤进度。
	// 可空：旧行没有这个快照，Undo 遇到 NULL 时退回归零行为。整数不用带默认值的布尔（AGENTS.md §2.3 第 9 条）。
	StepIndexBefore *int     `gorm:"column:step_index_before" json:"step_index_before,omitempty"`
	IntervalDays    *float64 `json:"interval_days,omitempty"`
	Stability       *float64 `json:"stability,omitempty"`
	Difficulty      *float64 `json:"difficulty,omitempty"`
}

func (Review) TableName() string { return "reviews" }

// DeckGrant 是卡组授权；角色集合故意只有三个。
type DeckGrant struct {
	DeckID    uint64    `gorm:"primaryKey" json:"deck_id"`
	UserID    uint64    `gorm:"primaryKey" json:"user_id"`
	Role      string    `gorm:"not null" json:"role"` // owner | editor | reader
	CreatedBy *uint64   `json:"created_by,omitempty"`
	CreatedAt time.Time `gorm:"not null" json:"created_at"`
}

func (DeckGrant) TableName() string { return "deck_grants" }

// ShareLink 是给免注册读者的只读分享链接。
type ShareLink struct {
	Token        string     `gorm:"primaryKey" json:"token"`
	DeckID       uint64     `gorm:"not null;index" json:"deck_id"`
	Role         string     `gorm:"not null" json:"role"`
	PasswordHash *string    `gorm:"column:password_hash" json:"-"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	CreatedBy    uint64     `gorm:"not null" json:"created_by"`
	CreatedAt    time.Time  `gorm:"not null" json:"created_at"`
	RevokedAt    *time.Time `json:"revoked_at,omitempty"`
}

func (ShareLink) TableName() string { return "share_links" }

// Media 只存元数据，字节在本地文件系统，路径由 sha256 决定。
// 主键 = 内容 sha256：对外标识就是哈希，不再有自增 id。
type Media struct {
	Sha256    string    `gorm:"primaryKey" json:"sha256"`
	RelPath   string    `gorm:"not null" json:"rel_path"`
	Mime      string    `gorm:"not null" json:"mime"`
	Bytes     int64     `gorm:"not null" json:"bytes"`
	Width     *int      `json:"width,omitempty"`
	Height    *int      `json:"height,omitempty"`
	CreatedBy *uint64   `json:"created_by,omitempty"`
	CreatedAt time.Time `gorm:"not null" json:"created_at"`
}

func (Media) TableName() string { return "media" }

// MediaNote 是 media ↔ note 的显式引用映射（-10-06 定）。
//
// 它是**派生索引**：内容的唯一来源始终是 notes.fields_json，映射由 note 写入路径在每次
// 写入后**重建**（删旧建新，见 NoteStore）。读取鉴权据此判定「这份媒体是否被我的可读卡组里的
// 未软删 note 引用」，不再扫描 fields_json。
//
// 为什么不带「谁建立的引用」列：越权引用改由写侧单点校验（写入前对写入前状态求值）拦住，
// 前提是所有 note 写入路径收敛到同一个方法。
type MediaNote struct {
	MediaSha string `gorm:"column:media_sha;not null;uniqueIndex:idx_media_notes_media_note,priority:1" json:"media_sha"`
	NoteID   uint64 `gorm:"column:note_id;not null;uniqueIndex:idx_media_notes_media_note,priority:2" json:"note_id"`
	// CreatedAt 是这条映射建立的时间；重建时整行替换，故它等于最近一次写入的时间。
	CreatedAt time.Time `gorm:"not null" json:"created_at"`
}

func (MediaNote) TableName() string { return "media_notes" }

// MediaUploader 记录「谁提供过这份字节」（-10-06 定）。
//
// 为什么不是 media.created_by 一列：字节按 sha256 去重，同一份字节全库只有一行，一列只能记
// 第一个上传者；于是「B 上传的字节恰好与 C 已有的相同（去重命中）」时 B 拿不到归属——A 撤销
// 共享后 B 读不到自己提供的文件。多对多记全部提供者，才让「能提供字节 ⇒ 可读」在去重下也成立。
//
// 上传与导入（凡本次提供字节的路径）按 (media_sha, user_id) **幂等**写入（clause.OnConflict
// 忽略重复）。**写入后不可撤销**：删了「提供过」就不再成立。
type MediaUploader struct {
	MediaSha  string    `gorm:"primaryKey;column:media_sha" json:"media_sha"`
	UserID    uint64    `gorm:"primaryKey;column:user_id" json:"user_id"`
	CreatedAt time.Time `gorm:"not null" json:"created_at"`
}

func (MediaUploader) TableName() string { return "media_uploaders" }

// ShareSessionDeck 记录「某个服务端会话通过分享链接打开过某个卡组」（L3）。
// 业务存取与过期规则在同文件的 ShareSessionStore，登记在 AllModels() 里。
type ShareSessionDeck struct {
	SessionID string    `gorm:"primaryKey;column:session_id" json:"session_id"`
	DeckID    uint64    `gorm:"primaryKey;column:deck_id" json:"deck_id"`
	ExpiresAt time.Time `gorm:"not null;index" json:"expires_at"`
}

func (ShareSessionDeck) TableName() string { return "share_session_decks" }

// APIKey 是用户级凭据；明文只在创建时返回一次，库里只存 sha256。
type APIKey struct {
	ID       uint64 `gorm:"primaryKey;column:id" json:"-"`
	PublicID string `gorm:"column:public_id;uniqueIndex" json:"id"`
	// UserID 是内部外键：卡组/key 列表按调用者过滤，客户端不需要它，故不出现在 JSON 里。
	UserID     uint64     `gorm:"not null;index" json:"-"`
	Name       string     `gorm:"not null" json:"name"`
	Prefix     string     `gorm:"not null" json:"prefix"`
	KeyHash    string     `gorm:"column:key_hash;not null;uniqueIndex" json:"-"`
	Scopes     string     `gorm:"not null" json:"scopes"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	CreatedAt  time.Time  `gorm:"not null" json:"created_at"`
}

func (APIKey) TableName() string { return "api_keys" }

// Job 是后台长任务（当前只有参数优化）；web 触发、子进程执行、页面轮询。
type Job struct {
	ID         uint64     `gorm:"primaryKey" json:"-"`
	PublicID   string     `gorm:"column:public_id;uniqueIndex" json:"id"`
	Kind       string     `gorm:"not null" json:"kind"`
	TargetID   *uint64    `json:"target_id,omitempty"`
	Status     string     `gorm:"not null" json:"status"` // queued | running | succeeded | failed
	Stage      *string    `json:"stage,omitempty"`
	LogTail    *string    `gorm:"column:log_tail" json:"log_tail,omitempty"`
	ResultJSON *string    `gorm:"column:result_json" json:"result_json,omitempty"`
	Error      *string    `json:"error,omitempty"`
	CreatedAt  time.Time  `gorm:"not null" json:"created_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

func (Job) TableName() string { return "jobs" }

// AuditLog 记录谁在什么时候改了什么。
type AuditLog struct {
	ID         uint64    `gorm:"primaryKey" json:"-"`
	PublicID   string    `gorm:"column:public_id;uniqueIndex" json:"id"`
	UserID     *uint64   `json:"user_id,omitempty"`
	APIKeyID   *uint64   `gorm:"column:api_key_id" json:"api_key_id,omitempty"`
	Action     string    `gorm:"not null" json:"action"`
	TargetType *string   `gorm:"column:target_type" json:"target_type,omitempty"`
	TargetID   *uint64   `gorm:"column:target_id" json:"target_id,omitempty"`
	DetailJSON *string   `gorm:"column:detail_json" json:"detail_json,omitempty"`
	CreatedAt  time.Time `gorm:"not null" json:"created_at"`
}

func (AuditLog) TableName() string { return "audit_log" }

// SchemaVersion 是单行表（id 恒为 1），记录已应用的破坏性迁移版本。
// 表名用单数，与措辞一致。
type SchemaVersion struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Version   int       `gorm:"not null" json:"version"`
	UpdatedAt time.Time `gorm:"not null" json:"updated_at"`
}

func (SchemaVersion) TableName() string { return "schema_version" }

// 用户与账号状态取值；字符串默认值由 store 层在 Go 侧显式给出（见包注释）。
const (
	RoleAdmin      = "admin"
	RoleUser       = "user"
	StatusActive   = "active"
	StatusDisabled = "disabled"
)

// Session 是服务端会话记录：cookie 只持有不可读的会话 ID 与签名，作废以这里的行状态为准。
// 把它放进库而非纯无状态 cookie，是为了让登出、改密码、禁用三种情况都能真正"服务端作废"
// csrf_token 绑定会话，供 CSRF 中间件校验。
type Session struct {
	ID         string     `gorm:"primaryKey;column:id" json:"id"`
	UserID     uint64     `gorm:"not null;index" json:"user_id"`
	CSRFToken  string     `gorm:"column:csrf_token;not null" json:"-"`
	CreatedAt  time.Time  `gorm:"not null" json:"created_at"`
	ExpiresAt  time.Time  `gorm:"not null;index" json:"expires_at"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

func (Session) TableName() string { return "sessions" }

// AllModels 是**全部** GORM 模型的唯一清单，AutoMigrate 从它取，测试的表清单断言也对照它。
// 模型的定义文件可以分开（例如 TOTP 两表放在 totp.go），但**新增模型必须登记在这里**：
// 漏登记的后果不是报错，而是 AutoMigrate 静默缺表——直到有查询撞上不存在的表才暴露。
func AllModels() []any {
	return []any{
		&User{}, &Identity{}, &Invite{}, &Setting{}, &Preset{}, &Deck{}, &Note{}, &Card{},
		&CardState{}, &Review{}, &DeckGrant{}, &ShareLink{}, &Media{}, &APIKey{}, &Job{},
		&MediaNote{}, &MediaUploader{},
		// L3 分享会话授权：模型定义在 models.go，存取在 share_session.go。
		&ShareSessionDeck{},
		&AuditLog{}, &SchemaVersion{}, &Session{},
		// TOTP：模型定义在 totp.go，但必须出现在这里（见上面的注释）。
		&UserTOTP{}, &TOTPRecoveryCode{},
		// SMTP：outbox 队列表，模型定义在 outbox.go。
		&OutboxMessage{},
		&EmailPref{},
		&ReminderLog{},
		// 每周学习摘要的发送台账：模型定义在 digest.go。
		&DigestLog{},
		&ActionToken{}, &LoginFingerprint{}, // A-class security mail: one-time tokens and login fingerprints.
		// 管理员自定义邮件模板：缺失即回退内置正文。
		&MailTemplate{},
		// L3 分享同意制：待接受的邀请与接收白名单。
		&DeckShareInvite{}, &ShareAllow{},
	}
}

// MailTemplate 是管理员自定义的邮件模板。
//
// 唯一键是 (type, locale)：同一类型每种语言一份。**表里没有行是正常状态**——那表示该类型
// 该语言用内置正文（发信方组装的那份），所以「删除自定义」就是删这一行，内置默认不可能被
// 弄丢。正文存 Markdown 原文而不是渲染结果：渲染、清洗、变量替换都发生在发送时，于是改
// 模板只影响之后入队的邮件。
type MailTemplate struct {
	Type      string    `gorm:"primaryKey;size:64" json:"type"`
	Locale    string    `gorm:"primaryKey;size:16" json:"locale"`
	Subject   string    `gorm:"not null" json:"subject"`
	BodyMD    string    `gorm:"not null" json:"body_md"`
	UpdatedBy *uint64   `json:"updated_by,omitempty"`
	UpdatedAt time.Time `gorm:"not null" json:"updated_at"`
}

func (MailTemplate) TableName() string { return "mail_templates" }

// DeckShareInvite 是「已发出、还没被接受」的卡组共享邀请。
//
// 同意制的落地方式：**授权（deck_grants）只在被邀请者接受时才写**，而可见集合谓词读的正是
// deck_grants——所以待接受的邀请不进任何可见集合，队列、统计、回溯、媒体鉴权全都不用改。
// 这是选「新表」而不是「给 deck_grants 加状态列」的唯一理由。
//
// 同一 (卡组, 用户) 只有一行：重复邀请是刷新那一行的角色与有效期，不叠加。
type DeckShareInvite struct {
	DeckID    uint64    `gorm:"primaryKey" json:"deck_id"`
	UserID    uint64    `gorm:"primaryKey" json:"user_id"`
	Role      string    `gorm:"not null" json:"role"`
	InvitedBy uint64    `gorm:"not null" json:"invited_by"`
	CreatedAt time.Time `gorm:"not null" json:"created_at"`
	// ExpiresAt 之后邀请作废（到期行由 internal/retention 每小时回收）。
	ExpiresAt time.Time `gorm:"not null;index" json:"expires_at"`
}

func (DeckShareInvite) TableName() string { return "deck_share_invites" }

// ShareAcceptPolicy 是一个用户对「别人可以把卡组分享给我吗」的答复。
//
// 三档而不是「白名单布尔」：拒绝所有人是最常见的诉求（不想被任何人拉进来），把它表达成
// 「白名单为空」会与「白名单还没配」混为一谈。
type ShareAcceptPolicy string

const (
	// ShareAcceptAnyone 是默认：任何人都可以邀请我。
	ShareAcceptAnyone ShareAcceptPolicy = "anyone"
	// ShareAcceptWhitelist 只接受 share_allow 里列出的人。
	ShareAcceptWhitelist ShareAcceptPolicy = "whitelist"
	// ShareAcceptNobody 拒绝所有分享邀请。
	ShareAcceptNobody ShareAcceptPolicy = "nobody"
)

// ShareAllow 是一行「from 可以把卡组分享给 to」的许可（由 to 自己配置）。
type ShareAllow struct {
	FromUserID uint64    `gorm:"primaryKey" json:"from_user_id"`
	ToUserID   uint64    `gorm:"primaryKey" json:"to_user_id"`
	CreatedAt  time.Time `gorm:"not null" json:"created_at"`
}

func (ShareAllow) TableName() string { return "share_allow" }
