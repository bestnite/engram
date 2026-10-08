package store

// 个人设置页的审计动作常量。
// 单独一个文件而不是扩写 audit.go：audit.go 是共享热点，并行里程碑都可能往里加常量；
// 常量在同包的任意文件里定义效果相同，拆开能避免无谓的改动冲突。
const (
	// ActionUserProfileUpdate 记录本人修改显示名 / 语言 / 时区 / 复习日切点。
	ActionUserProfileUpdate = "user.profile_update"
	// ActionUserPasswordChange 记录本人在设置页改密码。
	ActionUserPasswordChange = "user.password_change"
)
