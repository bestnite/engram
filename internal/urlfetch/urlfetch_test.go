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
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ---- controlled dependencies ----

// mapResolver returns a fixed set of addresses per host name, used to verify address classification and the "pin to the
// public address" behavior without triggering any real DNS query.
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

// recordingDialer records the address it was asked to dial while always dialing the local test server,
// so that on top of a real HTTPS transport it verifies "which address was validated".
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

// newTestClient builds a client with an injected controlled resolver/dialer; TLS trusts the test server's own certificate.
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

// readAll is a small helper that reads the whole response body in tests: it takes Fetch's two return values directly.
func readAll(rc io.ReadCloser, err error) ([]byte, error) {
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// ---- classification table: which addresses are non-public ----

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

		// Translation/tunnel addresses: even when they embed a loopback or private IPv4, they must never be treated as public targets.
		{"64:ff9b::7f00:1", false},     // NAT64 well-known prefix embedding 127.0.0.1
		{"64:ff9b::a00:1", false},      // NAT64 embedding 10.0.0.1
		{"64:ff9b:1::1", false},        // local-use NAT64 prefix
		{"2002:7f00:1::", false},       // 6to4 embedding 127.0.0.1
		{"2002:c0a8:101::", false},     // 6to4 embedding 192.168.1.1
		{"2001::1", false},             // Teredo (falls inside 2001::/23)
		{"2001:0:1234:5678::1", false}, // Teredo client address
		{"2001:2::1", false},           // benchmarking 2001:2::/48
		{"::192.168.0.1", false},       // IPv4-compatible embedding a private address
		{"::1.2.3.4", false},           // IPv4-compatible
		{"3fff::1", false},             // documentation 3fff::/20
		{"5f00::1", false},             // SRv6 SID segment (outside 2000::/3)
		{"2620:4f:8000::1", false},     // Direct Delegation AS112
		{"1fff::1", false},             // outside the 2000::/3 lower bound
		{"4000::1", false},             // outside the 2000::/3 upper bound

		// Ordinary assigned global unicast: must be allowed through.
		{"2001:4860:4860::8888", true}, // Google Public DNS
		{"2a00:1450:4001:82b::200e", true},
		{"2606:4700:4700::1111", true},
		{"::ffff:93.184.216.34", true}, // IPv4-mapped public address: judged public as IPv4 after Unmap
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

// ---- URL shape ----

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

// ---- real HTTPS transport ----

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
	// The connection must be pinned to the resolved public address, not to the host name itself.
	if !strings.HasPrefix(d.dialed()[0], "93.184.216.34:") {
		t.Errorf("dialed %q, want the validated public address", d.dialed()[0])
	}
}

// TestFetchSpoolsToTemporaryFileAndRemovesIt proves a download is not held in memory and that
// closing the body removes the spooled file.
func TestFetchSpoolsToTemporaryFileAndRemovesIt(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	pkg := []byte("PK\x03\x04 spooled-package-bytes")
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(pkg)
	}))
	defer srv.Close()

	c, _ := newTestClient(t, srv, publicResolver(), DefaultLimits())
	body, err := c.Fetch(context.Background(), "https://example.com/pkg.edeck", 1<<20)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	path := body.(*tempFile).Name()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("spooled file is missing before Close: %v", err)
	}
	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(got) != string(pkg) {
		t.Fatalf("body = %q, want %q", got, pkg)
	}
	if err := body.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("spooled file still exists after Close (err = %v)", err)
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

	// Literal address: no resolver involved, rejected directly by classification.
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

	// Host name resolving to a private address: rejected as well.
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

// TestFetchPinsToPublicAddressOnMixedResolution is the DNS rebinding defense line:
// when the same host resolves to both private and public addresses, only the public address may be dialed.
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

// TestFetchEnforcesMaxBytesChunked covers a chunked response with no Content-Length:
// the limit decision must be based on the bytes actually read, not on the response headers.
func TestFetchEnforcesMaxBytesChunked(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush() // commit the response headers without Content-Length, forcing chunked transfer
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
	// The first request dials the public address; the redirect target 127.0.0.1 must never be dialed.
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

// TestFetchCanonicalisesURL proves the request uses the canonical URL returned by validateURL rather than the raw string:
// leading and trailing whitespace is stripped and the request still goes out on the canonical result.
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

// TestFetchIgnoresEnvironmentProxy proves environment proxies are explicitly disabled:
// even with HTTP(S)_PROXY pointing at a black hole, the download goes direct instead of being forwarded (otherwise the proxy would bypass address validation).
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
	// The resolver deliberately fails; a literal private address should be rejected before resolution and must not pass because "resolution failed".
	c, _ := newTestClient(t, srv, mapResolver{err: errors.New("resolver must not be called")}, DefaultLimits())
	_, err := c.Fetch(context.Background(), "https://127.0.0.1/pkg", 1<<20)
	var fe *Error
	if !errors.As(err, &fe) || fe.Kind != KindBlockedHost {
		t.Fatalf("private literal error = %v, want KindBlockedHost", err)
	}
}

// TestFetchRedirectBoundary pins down the exact redirect limit semantics:
// MaxRedirects=N allows exactly N redirects (the server sees N+1 requests, i.e. the first request plus N follows),
// and N=0 means following none (redirects disabled).
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

// TestFetchRejectsRedirectToSpecialIPv6 is the real-HTTPS case for "special IPv6 targets must be rejected before dialing":
// the server redirects the request to a translation or tunnel address that embeds loopback/private IPv4; the downloader
// must stop it before dialing and never open a connection to those addresses (the dialer should only record the original public address).
func TestFetchRejectsRedirectToSpecialIPv6(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := strings.TrimPrefix(r.URL.Path, "/to-")
		http.Redirect(w, r, "https://["+host+"]/pkg", http.StatusFound)
	}))
	defer srv.Close()

	cases := map[string]string{
		"nat64":  "64:ff9b::7f00:1",  // NAT64 embedding 127.0.0.1
		"6to4":   "2002:7f00:1::",    // 6to4 embedding 127.0.0.1
		"teredo": "2001::1",          // Teredo
		"mapped": "::ffff:127.0.0.1", // IPv4-mapped loopback
		"doc":    "2001:db8::1",      // documentation range
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

// TestFetchClosesConnectionAfterResponse proves a single download does not leave the connection in the pool:
// the server can observe the connection being closed once the response ends (keep-alive off + idle connection reclamation).
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
