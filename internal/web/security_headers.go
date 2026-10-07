package web

import (
	"crypto/sha256"
	"encoding/base64"

	"github.com/gin-gonic/gin"
)

// 本文件是一组固定的安全响应头。CSP 以强制模式下发（命中违规即阻断），不是 Report-Only：
// 策略按「实际页面资源」逐条收敛（依据见 cspPolicy 的注释），并由
// TestSecurityHeadersGuardCoversServedShellAndScripts 静态守护——新出现的内联脚本或外链
// 来源一旦没被策略覆盖，用例就会变红。
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
// per-request 值穿透到拼装应用壳的代码路径，每请求现算一次也没有意义。
var cspScriptHash = func() string {
	sum := sha256.Sum256([]byte(themeBootstrapJS))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}()

// cspPolicy 是依「实际页面资源」逐条收敛出的策略，取值依据（已在源码与构建产物里逐个核对）：
//
//   - default-src 'self'：全站自托管，无 CDN。静态资源（图标、/pwa.js、自托管 MathJax）由
//     go:embed 打进二进制；前端产物（Vite 产出的 /assets/index-*.js 与 index-*.css）同样
//     嵌入后按同源路径下发。
//   - script-src 'self' <主题引导 hash>：外链脚本只有同源的三类——打包产物
//     （/assets/index-*.js）、静态脚本（/pwa.js、自托管 MathJax 的 tex-svg.js）与 Go 现场
//     生成的 service worker（/sw.js），所以 'self' 足够。页面唯一的 <script> 内联块是首屏
//     主题引导（themeBootstrapJS），用它的 SHA-256 精确放行，因此不写 script-src
//     'unsafe-inline'。也不写 'unsafe-eval'：交付的脚本没有一个在运行时编译 JS，守护用例
//     对自有的静态脚本与 /sw.js 断言这一点，命中即变红。
//   - style-src 'self' 'unsafe-inline'：样式表是打包产出的 /assets/index-*.css（同源）。
//     需要 'unsafe-inline' 是因为 SPA 会渲染 inline style 属性：统计页的柱宽
//     （StatsView.svelte）与骨架屏的占位宽度（Skeleton.svelte）都写在该属性里，而 CSP 的
//     hash/nonce 覆盖不到 style 属性，'unsafe-inline' 是唯一可行取值。
//   - img-src 'self' https:：图片来自 /media（self）；卡面清洗白名单允许 https 外链图片
//     （internal/render 的 imgSrcPattern），故放行 https，否则卡面外链图会被阻断。
//   - connect-src 'self'：SPA 的 XHR/fetch（API、上传、service worker 注册）都是同源。
//   - object-src 'none' / frame-src 'none'：交付页面里没有 <object>/<embed>/<iframe>。
//   - base-uri 'self' / form-action 'self'：无 <base> 注入面；表单由 SPA 接手提交
//     （onsubmit 阻止默认行为，改走同源 JSON 端点），没有第三方提交目标。
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
// CSP 是强制头：命中违规即阻断（已从 Report-Only 切换）。
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
