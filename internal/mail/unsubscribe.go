package mail

import "strings"

// 本文件是 M1-22（RFC 8058 一键退订）的邮件头部分：只有「可选类型」才带退订头。
//
// 分类依据仍是目录里的 Class：CanDisable 为真的类（B/C/D）才允许用户退订，
// 因此才在邮件里放退订入口；A 类安全/事务邮件一律不带，允许退订安全邮件等于给人留后门。
//
// 这里只构造纯头（不签发令牌、不碰数据库）：令牌由发信方用 ActionTokenService 签发，
// 明文只出现在退订 URL 里，绝不进日志。发信方拿 URL 调 UnsubscribeHeaders 即可，
// 头名与取值只有这一处定义，避免 B/C/D 各自拼写漂移。

// RFC 8058 的两个退订头名；取值英文且稳定（邮件协议字段，不本地化）。
const (
	// HeaderListUnsubscribe 指向一键退订地址（RFC 8058 的 https URL）。
	HeaderListUnsubscribe = "List-Unsubscribe"
	// HeaderListUnsubscribePost 声明该地址支持 One-Click POST（RFC 8058）。
	HeaderListUnsubscribePost = "List-Unsubscribe-Post"
)

// ListUnsubscribePostValue 是 List-Unsubscribe-Post 的固定取值（RFC 8058 §3.1）。
// 邮件客户端按此值对 List-Unsubscribe 里的 URL 发一个 body 为该值的 POST。
const ListUnsubscribePostValue = "List-Unsubscribe=One-Click"

// CanUnsubscribe 报告某类型是否属于「可选类型」：登记在目录里且所属类可被用户关闭。
// 未登记的类型返回 false（拼错的类型名既不发信也不带退订头）。
func CanUnsubscribe(t Type) bool {
	def, ok := Lookup(t)
	if !ok {
		return false
	}
	return CanDisable(def.Class)
}

// UnsubscribeHeaders 为可选类型的邮件构造 RFC 8058 退订头。
//
// url 是带一次性令牌的绝对退订地址（由发信方拼好）；为空或类型不可退订时返回 nil，
// 调用方据此不写 Headers —— 因此 A 类邮件绝不会出现退订头。
func UnsubscribeHeaders(t Type, url string) map[string]string {
	if !CanUnsubscribe(t) {
		return nil
	}
	url = strings.TrimSpace(url)
	if url == "" {
		return nil
	}
	return map[string]string{
		HeaderListUnsubscribe:     "<" + url + ">",
		HeaderListUnsubscribePost: ListUnsubscribePostValue,
	}
}
