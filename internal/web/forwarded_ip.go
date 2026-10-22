package web

import (
	"log/slog"
	"net/netip"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

// forwardedIPHeaders are the client IP headers gin trusts by default (the default RemoteIPHeaders
// value); the diagnostics look at only these two, matching the scope ClientIP() parses.
var forwardedIPHeaders = []string{"X-Forwarded-For", "X-Real-IP"}

// parseTrustedPrefixes turns validated TRUSTED_PROXIES entries into prefixes; a single IP counts as /32 or /128.
// SetTrustedProxies has already rejected invalid entries before this runs, so an entry that fails to parse here can simply be skipped.
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

// forwardedIPWarner logs one warning when "the request carries forwarding headers, but the direct address is not in the trusted proxy list".
//
// Not trusting proxies is the safe default, but its cost is silent: a deployment behind a reverse proxy
// takes the proxy's own address as every user's IP, and login rate limiting, auditing and new-device
// alerts all go wrong, while the administrator sees no hint at all.
//
// Only warn for loopback and private-network direct addresses: a reverse proxy is almost always co-located
// or internal, whereas a public source carrying forwarding headers is mostly spoofed. Warning on public
// sources too would let an attacker trigger this one-time log first and lure the admin into trusting their address. Log once per process so requests do not flood it.
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

// inPrefixes reports whether the address falls within any trusted prefix.
func inPrefixes(a netip.Addr, prefixes []netip.Prefix) bool {
	for _, p := range prefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
}
