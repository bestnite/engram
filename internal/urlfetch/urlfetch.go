// Package urlfetch 下载一个公开的 HTTPS 直链文件，内置 SSRF 防护。
//
// 它只做三件事：校验 URL 形态、把连接钉死在「校验过的公网地址」上、把响应字节限制在上限内。
// 它不解析网页、不跟随分享页、不接受用户凭据，只服务于「直接下载一个二进制包」这一种场景；
// 因此任何非 HTTPS、带 userinfo、主机落在非公网地址的输入都必须被拒。
package urlfetch

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// Kind 是下载失败的分类。调用方据此映射稳定 code，绝不解析 message。
type Kind int

const (
	// KindInvalidURL 表示 URL 缺失、非法、非 HTTPS，或带有用户凭据。
	KindInvalidURL Kind = iota
	// KindBlockedHost 表示主机解析到非公网地址（回环/私网/链路本地/多播/保留段等）。
	KindBlockedHost
	// KindFetchFailed 表示连接失败、超时、重定向过多或响应非 2xx。
	KindFetchFailed
	// KindTooLarge 表示响应字节超过调用方给出的上限。
	KindTooLarge
	// KindNotPackage 表示响应是 HTML 之类显然不是卡组包的内容。
	KindNotPackage
)

// Error 是带分类的下载错误。
//
// 它的 Error() 只含原因、不含原始 cause：cause 可能来自 net/http，会带上完整 URL，
// 而 URL 的用户凭据与查询串可能含签名令牌。cause 只供 errors.Is/As 使用，绝不进日志。
type Error struct {
	Kind   Kind
	Reason string
	cause  error
}

func (e *Error) Error() string { return "url fetch: " + e.Reason }

// Unwrap 暴露底层原因，供 errors.Is/As 判断；不参与字符串输出。
func (e *Error) Unwrap() error { return e.cause }

// Limits 是一次下载的边界：重定向次数与三段超时。
type Limits struct {
	MaxRedirects   int
	DNSTimeout     time.Duration
	ConnectTimeout time.Duration
	TotalTimeout   time.Duration
}

// DefaultLimits 给出保守的默认边界。
func DefaultLimits() Limits {
	return Limits{
		MaxRedirects:   5,
		DNSTimeout:     5 * time.Second,
		ConnectTimeout: 10 * time.Second,
		TotalTimeout:   30 * time.Second,
	}
}

// Resolver 解析主机名。生产用 net.DefaultResolver；测试注入受控实现以验证分类与钉死行为。
type Resolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// Dialer 建立一个 TCP 连接。生产用 net.Dialer；测试注入受控实现以便把地址指向本地服务器。
type Dialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

// Client 下载公开的 HTTPS 直链。
type Client struct {
	limits   Limits
	resolver Resolver
	dialer   Dialer
	// tlsConfig 仅供测试注入自签证书的信任根；生产为 nil，用系统根证书校验。
	tlsConfig *tls.Config
}

// New 构造生产客户端：系统解析器、系统拨号器、系统根证书。
func New(limits Limits) *Client {
	return &Client{limits: limits, resolver: net.DefaultResolver, dialer: &net.Dialer{}}
}

// Fetch 校验 rawURL 并下载其内容，把响应字节限制在 maxBytes 内返回。调用方负责 Close。
//
// 顺序不可换：先做地址形态与公网性校验，再建立连接；每一次重定向都会重新走这一步，
// 因此重定向到一个私网地址同样会被拒。
func (c *Client) Fetch(ctx context.Context, rawURL string, maxBytes int64) (io.ReadCloser, error) {
	u, err := validateURL(rawURL)
	if err != nil {
		return nil, err
	}
	if maxBytes <= 0 {
		return nil, &Error{Kind: KindInvalidURL, Reason: "size limit must be positive"}
	}
	transport := &http.Transport{
		// 关闭环境代理：HTTP_PROXY/HTTPS_PROXY 会把请求转发到别处，绕过地址校验。
		Proxy: nil,
		// 关闭透明解压：上限必须针对「实际收到的字节」，否则压缩比可以放大上限。
		DisableCompression: true,
		// 每次 Fetch 都新建 transport，闲置连接不会供后续请求复用，持有它的读取协程也会阻止回收。
		// 禁用 keep-alive，让连接在响应体读完后关闭，避免长期占用套接字。
		DisableKeepAlives: true,
		DialContext:       c.dialContext,
		TLSClientConfig:   c.tlsConfig,
	}
	// 双保险：任何路径若留下闲置连接，也在 Fetch 返回前关掉。
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport:     transport,
		Timeout:       c.limits.TotalTimeout,
		CheckRedirect: c.checkRedirect,
	}
	// 用校验后的规范化 URL 建请求：绝不把原始字符串原样交给 net/http。
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, &Error{Kind: KindInvalidURL, Reason: "cannot build request", cause: err}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, classifyFetchError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &Error{Kind: KindFetchFailed, Reason: fmt.Sprintf("unexpected status %d", resp.StatusCode)}
	}
	if ct := resp.Header.Get("Content-Type"); isHTMLType(ct) {
		return nil, &Error{Kind: KindNotPackage, Reason: "the URL returned an HTML page"}
	}
	if resp.ContentLength > maxBytes {
		return nil, &Error{Kind: KindTooLarge, Reason: "response exceeds the size limit"}
	}
	// 多读一个字节用来判定「是否超限」：恰好 maxBytes 的包合法，maxBytes+1 即超限。
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, classifyFetchError(err)
	}
	if int64(len(raw)) > maxBytes {
		return nil, &Error{Kind: KindTooLarge, Reason: "response exceeds the size limit"}
	}
	if looksHTML(raw) {
		return nil, &Error{Kind: KindNotPackage, Reason: "the URL returned an HTML page"}
	}
	return io.NopCloser(bytes.NewReader(raw)), nil
}

// dialContext 把连接钉死在「本次解析并校验过的公网地址」上。
//
// 它自己解析主机名、自己挑选公网地址、再对那个地址发起连接；net/http 不会做第二次解析，
// 因此校验与连接之间没有可被 DNS rebinding 利用的时间窗。
func (c *Client) dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, &Error{Kind: KindInvalidURL, Reason: "invalid dial address", cause: err}
	}
	addrs, err := c.resolvePublic(ctx, host)
	if err != nil {
		return nil, err
	}
	var last error
	for _, ip := range addrs {
		dctx, cancel := context.WithTimeout(ctx, c.limits.ConnectTimeout)
		conn, derr := c.dialer.DialContext(dctx, network, net.JoinHostPort(ip.String(), port))
		cancel()
		if derr == nil {
			return conn, nil
		}
		last = derr
	}
	return nil, &Error{Kind: KindFetchFailed, Reason: "cannot connect to host", cause: last}
}

// resolvePublic 把主机名解析成一组公网地址；非公网地址一律剔除。
//
// 只要解析结果里存在公网地址就连那个地址，绝连私网地址——因此「同一主机同时解析出公网与
// 私网」的投毒也无处落脚。全部结果都非公网（或主机就是私网字面量）时判为 blocked。
func (c *Client) resolvePublic(ctx context.Context, host string) ([]netip.Addr, error) {
	host = strings.TrimSuffix(host, ".")
	if ip, err := netip.ParseAddr(host); err == nil {
		if !isPublicIP(ip) {
			return nil, blockedHost()
		}
		return []netip.Addr{ip.Unmap()}, nil
	}
	dctx, cancel := context.WithTimeout(ctx, c.limits.DNSTimeout)
	defer cancel()
	ips, err := c.resolver.LookupIPAddr(dctx, host)
	if err != nil {
		return nil, &Error{Kind: KindFetchFailed, Reason: "cannot resolve host", cause: err}
	}
	out := make([]netip.Addr, 0, len(ips))
	for _, a := range ips {
		// 带 zone 的地址只可能是链路本地一类，直接跳过。
		if a.Zone != "" {
			continue
		}
		addr, ok := netip.AddrFromSlice(a.IP)
		if !ok || !isPublicIP(addr) {
			continue
		}
		out = append(out, addr.Unmap())
	}
	if len(out) == 0 {
		return nil, blockedHost()
	}
	return out, nil
}

// checkRedirect 在每次重定向上重新校验地址形态，并限制重定向次数。
// 私网目标就算通过了这里，也会在 dialContext 被拦下；两层校验互不替代。
func (c *Client) checkRedirect(req *http.Request, via []*http.Request) error {
	// 已跟随的重定向次数 == len(via)；严格大于上限才拒绝，因此 MaxRedirects=N
	// 恰好放行 N 次重定向，0 表示一次也不跟随（禁用重定向）。
	if len(via) > c.limits.MaxRedirects {
		return &Error{Kind: KindFetchFailed, Reason: "too many redirects"}
	}
	if _, err := validateURL(req.URL.String()); err != nil {
		return err
	}
	return nil
}

// validateURL 校验 URL 形态：非空、https、无 userinfo、有主机。
func validateURL(raw string) (*url.URL, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, &Error{Kind: KindInvalidURL, Reason: "url is empty"}
	}
	u, err := url.Parse(s)
	if err != nil {
		return nil, &Error{Kind: KindInvalidURL, Reason: "url is not valid", cause: err}
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return nil, &Error{Kind: KindInvalidURL, Reason: "only https is supported"}
	}
	if u.User != nil {
		return nil, &Error{Kind: KindInvalidURL, Reason: "url must not carry credentials"}
	}
	// 直链下载只接受层次化 URL：opaque 形式（https:path）不是可下载的资源定位；
	// fragment 根本不会发给服务器，出现即视为异常输入。
	if u.Opaque != "" {
		return nil, &Error{Kind: KindInvalidURL, Reason: "url must be hierarchical"}
	}
	if u.Fragment != "" {
		return nil, &Error{Kind: KindInvalidURL, Reason: "url must not carry a fragment"}
	}
	if u.Hostname() == "" {
		return nil, &Error{Kind: KindInvalidURL, Reason: "url has no host"}
	}
	return u, nil
}

// classifyFetchError 把 net/http 的错误归一成带分类的 *Error，保留嵌套的原始分类。
func classifyFetchError(err error) error {
	var fe *Error
	if errors.As(err, &fe) {
		return fe
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &Error{Kind: KindFetchFailed, Reason: "download timed out", cause: err}
	}
	return &Error{Kind: KindFetchFailed, Reason: "download failed", cause: err}
}

func blockedHost() error {
	return &Error{Kind: KindBlockedHost, Reason: "host does not resolve to a public address"}
}

// ipv6GlobalUnicast 是 IPv6 的全球单播范围；只有落在这里的地址才可能是「普通公网地址」。
var ipv6GlobalUnicast = netip.MustParsePrefix("2000::/3")

// ipv6SpecialPrefixes 是落于 2000::/3 之内、却被 IANA 登记为特殊用途的前缀。
//
// 它们与普通全球单播同段，必须逐条排除；2000::/3 之外的翻译/隧道/保留段
// （NAT64 的 64:ff9b::/96 与 64:ff9b:1::/48、Discard-Only 的 100::/64、
// IPv4-compatible 的 ::/96 等）由范围本身挡下。清单取自 IANA IPv6 Special-Purpose
// Address Registry，只保留落在 2000::/3 内的条目。
var ipv6SpecialPrefixes = []netip.Prefix{
	netip.MustParsePrefix("2001::/23"),         // IETF Protocol Assignments（含 Teredo 2001::/32、ORCHID、基准测试 2001:2::/48 等）
	netip.MustParsePrefix("2001:db8::/32"),     // Documentation
	netip.MustParsePrefix("2002::/16"),         // 6to4
	netip.MustParsePrefix("2620:4f:8000::/48"), // Direct Delegation AS112 Service
	netip.MustParsePrefix("3fff::/20"),         // Documentation
}

// isPublicIP 判定一个地址是否是「普通已分配的全局单播」，其余一律拒绝。
//
// SSRF 防护取保守口径。IPv4 逐条排除回环/私网/链路本地/多播与 IANA 特殊用途登记段
// （含 192.88.99.0/24 这段已废弃的 6to4 relay anycast）。IPv6 只放行 2000::/3 之内、
// 且不属于 IANA 特殊用途登记的地址：NAT64（64:ff9b::/96、64:ff9b:1::/48）、
// IPv4-compatible（::/96）、6to4（2002::/16）、Teredo（2001::/32，落在 2001::/23 内）、
// 文档与保留段都不会被当成公网目标。IPv4-mapped IPv6 先经 Unmap 归入 IPv4 判定，
// 因此 ::ffff:127.0.0.1 与 127.0.0.1 同判。
func isPublicIP(ip netip.Addr) bool {
	if !ip.IsValid() || ip.Zone() != "" {
		return false
	}
	ip = ip.Unmap()
	if ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return false
	}
	if ip.Is4() {
		b := ip.As4()
		switch {
		case b[0] == 0: // 0.0.0.0/8 "this network"
			return false
		case b[0] == 100 && b[1] >= 64 && b[1] <= 127: // 100.64.0.0/10 CGNAT
			return false
		case b[0] == 192 && b[1] == 0 && (b[2] == 0 || b[2] == 2): // 192.0.0.0/24, 192.0.2.0/24
			return false
		case b[0] == 192 && b[1] == 88 && b[2] == 99: // 192.88.99.0/24 6to4 relay anycast（已废弃）
			return false
		case b[0] == 198 && (b[1] == 18 || b[1] == 19): // 198.18.0.0/15 benchmarking
			return false
		case b[0] == 198 && b[1] == 51 && b[2] == 100: // 198.51.100.0/24 TEST-NET-2
			return false
		case b[0] == 203 && b[1] == 0 && b[2] == 113: // 203.0.113.0/24 TEST-NET-3
			return false
		case b[0] >= 240: // 240.0.0.0/4 reserved, incl. 255.255.255.255
			return false
		}
		return true
	}
	if !ipv6GlobalUnicast.Contains(ip) {
		return false
	}
	for _, p := range ipv6SpecialPrefixes {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

// isHTMLType 判断声明的 Content-Type 是否是 HTML。
func isHTMLType(ct string) bool {
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	switch strings.ToLower(strings.TrimSpace(ct)) {
	case "text/html", "application/xhtml+xml":
		return true
	default:
		return false
	}
}

// looksHTML 用响应体的开头判断内容是否像 HTML 页面（去掉 BOM 与空白后看标签）。
func looksHTML(raw []byte) bool {
	if len(raw) > 512 {
		raw = raw[:512]
	}
	s := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(string(raw), "\ufeff")))
	return strings.HasPrefix(s, "<!doctype html") || strings.HasPrefix(s, "<html")
}
