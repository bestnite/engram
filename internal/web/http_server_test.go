package web

import (
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// TestNewHTTPServerTimeouts 断言上线配置：请求头与空闲连接有超时，整体读写不设超时
// （大文件上传与流式导出不能被截断）。
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

// TestHTTPServerDropsSlowHeaders 是反面用例：只发半截请求头的连接会在请求头超时后被服务端关闭，
// 而不是无限期挂着。超时在这里缩短，只为让测试快速结束；断言的是行为而不是常量。
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
