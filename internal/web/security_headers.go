package web

import (
	"crypto/sha256"
	"encoding/base64"

	"github.com/gin-gonic/gin"
)

// 本文件是 F26：一组固定的安全响应头。CSP 已从 Report-Only 切成强制模式（阻断）：策略按
// 「实际页面资源」逐条收敛（见 cspPolicy 的注释），并由
// TestSecurityHeadersGuardCoversTemplAndScripts 静态守护，防止模板新增内联脚本而策略没跟上。
const (
	// cspHeader 是强制模式的 CSP 头名。一字之差（Content-Security-Policy-Report-Only）即
	// 只上报不阻断，故单独成常量：切换强制/观察只改这一处。强制与 Report-Only 不可同时
	// 下发——两个 CSP 头浏览器只认最后一个，混发会让策略不可预期。
	cspHeader = "Content-Security-Policy"
	// referrerPolicy 跨站只发 origin、站内保留完整路径。分享链接（/s/:token）的凭据在
	// 路径里，跨站一律不回传路径，避免把访问凭据随 Referer 泄漏给第三方。
	referrerPolicy = "strict-origin-when-cross-origin"
	// permissionsPolicy 关掉本站一概不用的浏览器能力，取最小集。
	permissionsPolicy = "accelerometer=(), autoplay=(), camera=(), display-capture=(), encrypted-media=(), fullscreen=(), geolocation=(), gyroscope=(), magnetometer=(), microphone=(), midi=(), payment=(), picture-in-picture=(), publickey-credentials-get=(), screen-wake-lock=(), serial=(), usb=(), xr-spatial-tracking=()"
)

// cspScriptHash 是内联主题引导脚本（themeBootstrapJS，编译期常量）的 SHA-256 白名单 token。
// 全站只有这一处内联 <script>，且内容不随请求变化，所以用 hash 而不是 nonce：不必把
// per-request nonce 穿透到每个 LayoutData，模板也不用改。
var cspScriptHash = func() string {
	sum := sha256.Sum256([]byte(themeBootstrapJS))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}()

// cspPolicy 是依「实际页面资源」逐条收敛出的策略，取值依据（已 grep 模板与静态资源核对）：
//
//   - default-src 'self'：全站自托管，无 CDN（静态资源 go:embed）。
//   - script-src 'self' <主题引导 hash>：外链脚本只有自托管的 htmx / MathJax / media.js /
//     notes.js / review.js / pwa.js / sw.js（base.templ、review.templ）；内联主题引导用
//     hash 放行。这里刻意不写 script-src 'unsafe-inline'：内联脚本已由 hash 精确放行。
//     也不写 'unsafe-eval'：htmx 只在 hx-on / hx-*="js:…" 这类 JS 表达式上才用
//     new Function 编译（DESIGN.md §11）。原先 notes.templ 预览区的
//     hx-on::after-swap 已改成 notes.js 里的委托监听（与 review.js 同一手法），全仓
//     再无触发 eval 的写法，TestSecurityHeadersGuardCoversTemplAndScripts 守着这一点。
//   - style-src 'self' 'unsafe-inline'：tailwind.css 自托管；stats.templ 的进度条用内联
//     style= 属性表达逐行宽度（templ.SafeCSS），decks.templ 的 <noscript> 里还有一段内联
//     <style>（正文来自常量）。CSP 的 hash/nonce 覆盖不到 style 属性，'unsafe-inline'
//     是唯一可行取值。
//   - img-src 'self' https:：图片来自 /media（self）；卡面清洗白名单允许 https 外链图片
//     （internal/render 的 imgSrcPattern），故放行 https，否则卡面外链图会被阻断。
//   - connect-src 'self'：htmx 的 XHR（media.js 触发 change、htmx 处理）与 media.js 的
//     fetch 都是同源。
//   - object-src 'none' / frame-src 'none'：模板里没有 <object>/<embed>/<iframe>。
//   - base-uri 'self' / form-action 'self'：无 <base> 注入面；所有表单都提交到本站。
//   - frame-ancestors 'none'：分享页（/s/:token）是独立页面，没有任何被第三方嵌入的需求，
//     故不允许被任何来源嵌套（与下面的 X-Frame-Options: DENY 一致）。
var cspPolicy = "default-src 'self'; " +
	"script-src 'self' " + cspScriptHash + "; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' https:; " +
	"connect-src 'self'; " +
	"object-src 'none'; " +
	"frame-src 'none'; " +
	"base-uri 'self'; " +
	"form-action 'self'; " +
	"frame-ancestors 'none'"

// securityHeaders 给每个响应统一加五组安全头。挂全局中间件，因此 /media、/api/v1、/mcp、
// 静态资源与 404 回退都覆盖到；静态资源本就自托管（'self'），同一份策略无需特例放宽。
// CSP 是强制头：命中违规即阻断（F26 已从 Report-Only 切换）。
func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set(cspHeader, cspPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", referrerPolicy)
		h.Set("X-Frame-Options", "DENY")
		h.Set("Permissions-Policy", permissionsPolicy)
		c.Next()
	}
}
