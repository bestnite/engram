package store

// API Key 总览的写操作审计动作常量。
//
// 单独一个文件而不是扩写 audit.go：audit.go 是共享热点，并行里程碑都可能往里加常量；
// 常量在同包的任意文件里定义效果相同，拆开能避免无谓的改动冲突。
const (
	// ActionAPIKeyCreate 是用户在 /settings/keys 创建自己的 API Key 时写入的审计动作。
	ActionAPIKeyCreate = "api_key.create"
	// ActionAPIKeyRevoke 是管理面板撤销任意用户 API Key 时写入的审计动作。
	ActionAPIKeyRevoke = "api_key.revoke"
)
