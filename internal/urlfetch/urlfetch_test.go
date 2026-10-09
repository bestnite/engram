package urlfetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ---- 受控依赖 ----

// mapResolver 按主机名返回固定的地址集合，用来验证地址分类与「钉死在公网地址」的行为，
// 不触发任何真实 DNS 查询。
type mapResolver struct {
	addrs map[string][]net.IPAddr
	err   error
}

func (m mapResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	if m.err != nil {
		return nil, m.err
	}
	if a, ok := m.addrs[host]; ok {
		return a, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

// recordingDialer 记录它被要求拨打的地址，实际连接固定拨到本地测试服务器，
// 从而在真实 HTTPS 传输之上验证「校验的是哪个地址」。
type recordingDialer struct {
	target string
	addrs  []string
}

func (d *recordingDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	d.addrs = append(d.addrs, address)
	var nd net.Dialer
	return nd.DialContext(ctx, network, d.target)
}

func (d *recordingDialer) dialed() []string { return d.addrs }

// newTestClient 构造一个注入受控解析器/拨号器的客户端；TLS 信任测试服务器自己的证书。
func newTestClient(t *testing.T, srv *httptest.Server, res Resolver, lim Limits) (*Client, *recordingDialer) {
	t.Helper()
	d := &recordingDialer{target: srv.Listener.Addr().String()}
	c := &Client{limits: lim, resolver: res, dialer: d}
	if tr, ok := srv.Client().Transport.(*http.Transport); ok && tr.TLSClientConfig != nil {
		c.tlsConfig = tr.TLSClientConfig
	}
	return c, d
}

func publicResolver() mapResolver {
	return mapResolver{addrs: map[string][]net.IPAddr{
		"example.com": {{IP: net.ParseIP("93.184.216.34")}},
	}}
}

// readAll 是测试里读完整响应体的小包装：直接接收 Fetch 的两个返回值。
func readAll(rc io.ReadCloser, err error) ([]byte, error) {
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// ---- 分类表：哪些地址是非公网 ----

func TestIsPublicIPClassifiesSpecialRanges(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"93.184.216.34", true},
		{"1.1.1.1", true},
		{"2606:4700:4700::1111", true},
		{"0.0.0.0", false},
		{"0.1.2.3", false}, // 0.0.0.0/8
		{"127.0.0.1", false},
		{"10.0.0.5", false},
		{"172.16.0.1", false},
		{"192.168.1.1", false},
		{"169.254.10.1", false},
		{"100.64.0.1", false}, // CGNAT
		{"192.0.0.1", false},
		{"192.0.2.1", false}, // TEST-NET-1
		{"198.18.0.1", false},
		{"198.51.100.1", false},
		{"203.0.113.1", false},
		{"224.0.0.1", false},
		{"240.0.0.1", false},
		{"255.255.255.255", false},
		{"::1", false},
		{"::", false},
		{"fe80::1", false},
		{"fc00::1", false},
		{"ff02::1", false},
		{"2001:db8::1", false},
		{"::ffff:127.0.0.1", false}, // IPv4-mapped loopback
		{"::ffff:10.0.0.1", false},  // IPv4-mapped private

		// 翻译/隧道地址：即使内嵌了回环或私网 IPv4，也绝不能当成公网目标。
		{"64:ff9b::7f00:1", false},     // NAT64 well-known 前缀内嵌 127.0.0.1
		{"64:ff9b::a00:1", false},      // NAT64 内嵌 10.0.0.1
		{"64:ff9b:1::1", false},        // 本地用途 NAT64 前缀
		{"2002:7f00:1::", false},       // 6to4 内嵌 127.0.0.1
		{"2002:c0a8:101::", false},     // 6to4 内嵌 192.168.1.1
		{"2001::1", false},             // Teredo（落在 2001::/23）
		{"2001:0:1234:5678::1", false}, // Teredo 客户端地址
		{"2001:2::1", false},           // 基准测试 2001:2::/48
		{"::192.168.0.1", false},       // IPv4-compatible 内嵌私网
		{"::1.2.3.4", false},           // IPv4-compatible
		{"3fff::1", false},             // 文档 3fff::/20
		{"5f00::1", false},             // SRv6 SID 段（2000::/3 之外）
		{"2620:4f:8000::1", false},     // Direct Delegation AS112
		{"1fff::1", false},             // 2000::/3 下界之外
		{"4000::1", false},             // 2000::/3 上界之外

		// 普通已分配的全局单播：必须放行。
		{"2001:4860:4860::8888", true}, // Google Public DNS
		{"2a00:1450:4001:82b::200e", true},
		{"2606:4700:4700::1111", true},
		{"::ffff:93.184.216.34", true}, // IPv4-mapped 公网：Unmap 后按 IPv4 判为公网
	}
	for _, tc := range cases {
		t.Run(tc.ip, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			if ip == nil {
				t.Fatalf("test bug: cannot parse %q", tc.ip)
			}
			addr, ok := netip.AddrFromSlice(ip)
			if !ok {
				t.Fatalf("test bug: cannot convert %q", tc.ip)
			}
			if got := isPublicIP(addr); got != tc.want {
				t.Errorf("isPublicIP(%s) = %v, want %v", tc.ip, got, tc.want)
			}
		})
	}
}

// ---- URL 形态 ----

func TestValidateURL(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"https ok", "https://example.com/pkg.edeck", false},
		{"https with port and query", "https://example.com:8443/pkg?sig=abc", false},
		{"http rejected", "http://example.com/pkg", true},
		{"ftp rejected", "ftp://example.com/pkg", true},
		{"file rejected", "file:///etc/passwd", true},
		{"userinfo rejected", "https://user:pass@example.com/pkg", true},
		{"empty rejected", "   ", true},
		{"no host rejected", "https:///pkg", true},
		{"garbage rejected", "://", true},
		{"opaque rejected", "https:example.com/pkg", true},
		{"fragment rejected", "https://example.com/pkg#frag", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateURL(tc.raw)
			if tc.wantErr && err == nil {
				t.Fatalf("validateURL(%q) = nil, want error", tc.raw)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateURL(%q) = %v, want nil", tc.raw, err)
			}
			if err != nil && err.Error() != "" && strings.Contains(err.Error(), tc.raw) && strings.Contains(tc.raw, "@") {
				t.Errorf("validateURL error leaked the raw url: %q", err.Error())
			}
		})
	}
}

// ---- 真实 HTTPS 传输 ----

func TestFetchDownloadsPublicPackage(t *testing.T) {
	pkg := []byte("PK\x03\x04 fake-but-binary-package-bytes")
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pkg.edeck" {
			http.NotFound(w, r)
			return
		}
		w.Write(pkg)
	}))
	defer srv.Close()

	c, d := newTestClient(t, srv, publicResolver(), DefaultLimits())
	got, err := readAll(c.Fetch(context.Background(), "https://example.com/pkg.edeck", 1<<20))
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if string(got) != string(pkg) {
		t.Errorf("Fetch() body = %q, want %q", got, pkg)
	}
	if len(d.dialed()) != 1 {
		t.Fatalf("dialed %v, want exactly one connection", d.dialed())
	}
	// 连接必须钉死在解析出的公网地址上，而不是主机名本身。
	if !strings.HasPrefix(d.dialed()[0], "93.184.216.34:") {
		t.Errorf("dialed %q, want the validated public address", d.dialed()[0])
	}
}

func TestFetchRejectsNonHTTPSAndUserinfo(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	c, d := newTestClient(t, srv, publicResolver(), DefaultLimits())

	for _, raw := range []string{
		"http://example.com/pkg",
		"ftp://example.com/pkg",
		"https://user:pass@example.com/pkg",
		"",
	} {
		_, err := c.Fetch(context.Background(), raw, 1<<20)
		var fe *Error
		if !errors.As(err, &fe) || fe.Kind != KindInvalidURL {
			t.Errorf("Fetch(%q) error = %v, want KindInvalidURL", raw, err)
		}
	}
	if len(d.dialed()) != 0 {
		t.Errorf("dialed %v, want no connection for rejected URLs", d.dialed())
	}
}

func TestFetchBlocksNonPublicHosts(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	// 字面量地址：不经过解析器，直接按分类拒绝。
	literals := []string{
		"https://127.0.0.1/pkg", "https://10.0.0.5/pkg", "https://192.168.1.1/pkg",
		"https://169.254.1.1/pkg", "https://0.0.0.0/pkg", "https://100.64.0.1/pkg",
		"https://192.0.2.1/pkg", "https://198.18.0.1/pkg", "https://240.0.0.1/pkg",
		"https://[::1]/pkg", "https://[fc00::1]/pkg", "https://[fe80::1]/pkg",
		"https://[::ffff:127.0.0.1]/pkg", "https://[2001:db8::1]/pkg",
	}
	for _, raw := range literals {
		c, d := newTestClient(t, srv, publicResolver(), DefaultLimits())
		_, err := c.Fetch(context.Background(), raw, 1<<20)
		var fe *Error
		if !errors.As(err, &fe) || fe.Kind != KindBlockedHost {
			t.Errorf("Fetch(%q) error = %v, want KindBlockedHost", raw, err)
		}
		if len(d.dialed()) != 0 {
			t.Errorf("Fetch(%q) dialed %v, want no connection", raw, d.dialed())
		}
	}

	// 主机名解析到私网地址：同样拒绝。
	c, d := newTestClient(t, srv, mapResolver{addrs: map[string][]net.IPAddr{
		"internal.example.com": {{IP: net.ParseIP("10.0.0.5")}},
	}}, DefaultLimits())
	_, err := c.Fetch(context.Background(), "https://internal.example.com/pkg", 1<<20)
	var fe *Error
	if !errors.As(err, &fe) || fe.Kind != KindBlockedHost {
		t.Fatalf("private-resolving host error = %v, want KindBlockedHost", err)
	}
	if len(d.dialed()) != 0 {
		t.Errorf("dialed %v, want no connection for a private-resolving host", d.dialed())
	}
}

// TestFetchPinsToPublicAddressOnMixedResolution 是 DNS rebinding 防线：
// 同一主机同时解析出私网与公网地址时，只允许连公网地址。
func TestFetchPinsToPublicAddressOnMixedResolution(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("PK\x03\x04 payload"))
	}))
	defer srv.Close()
	res := mapResolver{addrs: map[string][]net.IPAddr{
		"example.com": {
			{IP: net.ParseIP("10.0.0.5")},
			{IP: net.ParseIP("93.184.216.34")},
		},
	}}
	c, d := newTestClient(t, srv, res, DefaultLimits())
	if _, err := readAll(c.Fetch(context.Background(), "https://example.com/pkg", 1<<20)); err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	dialed := d.dialed()
	if len(dialed) != 1 || !strings.HasPrefix(dialed[0], "93.184.216.34:") {
		t.Fatalf("dialed %v, want the public address only (never the private one)", dialed)
	}
	for _, addr := range dialed {
		if strings.Contains(addr, "10.0.0.5") {
			t.Errorf("dialed the private address %q; rebinding must be impossible", addr)
		}
	}
}

func TestFetchEnforcesMaxBytes(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, 128))
	}))
	defer srv.Close()

	c, _ := newTestClient(t, srv, publicResolver(), DefaultLimits())
	if _, err := readAll(c.Fetch(context.Background(), "https://example.com/pkg", 128)); err != nil {
		t.Fatalf("exactly-at-limit body error = %v, want nil", err)
	}
	_, err := c.Fetch(context.Background(), "https://example.com/pkg", 127)
	var fe *Error
	if !errors.As(err, &fe) || fe.Kind != KindTooLarge {
		t.Fatalf("over-limit body error = %v, want KindTooLarge", err)
	}
}

// TestFetchEnforcesMaxBytesChunked 覆盖没有 Content-Length 的分块响应：
// 上限判定必须基于实际读到的字节，而不是响应头。
func TestFetchEnforcesMaxBytesChunked(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush() // 提交响应头但不带 Content-Length，强制分块传输
		}
		w.Write(make([]byte, 256))
	}))
	defer srv.Close()

	c, _ := newTestClient(t, srv, publicResolver(), DefaultLimits())
	_, err := c.Fetch(context.Background(), "https://example.com/pkg", 128)
	var fe *Error
	if !errors.As(err, &fe) || fe.Kind != KindTooLarge {
		t.Fatalf("chunked over-limit body error = %v, want KindTooLarge", err)
	}
}

func TestFetchRejectsNon2xx(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()
	c, _ := newTestClient(t, srv, publicResolver(), DefaultLimits())
	_, err := c.Fetch(context.Background(), "https://example.com/pkg", 1<<20)
	var fe *Error
	if !errors.As(err, &fe) || fe.Kind != KindFetchFailed {
		t.Fatalf("404 error = %v, want KindFetchFailed", err)
	}
}

func TestFetchBoundsRedirects(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.com/loop", http.StatusFound)
	}))
	defer srv.Close()
	lim := DefaultLimits()
	lim.MaxRedirects = 3
	c, _ := newTestClient(t, srv, publicResolver(), lim)
	_, err := c.Fetch(context.Background(), "https://example.com/loop", 1<<20)
	var fe *Error
	if !errors.As(err, &fe) || fe.Kind != KindFetchFailed {
		t.Fatalf("redirect-loop error = %v, want KindFetchFailed", err)
	}
}

func TestFetchRejectsRedirectToPrivateAndNonHTTPS(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/to-private":
			http.Redirect(w, r, "https://127.0.0.1/pkg", http.StatusFound)
		case "/to-http":
			http.Redirect(w, r, "http://example.com/pkg", http.StatusFound)
		default:
			w.Write([]byte("PK\x03\x04"))
		}
	}))
	defer srv.Close()

	c, d := newTestClient(t, srv, publicResolver(), DefaultLimits())
	if _, err := c.Fetch(context.Background(), "https://example.com/to-private", 1<<20); func() bool {
		var fe *Error
		return !errors.As(err, &fe) || fe.Kind != KindBlockedHost
	}() {
		t.Fatalf("redirect-to-private error = %v, want KindBlockedHost", err)
	}
	// 首次请求连的是公网地址，重定向目标 127.0.0.1 绝不能被拨号。
	for _, addr := range d.dialed() {
		if strings.HasPrefix(addr, "127.0.0.1:") {
			t.Errorf("dialed the private redirect target %q", addr)
		}
	}

	c2, _ := newTestClient(t, srv, publicResolver(), DefaultLimits())
	_, err := c2.Fetch(context.Background(), "https://example.com/to-http", 1<<20)
	var fe *Error
	if !errors.As(err, &fe) || fe.Kind != KindInvalidURL {
		t.Fatalf("redirect-to-http error = %v, want KindInvalidURL", err)
	}
}

// TestFetchCanonicalisesURL 证明请求用的是 validateURL 返回的规范化 URL，
// 而不是原始字符串：前后空白会被去掉，请求仍按规范化结果发出。
func TestFetchCanonicalisesURL(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pkg.edeck" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte("PK\x03\x04 payload"))
	}))
	defer srv.Close()

	c, d := newTestClient(t, srv, publicResolver(), DefaultLimits())
	if _, err := readAll(c.Fetch(context.Background(), "  https://example.com/pkg.edeck  ", 1<<20)); err != nil {
		t.Fatalf("Fetch() with a whitespace-padded url error = %v, want success", err)
	}
	if dialed := d.dialed(); len(dialed) != 1 || !strings.HasPrefix(dialed[0], "93.184.216.34:") {
		t.Fatalf("dialed %v, want the validated public address", dialed)
	}
}

func TestFetchRejectsHTML(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/typed":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			io.WriteString(w, "<!DOCTYPE html><html></html>")
		case "/sniffed":
			w.Header().Set("Content-Type", "application/octet-stream")
			io.WriteString(w, "  <html><body>login</body></html>")
		default:
			w.Write([]byte("PK\x03\x04"))
		}
	}))
	defer srv.Close()
	c, _ := newTestClient(t, srv, publicResolver(), DefaultLimits())
	for _, path := range []string{"/typed", "/sniffed"} {
		_, err := c.Fetch(context.Background(), "https://example.com"+path, 1<<20)
		var fe *Error
		if !errors.As(err, &fe) || fe.Kind != KindNotPackage {
			t.Errorf("Fetch(%s) error = %v, want KindNotPackage", path, err)
		}
	}
}

func TestFetchTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	lim := DefaultLimits()
	lim.TotalTimeout = 200 * time.Millisecond
	c, _ := newTestClient(t, srv, publicResolver(), lim)
	_, err := c.Fetch(context.Background(), "https://example.com/slow", 1<<20)
	var fe *Error
	if !errors.As(err, &fe) || fe.Kind != KindFetchFailed {
		t.Fatalf("timeout error = %v, want KindFetchFailed", err)
	}
}

// TestFetchIgnoresEnvironmentProxy 证明环境代理被显式关闭：
// 即使设置了指向黑洞的 HTTP(S)_PROXY，下载也走直连而不是被转发出去（否则代理会绕过地址校验）。
func TestFetchIgnoresEnvironmentProxy(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("PK\x03\x04 payload"))
	}))
	defer srv.Close()
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("NO_PROXY", "")

	c, _ := newTestClient(t, srv, publicResolver(), DefaultLimits())
	if _, err := readAll(c.Fetch(context.Background(), "https://example.com/pkg", 1<<20)); err != nil {
		t.Fatalf("Fetch() with a bogus proxy env error = %v, want success (proxy must be ignored)", err)
	}
}

func TestFetchRejectsPrivateLiteralWithoutResolving(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	// 解析器故意报错；字面量私网地址应在解析之前就被拒，不能因为「解析失败」而放行。
	c, _ := newTestClient(t, srv, mapResolver{err: errors.New("resolver must not be called")}, DefaultLimits())
	_, err := c.Fetch(context.Background(), "https://127.0.0.1/pkg", 1<<20)
	var fe *Error
	if !errors.As(err, &fe) || fe.Kind != KindBlockedHost {
		t.Fatalf("private literal error = %v, want KindBlockedHost", err)
	}
}

// TestFetchRedirectBoundary 固化重定向上限的精确语义：
// MaxRedirects=N 恰好放行 N 次重定向（服务端看到 N+1 个请求，即首个请求加 N 次跟随），
// N=0 表示一次也不跟随（禁用重定向）。
func TestFetchRedirectBoundary(t *testing.T) {
	for _, n := range []int{0, 1, 3} {
		t.Run(fmt.Sprintf("limit=%d", n), func(t *testing.T) {
			var hits int32
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&hits, 1)
				http.Redirect(w, r, "https://example.com/loop", http.StatusFound)
			}))
			defer srv.Close()

			lim := DefaultLimits()
			lim.MaxRedirects = n
			c, _ := newTestClient(t, srv, publicResolver(), lim)
			_, err := c.Fetch(context.Background(), "https://example.com/loop", 1<<20)
			var fe *Error
			if !errors.As(err, &fe) || fe.Kind != KindFetchFailed {
				t.Fatalf("limit=%d error = %v, want KindFetchFailed", n, err)
			}
			if got := int(atomic.LoadInt32(&hits)); got != n+1 {
				t.Fatalf("limit=%d: server saw %d requests, want %d (first request + exactly %d redirects)",
					n, got, n+1, n)
			}
		})
	}
}

// TestFetchRejectsRedirectToSpecialIPv6 是「特殊 IPv6 目标必须在拨号前被拒」的真实 HTTPS 用例：
// 服务端把请求重定向到内嵌回环/私网的翻译或隧道地址；下载器必须在拨号前拦下，
// 绝不向这些地址发起连接（拨号器只应记录最初那个公网地址）。
func TestFetchRejectsRedirectToSpecialIPv6(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := strings.TrimPrefix(r.URL.Path, "/to-")
		http.Redirect(w, r, "https://["+host+"]/pkg", http.StatusFound)
	}))
	defer srv.Close()

	cases := map[string]string{
		"nat64":  "64:ff9b::7f00:1",  // NAT64 内嵌 127.0.0.1
		"6to4":   "2002:7f00:1::",    // 6to4 内嵌 127.0.0.1
		"teredo": "2001::1",          // Teredo
		"mapped": "::ffff:127.0.0.1", // IPv4-mapped 回环
		"doc":    "2001:db8::1",      // 文档段
	}
	for name, addr := range cases {
		t.Run(name, func(t *testing.T) {
			c, d := newTestClient(t, srv, publicResolver(), DefaultLimits())
			_, err := c.Fetch(context.Background(), "https://example.com/to-"+addr, 1<<20)
			var fe *Error
			if !errors.As(err, &fe) || fe.Kind != KindBlockedHost {
				t.Fatalf("redirect to %s error = %v, want KindBlockedHost", addr, err)
			}
			dialed := d.dialed()
			if len(dialed) != 1 || !strings.HasPrefix(dialed[0], "93.184.216.34:") {
				t.Fatalf("dialed %v, want only the original public address (special target rejected before dialing)", dialed)
			}
			for _, a := range dialed {
				if strings.Contains(a, addr) {
					t.Errorf("dialed the special address %q; it must be rejected before dialing", a)
				}
			}
		})
	}
}

// TestFetchClosesConnectionAfterResponse 证明一次下载不会把连接留在池里：
// 服务端能观察到连接在响应结束后被关闭（keep-alive 已关 + 闲置连接回收）。
func TestFetchClosesConnectionAfterResponse(t *testing.T) {
	closed := make(chan struct{}, 1)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("PK\x03\x04 payload"))
	}))
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateClosed {
			select {
			case closed <- struct{}{}:
			default:
			}
		}
	}
	srv.StartTLS()
	defer srv.Close()

	c, _ := newTestClient(t, srv, publicResolver(), DefaultLimits())
	if _, err := readAll(c.Fetch(context.Background(), "https://example.com/pkg", 1<<20)); err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("server never observed the client closing the connection: the socket leaked")
	}
}
