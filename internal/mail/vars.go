package mail

// 本文件声明每种邮件类型的**模板变量**：变量名、是否必填、以及管理页
// 展示用途时用的语言包键。发信方按同一张表传值，管理页按同一张表校验与展示——所以变量
// 不可能出现「发信方有、页面不认」的分歧。
//
// 变量名是接口的一部分（管理员在模板里写的就是它），一律英文小写下划线；改名等于让已有
// 模板失效，要一起改这里的 Spec 与发信方。

// VarSpec 描述一个模板变量。
type VarSpec struct {
	// Name 是模板里书写的名字：`{{url}}` 写的就是它。
	Name string
	// Required 为真时，自定义模板缺了它就被拒绝保存、发送时也会退回内置正文。
	//
	// **判据是「缺了这封信就没用或没法收场」，据此刻意只收两类**：① 收件人必须点的链接
	// （重置、验证、邀请、复习、统计、卡组页、退订）——没有链接的密码邮件是废纸，没有退订
	// 入口的营销类邮件是违规；② deck_shared 的 deck 与 inviter（缺了它这封信不知在说什么）。
	// **展示性细节一律不必填**：到期时间、待复习张数、通过率这类少了只是信息少一点，
	// 标成必填只会让管理员在保存时撞见莫名其妙的失败。
	Required bool
	// NoteKey 是管理页展示该变量用途的语言包键。
	NoteKey string
}

// mailVars 是唯一的变量表。类型不在表里 ⇒ 该类型不支持自定义模板（发信方还没有），
// 管理页也就不会为它渲染编辑器。
var mailVars = map[Type][]VarSpec{
	TypePasswordReset: {
		{Name: "site", Required: false, NoteKey: "admin.mail.var.site"},
		{Name: "username", Required: false, NoteKey: "admin.mail.var.username"},
		{Name: "url", Required: true, NoteKey: "admin.mail.var.reset_url"},
		{Name: "expires", Required: false, NoteKey: "admin.mail.var.expires"},
	},
	TypeEmailVerification: {
		{Name: "site", Required: false, NoteKey: "admin.mail.var.site"},
		{Name: "url", Required: true, NoteKey: "admin.mail.var.verify_url"},
		{Name: "expires", Required: false, NoteKey: "admin.mail.var.expires"},
	},
	TypeNewDeviceLogin: {
		{Name: "site", Required: false, NoteKey: "admin.mail.var.site"},
		{Name: "username", Required: false, NoteKey: "admin.mail.var.username"},
		{Name: "time", Required: false, NoteKey: "admin.mail.var.time"},
		{Name: "ip", Required: false, NoteKey: "admin.mail.var.ip"},
		{Name: "url", Required: false, NoteKey: "admin.mail.var.settings_url"},
	},
	TypeCredentialChanged: {
		{Name: "site", Required: false, NoteKey: "admin.mail.var.site"},
		{Name: "what", Required: false, NoteKey: "admin.mail.var.what"},
		{Name: "time", Required: false, NoteKey: "admin.mail.var.time"},
	},
	TypeAccountStatus: {
		{Name: "site", Required: false, NoteKey: "admin.mail.var.site"},
		{Name: "status", Required: false, NoteKey: "admin.mail.var.status"},
		{Name: "reason", Required: false, NoteKey: "admin.mail.var.reason"},
	},
	TypeInvite: {
		{Name: "site", Required: false, NoteKey: "admin.mail.var.site"},
		{Name: "url", Required: true, NoteKey: "admin.mail.var.invite_url"},
		{Name: "expires", Required: false, NoteKey: "admin.mail.var.expires"},
	},
	TypeReviewReminder: {
		{Name: "site", Required: false, NoteKey: "admin.mail.var.site"},
		{Name: "count", Required: false, NoteKey: "admin.mail.var.due_count"},
		{Name: "url", Required: true, NoteKey: "admin.mail.var.review_url"},
		{Name: "unsubscribe_url", Required: true, NoteKey: "admin.mail.var.unsubscribe_url"},
	},
	TypeStudyDigest: {
		{Name: "site", Required: false, NoteKey: "admin.mail.var.site"},
		{Name: "count", Required: false, NoteKey: "admin.mail.var.reviewed_count"},
		{Name: "rate", Required: false, NoteKey: "admin.mail.var.pass_rate"},
		{Name: "days", Required: false, NoteKey: "admin.mail.var.streak_days"},
		// 摘要有六个统计行，内置正文用到的变量必须都在这里——「用了却没登记」会被
		// Validate 判成打错字，预填的默认也就存不回去。
		{Name: "new_cards", Required: false, NoteKey: "admin.mail.var.new_cards"},
		{Name: "due", Required: false, NoteKey: "admin.mail.var.due_count"},
		{Name: "url", Required: true, NoteKey: "admin.mail.var.stats_url"},
		{Name: "unsubscribe_url", Required: true, NoteKey: "admin.mail.var.unsubscribe_url"},
	},
	TypeDeckShared: {
		{Name: "site", Required: false, NoteKey: "admin.mail.var.site"},
		{Name: "inviter", Required: true, NoteKey: "admin.mail.var.inviter"},
		{Name: "deck", Required: true, NoteKey: "admin.mail.var.deck"},
		{Name: "role", Required: false, NoteKey: "admin.mail.var.role"},
		{Name: "url", Required: true, NoteKey: "admin.mail.var.deck_url"},
		{Name: "unsubscribe_url", Required: true, NoteKey: "admin.mail.var.unsubscribe_url"},
	},
	TypeDeckPermissionChanged: {
		{Name: "site", Required: false, NoteKey: "admin.mail.var.site"},
		{Name: "actor", Required: true, NoteKey: "admin.mail.var.actor"},
		{Name: "deck", Required: true, NoteKey: "admin.mail.var.deck"},
		{Name: "role", Required: false, NoteKey: "admin.mail.var.role"},
		{Name: "url", Required: false, NoteKey: "admin.mail.var.deck_url"},
		{Name: "unsubscribe_url", Required: true, NoteKey: "admin.mail.var.unsubscribe_url"},
	},
	TypeOptimizeDone: {
		{Name: "site", Required: false, NoteKey: "admin.mail.var.site"},
		{Name: "preset", Required: false, NoteKey: "admin.mail.var.preset"},
		{Name: "count", Required: false, NoteKey: "admin.mail.var.reviewed_count"},
		{Name: "url", Required: true, NoteKey: "admin.mail.var.presets_url"},
		{Name: "unsubscribe_url", Required: true, NoteKey: "admin.mail.var.unsubscribe_url"},
	},
	TypeJobFailed: {
		{Name: "site", Required: false, NoteKey: "admin.mail.var.site"},
		{Name: "job_id", Required: false, NoteKey: "admin.mail.var.job_id"},
		{Name: "kind", Required: false, NoteKey: "admin.mail.var.job_kind"},
		{Name: "reason", Required: false, NoteKey: "admin.mail.var.fail_reason"},
		{Name: "url", Required: false, NoteKey: "admin.mail.var.jobs_url"},
	},
	TypeMediaDiskAlert: {
		{Name: "site", Required: false, NoteKey: "admin.mail.var.site"},
		{Name: "used", Required: false, NoteKey: "admin.mail.var.media_used"},
		{Name: "limit", Required: false, NoteKey: "admin.mail.var.media_limit"},
	},
}

// VarSpecs 返回某类型的变量表；第二个返回值为 false 表示该类型不支持自定义模板。
// 名字不叫 Vars：那是模板变量**值**的类型（template.go），两者同名会互相遮蔽。
func VarSpecs(t Type) ([]VarSpec, bool) {
	specs, ok := mailVars[t]
	return specs, ok
}

// SupportsTemplate 表示该类型是否已接入自定义模板（发信方已按变量表传值）。
func SupportsTemplate(t Type) bool {
	_, ok := mailVars[t]
	return ok
}

// RequiredVars 返回必填变量名，供保存校验与测试断言使用。
func RequiredVars(t Type) []string {
	specs, ok := mailVars[t]
	if !ok {
		return nil
	}
	out := make([]string, 0, len(specs))
	for _, s := range specs {
		if s.Required {
			out = append(out, s.Name)
		}
	}
	return out
}
