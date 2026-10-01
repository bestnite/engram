package store

// M6-2 用户管理各写操作的审计动作常量。
//
// 单独一个文件而不是扩写 audit.go：audit.go 是共享热点，并行里程碑都可能往里加常量；
// 常量在同包的任意文件里定义效果相同，拆开能避免无谓的改动冲突。
const (
	ActionUserDisable       = "user.disable"
	ActionUserEnable        = "user.enable"
	ActionUserRoleChange    = "user.role_change"
	ActionUserPasswordReset = "user.password_reset"
	ActionUserForceLogout   = "user.force_logout"
	ActionUserDelete        = "user.delete"
)
