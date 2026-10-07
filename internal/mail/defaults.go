package mail

import "strings"

// 本文件是「内置正文」的唯一定义（DESIGN.md §4.7）。
//
// 内置正文有两个读者，**必须出自同一处**：
//
//  1. 发信方——没有自定义模板（或模板损坏、变量缺失）时，发出去的就是它；
//  2. 管理页——邮件模板的编辑框预填它，管理员在这份原件上改。
//
// 两份实现必然漂移，而漂移的表现是最难发现的那种：管理页显示的「默认」和实际发出的正文
// 不是一回事，管理员以为自己在改默认，其实在改另一个东西。所以这里同一段代码有**两种用法**：
// 传真实变量得到可发出的纯文本，传占位符变量（PlaceholderVars）得到模板文本。
//
// 为什么不用「把默认写进数据库」的做法：默认文案会随之冻在库里，此后改语言包不再影响它们，
// 而「恢复默认」还得再从代码抄一份回去。内置默认永远在代码里意味着模板被删、被写坏都不会
// 让邮件发不出去。

// TfFunc 是「按语言包 id 取文案并代入变量」的最小形状（`*i18n.Localizer` 的 `Tf` 即可）。
type TfFunc func(msgID string, data map[string]any) string

// 变体：同一类型标识下有两种内置写法的场合。今天只有邮箱验证一处——注册验证与改邮箱确认
// 都属于「确认一个邮箱地址」，共用同一套变量与同一个偏好开关，所以是同一个类型。
const (
	// VariantDefault 是主写法。
	VariantDefault = ""
	// VariantEmailChange 是「确认新邮箱」的写法。
	VariantEmailChange = "change"
)

// defaultLine 是内置正文里的一行：要么是语言包文案（ID），要么是变量值本身（Var）。
//
// 链接、退订地址这类内容不是文案，是变量——把它们也写成语言包条目等于让每种语言各抄一遍
// 同一个 URL，且抄错不会被发现。
type defaultLine struct {
	ID  string
	Var string
	// Join 为真时输出「<语言包文案>: <变量值>」，即退订入口那一行——它与模板路径
	// 自动追加的那一行同形（internal/mail/template.go 的 Render）。
	Join bool
	// Args 把「语言包里的变量名」映射到「变量表里的键」，用于同一段文案里的
	// `{{.count}}` 在不同行含义不同的场合（摘要的「已复习」「新卡」「到期」都用
	// `{{.count}}`，但读的是三个不同的数）。为空时整份变量表都传进去。
	Args map[string]string
}

func msg(id string) defaultLine    { return defaultLine{ID: id} }
func varLine(v string) defaultLine { return defaultLine{Var: v} }
func blank() defaultLine           { return defaultLine{} }

// defaultSpec 是一种邮件类型的内置写法。
type defaultSpec struct {
	// subjectID 是主题的语言包 id。
	subjectID string
	// lines 是正文的行序列；空 ID 且空 Var 表示空行。
	lines func(v Vars) []defaultLine
}

// defaultSpecs 是全部类型的内置正文。顺序与行数与各发信方原来的组装逐字一致——
// 抽取的目的只是让管理页看到同一份文本，不是顺手改文案。
var defaultSpecs = map[Type]defaultSpec{
	TypePasswordReset: {
		subjectID: "mail.reset.subject",
		lines: func(Vars) []defaultLine {
			return []defaultLine{
				msg("mail.reset.greeting"), blank(),
				msg("mail.reset.body"), blank(),
				msg("mail.reset.link_label"), varLine("url"), blank(),
				msg("mail.reset.expires"),
			}
		},
	},
	TypeEmailVerification: {
		subjectID: "mail.verify.subject",
		lines: func(Vars) []defaultLine {
			return []defaultLine{
				msg("mail.verify.greeting"), blank(),
				msg("mail.verify.body"), blank(),
				msg("mail.verify.link_label"), varLine("url"), blank(),
				msg("mail.verify.expires"),
			}
		},
	},
	TypeNewDeviceLogin: {
		subjectID: "mail.notice.new_device.subject",
		lines: func(Vars) []defaultLine {
			return []defaultLine{msg("mail.notice.greeting"), blank(), msg("mail.notice.new_device.body")}
		},
	},
	TypeCredentialChanged: {
		subjectID: "mail.notice.credential.subject",
		lines: func(Vars) []defaultLine {
			return []defaultLine{msg("mail.notice.greeting"), blank(), msg("mail.notice.credential.body")}
		},
	},
	TypeAccountStatus: {
		subjectID: "mail.notice.account.subject",
		lines: func(Vars) []defaultLine {
			return []defaultLine{msg("mail.notice.greeting"), blank(), msg("mail.notice.account.body")}
		},
	},
	TypeDeckShared: {
		subjectID: "mail.deck_shared.subject",
		lines: func(Vars) []defaultLine {
			return []defaultLine{
				msg("mail.deck_shared.greeting"), blank(),
				msg("mail.deck_shared.body"), blank(),
				msg("mail.deck_shared.link_label"), varLine("url"),
			}
		},
	},
	TypeDeckPermissionChanged: {
		subjectID: "mail.deck_permission.subject",
		lines: func(v Vars) []defaultLine {
			// 撤销时 role 为空：正文要说「被取消了访问」，而不是「权限改成了空」。
			// 这条判断与发信方一致——它由变量推导，不另设参数，管理页拿到的默认
			// 因占位符非空而落在「授权」那一支。
			line := msg("mail.deck_permission.granted")
			if strings.TrimSpace(v["role"]) == "" {
				line = msg("mail.deck_permission.revoked")
			}
			return []defaultLine{
				msg("mail.deck_permission.greeting"), blank(),
				line, blank(),
				msg("mail.deck_permission.link_label"), varLine("url"),
			}
		},
	},
	TypeInvite: {
		subjectID: "mail.invite.subject",
		lines: func(v Vars) []defaultLine {
			out := []defaultLine{
				msg("mail.invite.greeting"), blank(),
				msg("mail.invite.body"), blank(),
				msg("mail.invite.link_label"), varLine("url"),
			}
			// 有效期是可选变量：没有就不出现那一行（不是留一个空行）。
			if strings.TrimSpace(v["expires"]) != "" {
				out = append(out, blank(), msg("mail.invite.expires"))
			}
			return out
		},
	},
	TypeReviewReminder: {
		subjectID: "mail.reminder.subject",
		lines: func(Vars) []defaultLine {
			return []defaultLine{msg("mail.reminder.body")}
		},
	},
	TypeStudyDigest: {
		subjectID: "mail.digest.subject",
		lines: func(Vars) []defaultLine {
			// 六行里有三行都写 `{{.count}}`，读的却是三个不同的数，所以逐行指定取值。
			return []defaultLine{
				{ID: "mail.digest.reviewed", Args: map[string]string{"count": "count"}},
				{ID: "mail.digest.pass_rate", Args: map[string]string{"rate": "rate"}},
				{ID: "mail.digest.streak", Args: map[string]string{"days": "days"}},
				{ID: "mail.digest.new_cards", Args: map[string]string{"count": "new_cards"}},
				{ID: "mail.digest.due", Args: map[string]string{"count": "due"}},
				{ID: "mail.digest.link", Args: map[string]string{"url": "url"}},
			}
		},
	},
	TypeOptimizeDone: {
		subjectID: "mail.optimize.subject",
		lines: func(Vars) []defaultLine {
			return []defaultLine{
				msg("mail.optimize.greeting"), blank(),
				msg("mail.optimize.body"), blank(),
				msg("mail.optimize.link_label"), varLine("url"),
			}
		},
	},
	TypeJobFailed: {
		subjectID: "mail.admin.job_failed.subject",
		lines: func(Vars) []defaultLine {
			return []defaultLine{msg("mail.admin.greeting"), blank(), msg("mail.admin.job_failed.body")}
		},
	},
	TypeMediaDiskAlert: {
		subjectID: "mail.admin.media_alert.subject",
		lines: func(Vars) []defaultLine {
			return []defaultLine{msg("mail.admin.greeting"), blank(), msg("mail.admin.media_alert.body")}
		},
	},
}

// PlaceholderVars 返回该类型的占位符变量：每个变量名映射到 `{{名字}}` 本身。
//
// 管理页拿它调 DefaultTemplate，得到的是**模板文本**（未代入任何真实值），管理员直接在
// 它上面改；发信方则传真实变量。两条路走同一段代码，所以「页面里看到的默认」与
// 「实际发出的兜底」不可能不一样。
func PlaceholderVars(t Type) Vars {
	specs, ok := VarSpecs(t)
	if !ok {
		return Vars{}
	}
	out := make(Vars, len(specs))
	for _, s := range specs {
		out[s.Name] = "{{" + s.Name + "}}"
	}
	return out
}

// DefaultTemplate 返回某类型的内置正文（主题 + 正文）。
//
// 传真实变量 → 可直接发出的纯文本；传 PlaceholderVars(t) → 模板文本。
// 第二个返回值是该类型有没有内置写法（目录里的每个类型都应该有）。
func DefaultTemplate(t Type, variant string, tf TfFunc, vars Vars) (subject, body string, ok bool) {
	spec, found := defaultSpecs[t]
	if !found {
		return "", "", false
	}
	subjectID := spec.subjectID
	if t == TypeEmailVerification && variant == VariantEmailChange {
		subjectID = "mail.verify.change_subject"
	}
	lines := spec.lines(vars)
	if t == TypeEmailVerification && variant == VariantEmailChange {
		lines = changeVariantLines(lines)
	}

	// 可选类邮件的退订入口要出现在正文里：RFC 8058 头只有邮件客户端看得到，网页端与
	// 纯文本阅读器看不到（这条与 vars.go 里「退订链接是必填变量」是同一条口径）。
	if declaresVar(t, "unsubscribe_url") {
		if unsub := strings.TrimSpace(vars["unsubscribe_url"]); unsub != "" {
			lines = append(lines, blank(),
				defaultLine{ID: "mail.footer.unsubscribe", Var: "unsubscribe_url", Join: true})
		}
	}
	return tf(subjectID, anyMap(vars)), renderLines(lines, tf, vars), true
}

// changeVariantLines 把主写法的行换成「改邮箱确认」那一套文案 id。
//
// 两个变体的行序完全相同（问候 / 正文 / 链接 / 有效期），只有语言包 id 不同，
// 所以这里做 id 替换而不是另写一份行序列——另写一份迟早只改一边。
func changeVariantLines(lines []defaultLine) []defaultLine {
	swap := map[string]string{
		"mail.verify.greeting":   "mail.verify.change_greeting",
		"mail.verify.body":       "mail.verify.change_body",
		"mail.verify.link_label": "mail.verify.change_link_label",
		"mail.verify.expires":    "mail.verify.change_expires",
	}
	out := make([]defaultLine, 0, len(lines))
	for _, l := range lines {
		if to, ok := swap[l.ID]; ok {
			l.ID = to
		}
		out = append(out, l)
	}
	return out
}

// renderLines 把行序列渲染成一段文本：语言包行走 tf，变量行取值本身。
func renderLines(lines []defaultLine, tf TfFunc, vars Vars) string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		switch {
		case l.Join && l.ID != "" && l.Var != "":
			out = append(out, tf(l.ID, anyMap(vars))+": "+vars[l.Var])
		case l.ID != "":
			out = append(out, tf(l.ID, lineData(l, vars)))
		case l.Var != "":
			out = append(out, vars[l.Var])
		default:
			out = append(out, "")
		}
	}
	return strings.Join(out, "\n")
}

// lineData 组装某一行要传给语言包的变量：Args 为空时整份变量表都传进去，
// 否则按 Args 指定的键逐项取（值仍可能是 `{{name}}` 占位符，取决于调用方传的是什么）。
func lineData(l defaultLine, vars Vars) map[string]any {
	if len(l.Args) == 0 {
		return anyMap(vars)
	}
	data := make(map[string]any, len(l.Args))
	for msgVar, varsKey := range l.Args {
		data[msgVar] = vars[varsKey]
	}
	return data
}

// declaresVar 报告该类型的变量表里有没有这个变量。用它而不是另设一个开关字段：
// 「可选类邮件要带退订入口」这件事已经由 vars.go 的变量表表达了，再标一遍必漂。
func declaresVar(t Type, name string) bool {
	specs, ok := VarSpecs(t)
	if !ok {
		return false
	}
	for _, s := range specs {
		if s.Name == name {
			return true
		}
	}
	return false
}

// anyMap 把变量表转成 go-i18n 需要的 any 映射（只做类型转换，不动内容）。
func anyMap(vars Vars) map[string]any {
	out := make(map[string]any, len(vars))
	for k, v := range vars {
		out[k] = v
	}
	return out
}
