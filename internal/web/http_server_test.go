package web

import (
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// TestNewHTTPServerTimeouts asserts the production configuration: request headers and idle connections have timeouts, and
// the overall read/write has none (large file uploads and streaming exports must not be truncated).
func TestNewHTTPServerTimeouts(t *testing.T) {
	srv := newHTTPServer(":0", http.NotFoundHandler())
	cases := []struct {
		name string
		got  time.Duration
		ok   func(time.Duration) bool
	}{
		{"ReadHeaderTimeout is set", srv.ReadHeaderTimeout, func(d time.Duration) bool { return d > 0 }},
		{"IdleTimeout is set", srv.IdleTimeout, func(d time.Duration) bool { return d > 0 }},
		{"ReadTimeout is unset", srv.ReadTimeout, func(d time.Duration) bool { return d == 0 }},
		{"WriteTimeout is unset", srv.WriteTimeout, func(d time.Duration) bool { return d == 0 }},
	}
	for _, tc := range cases {
		if !tc.ok(tc.got) {
			t.Errorf("%s: got %v", tc.name, tc.got)
		}
	}
}

// TestHTTPServerDropsSlowHeaders is the negative case: a connection that sends only a partial request header is closed by the server
// after the header timeout instead of hanging forever. The timeouts are shortened here only so the test finishes fast; what is asserted is behaviour, not constants.
func TestHTTPServerDropsSlowHeaders(t *testing.T) {
	srv := newHTTPServer("", http.NotFoundHandler())
	srv.ReadHeaderTimeout = 200 * time.Millisecond
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost: example.com\r\n")); err != nil {
		t.Fatalf("write partial header: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	start := time.Now()
	_, err = io.ReadAll(conn)
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatalf("server kept the slow connection open for %v", time.Since(start))
	}
}
