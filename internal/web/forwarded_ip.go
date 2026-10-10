package web

import (
	"log/slog"
	"net/netip"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

// forwardedIPHeaders 是 gin 默认采信的客户端 IP 头（RemoteIPHeaders 的缺省值），
// 诊断只看这两个，与 ClientIP() 的解析范围保持一致。
var forwardedIPHeaders = []string{"X-Forwarded-For", "X-Real-IP"}

// parseTrustedPrefixes 把已校验的 TRUSTED_PROXIES 条目转成前缀；单个 IP 视为 /32 或 /128。
// 调用前 SetTrustedProxies 已拒绝非法项，这里遇到解析失败的条目直接跳过即可。
func parseTrustedPrefixes(entries []string) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(entries))
	for _, entry := range entries {
		if strings.Contains(entry, "/") {
			if p, err := netip.ParsePrefix(entry); err == nil {
				out = append(out, p.Masked())
			}
			continue
		}
		if a, err := netip.ParseAddr(entry); err == nil {
			a = a.Unmap()
			out = append(out, netip.PrefixFrom(a, a.BitLen()))
		}
	}
	return out
}

// forwardedIPWarner 在「请求带着转发头，但直连地址不在可信代理列表里」时记一次警告。
//
// 不信任代理是安全的缺省，但它的代价是静默的：反向代理后面的部署会把代理自己的地址
// 当成每个用户的 IP，登录限流、审计和新设备提醒全部失真，而管理员看不到任何提示。
// 这条日志把这个错配变成可见的，并点名直连地址，方便填入 TRUSTED_PROXIES。
//
// 只对回环与私有网段的直连地址告警：反向代理几乎总在同机或内网，而公网来源带转发头
// 多半是伪造。若对公网来源也告警，攻击者就能抢先触发这唯一一次日志，诱导管理员把
// 攻击者地址写进可信列表。每个进程只记一次，避免每个请求刷屏。
func forwardedIPWarner(logger *slog.Logger, trusted []string) gin.HandlerFunc {
	prefixes := parseTrustedPrefixes(trusted)
	var once sync.Once
	return func(c *gin.Context) {
		header := ""
		for _, name := range forwardedIPHeaders {
			if strings.TrimSpace(c.GetHeader(name)) != "" {
				header = name
				break
			}
		}
		if header != "" {
			if remote, err := netip.ParseAddr(c.RemoteIP()); err == nil {
				remote = remote.Unmap()
				if (remote.IsLoopback() || remote.IsPrivate()) && !inPrefixes(remote, prefixes) {
					once.Do(func() {
						logger.Warn("forwarded client ip header ignored because the connecting address is not a trusted proxy; "+
							"if requests reach this server through a reverse proxy, add that proxy's address to TRUSTED_PROXIES",
							"header", header,
							"remote_ip", remote.String(),
							"trusted_proxies", len(prefixes),
						)
					})
				}
			}
		}
		c.Next()
	}
}

// inPrefixes 判断地址是否落在任一可信前缀内。
func inPrefixes(a netip.Addr, prefixes []netip.Prefix) bool {
	for _, p := range prefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
}
