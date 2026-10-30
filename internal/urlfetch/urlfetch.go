// Package urlfetch downloads a public HTTPS direct-link file with built-in SSRF protection.
//
// It does only three things: validate the URL shape, pin the connection to the "validated public address", and cap the
// response bytes. It does not parse web pages, does not follow share pages, and does not accept user credentials; it serves
// only the "download one binary package directly" scenario, so any input that is not HTTPS, carries userinfo, or whose host
// resolves to a non-public address must be rejected.
package urlfetch

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"time"
)

// Kind classifies a download failure. Callers map it to a stable code from this; never parse message.
type Kind int

const (
	// KindInvalidURL means the URL is missing, invalid, not HTTPS, or carries user credentials.
	KindInvalidURL Kind = iota
	// KindBlockedHost means the host resolves to a non-public address (loopback/private/link-local/multicast/reserved ranges, etc.).
	KindBlockedHost
	// KindFetchFailed means connection failure, timeout, too many redirects, or a non-2xx response.
	KindFetchFailed
	// KindTooLarge means the response bytes exceed the caller-supplied limit.
	KindTooLarge
	// KindNotPackage means the response is HTML or otherwise obviously not a deck package.
	KindNotPackage
)

// Error is a classified download error.
//
// Its Error() contains only the reason, not the raw cause: the cause may come from net/http and would carry the full URL,
// whose user credentials and query string may contain signed tokens. The cause is for errors.Is/As only and never goes into logs.
type Error struct {
	Kind   Kind
	Reason string
	cause  error
}

func (e *Error) Error() string { return "url fetch: " + e.Reason }

// Unwrap exposes the underlying cause for errors.Is/As; it does not participate in string output.
func (e *Error) Unwrap() error { return e.cause }

// Limits bounds a single download: the redirect count and three timeouts.
type Limits struct {
	MaxRedirects   int
	DNSTimeout     time.Duration
	ConnectTimeout time.Duration
	TotalTimeout   time.Duration
}

// DefaultLimits gives conservative default bounds.
func DefaultLimits() Limits {
	return Limits{
		MaxRedirects:   5,
		DNSTimeout:     5 * time.Second,
		ConnectTimeout: 10 * time.Second,
		TotalTimeout:   30 * time.Second,
	}
}

// Resolver resolves host names. Production uses net.DefaultResolver; tests inject a controlled implementation to verify classification and pinning behavior.
type Resolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// Dialer establishes a TCP connection. Production uses net.Dialer; tests inject a controlled implementation to point the address at a local server.
type Dialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

// Client downloads a public HTTPS direct link.
type Client struct {
	limits   Limits
	resolver Resolver
	dialer   Dialer
	// tlsConfig exists only for tests to inject a trust root for self-signed certificates; nil in production, where the system roots are used.
	tlsConfig *tls.Config
}

// New constructs the production client: system resolver, system dialer, system root certificates.
func New(limits Limits) *Client {
	return &Client{limits: limits, resolver: net.DefaultResolver, dialer: &net.Dialer{}}
}

// Fetch validates rawURL and downloads its content into a temporary file capped at maxBytes.
// The caller is responsible for Close, which also removes the file, so a package of any size is
// never held in memory on the way to the importer.
//
// The order cannot be swapped: validate the address shape and publicness first, then connect; every redirect goes through
// this step again, so a redirect to a private address is rejected as well.
func (c *Client) Fetch(ctx context.Context, rawURL string, maxBytes int64) (io.ReadCloser, error) {
	u, err := validateURL(rawURL)
	if err != nil {
		return nil, err
	}
	if maxBytes <= 0 {
		return nil, &Error{Kind: KindInvalidURL, Reason: "size limit must be positive"}
	}
	transport := &http.Transport{
		// Disable environment proxies: HTTP_PROXY/HTTPS_PROXY would forward the request elsewhere and bypass address validation.
		Proxy: nil,
		// Disable transparent decompression: the limit must apply to the bytes actually received, otherwise a compression ratio could inflate the limit.
		DisableCompression: true,
		// A new transport is created for each Fetch, so idle connections are not reused by later requests, and the read goroutine holding them would prevent collection.
		// Disable keep-alive so the connection closes once the response body has been read, avoiding long-lived socket usage.
		DisableKeepAlives: true,
		DialContext:       c.dialContext,
		TLSClientConfig:   c.tlsConfig,
	}
	// Belt and braces: if any path leaves an idle connection behind, close it before Fetch returns.
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport:     transport,
		Timeout:       c.limits.TotalTimeout,
		CheckRedirect: c.checkRedirect,
	}
	// Build the request from the validated canonical URL: never hand the raw string to net/http as-is.
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
	// Stream into a temporary file. A package may be gigabytes, so holding the response in memory
	// would defeat streaming the media inside it; the caller imports from this file and Close
	// removes it.
	tmp, err := os.CreateTemp("", "engram-urlfetch-*")
	if err != nil {
		return nil, &Error{Kind: KindFetchFailed, Reason: "cannot create temporary file", cause: err}
	}
	body := &tempFile{File: tmp, path: tmp.Name()}
	fail := func(err error) (io.ReadCloser, error) {
		body.Close()
		return nil, err
	}
	// Read one extra byte to decide whether the limit is exceeded: a package of exactly maxBytes is legal, maxBytes+1 is over it.
	n, err := io.Copy(tmp, io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return fail(classifyFetchError(err))
	}
	if n > maxBytes {
		return fail(&Error{Kind: KindTooLarge, Reason: "response exceeds the size limit"})
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return fail(&Error{Kind: KindFetchFailed, Reason: "cannot rewind temporary file", cause: err})
	}
	head := make([]byte, 512)
	read, err := io.ReadFull(tmp, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return fail(classifyFetchError(err))
	}
	if looksHTML(head[:read]) {
		return fail(&Error{Kind: KindNotPackage, Reason: "the URL returned an HTML page"})
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return fail(&Error{Kind: KindFetchFailed, Reason: "cannot rewind temporary file", cause: err})
	}
	return body, nil
}

// tempFile is a downloaded body backed by a temporary file: closing it removes the file, so a
// caller that defers Close leaves nothing behind on any path.
type tempFile struct {
	*os.File
	path string
}

func (t *tempFile) Close() error {
	err := t.File.Close()
	if rerr := os.Remove(t.path); rerr != nil && !errors.Is(rerr, os.ErrNotExist) && err == nil {
		err = rerr
	}
	return err
}

// dialContext pins the connection to the "public address resolved and validated for this fetch".
//
// It resolves the host name itself, picks the public address itself, and then connects to that address; net/http does not
// resolve a second time, so there is no time window between validation and connection for DNS rebinding to exploit.
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

// resolvePublic resolves a host name into a set of public addresses, dropping every non-public address.
//
// As long as one public address exists among the results it connects to that address and never to a private one — so poisoning
// where "the same host resolves to both a public and a private address" has nowhere to land. When every result is non-public
// (or the host is a private literal) the result is blocked.
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
		// An address carrying a zone can only be something like link-local; skip it outright.
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

// checkRedirect re-validates the address shape on every redirect and limits the redirect count.
// A private target that passes here is still blocked in dialContext; neither layer replaces the other.
func (c *Client) checkRedirect(req *http.Request, via []*http.Request) error {
	// Followed redirects == len(via); reject only when strictly greater than the limit, so MaxRedirects=N
	// allows exactly N redirects, and 0 means following none (redirects disabled).
	if len(via) > c.limits.MaxRedirects {
		return &Error{Kind: KindFetchFailed, Reason: "too many redirects"}
	}
	if _, err := validateURL(req.URL.String()); err != nil {
		return err
	}
	return nil
}

// validateURL validates the URL shape: non-empty, https, no userinfo, has a host.
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
	// Direct-link downloads accept only hierarchical URLs: the opaque form (https:path) is not a downloadable resource locator;
	// a fragment is never sent to the server at all, so its presence is treated as abnormal input.
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

// classifyFetchError normalizes a net/http error into a classified *Error, preserving the nested original classification.
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

// ipv6GlobalUnicast is the IPv6 global unicast range; only addresses falling inside it can be a "regular public address".
var ipv6GlobalUnicast = netip.MustParsePrefix("2000::/3")

// ipv6SpecialPrefixes are the prefixes inside 2000::/3 that IANA registers for special purposes.
//
// They share the range with ordinary global unicast and must be excluded one by one; translation/tunnel/reserved ranges
// outside 2000::/3 (NAT64's 64:ff9b::/96 and 64:ff9b:1::/48, Discard-Only's 100::/64,
// IPv4-compatible ::/96 and so on) are excluded by the range itself. The list is taken from the IANA IPv6 Special-Purpose
// Address Registry, keeping only the entries that fall inside 2000::/3.
var ipv6SpecialPrefixes = []netip.Prefix{
	netip.MustParsePrefix("2001::/23"),         // IETF Protocol Assignments (including Teredo 2001::/32, ORCHID, benchmarking 2001:2::/48, etc.)
	netip.MustParsePrefix("2001:db8::/32"),     // Documentation
	netip.MustParsePrefix("2002::/16"),         // 6to4
	netip.MustParsePrefix("2620:4f:8000::/48"), // Direct Delegation AS112 Service
	netip.MustParsePrefix("3fff::/20"),         // Documentation
}

// isPublicIP decides whether an address is "ordinary assigned global unicast"; everything else is rejected.
//
// SSRF protection takes the conservative stance. IPv4 excludes loopback/private/link-local/multicast and IANA special-purpose
// registered ranges one by one (including the deprecated 6to4 relay anycast 192.88.99.0/24). IPv6 allows only addresses inside
// 2000::/3 that are not part of an IANA special-purpose registration: NAT64 (64:ff9b::/96, 64:ff9b:1::/48),
// IPv4-compatible (::/96), 6to4 (2002::/16), and Teredo (2001::/32, which falls inside 2001::/23);
// documentation and reserved ranges are never treated as public targets. IPv4-mapped IPv6 goes through Unmap into IPv4
// first, so ::ffff:127.0.0.1 and 127.0.0.1 are judged the same.
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
		case b[0] == 192 && b[1] == 88 && b[2] == 99: // 192.88.99.0/24 6to4 relay anycast (deprecated)
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

// isHTMLType reports whether the declared Content-Type is HTML.
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

// looksHTML uses the start of the response body to decide whether the content looks like an HTML page (checking for a tag after stripping the BOM and whitespace).
func looksHTML(raw []byte) bool {
	if len(raw) > 512 {
		raw = raw[:512]
	}
	s := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(string(raw), "\ufeff")))
	return strings.HasPrefix(s, "<!doctype html") || strings.HasPrefix(s, "<html")
}
