package web

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// newWarnerRouter 搭一个只挂诊断中间件的最小路由，可信代理配置与生产装配同源。
func newWarnerRouter(t *testing.T, trusted []string) (*gin.Engine, *bytes.Buffer) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	router := gin.New()
	if err := router.SetTrustedProxies(trusted); err != nil {
		t.Fatalf("set trusted proxies: %v", err)
	}
	router.Use(forwardedIPWarner(logger, trusted))
	router.GET("/", func(c *gin.Context) { c.String(http.StatusOK, c.ClientIP()) })
	return router, &buf
}

func serveFrom(router *gin.Engine, remoteAddr string, headers map[string]string) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = remoteAddr
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	router.ServeHTTP(httptest.NewRecorder(), req)
}

const warnMessage = "forwarded client ip header ignored"

// TestForwardedIPWarner 覆盖告警的触发条件：转发头 + 内网直连 + 不在可信列表。
// 负向用例同样重要：公网来源（多半伪造）与已信任的代理都不得告警。
func TestForwardedIPWarner(t *testing.T) {
	cases := []struct {
		name       string
		trusted    []string
		remoteAddr string
		headers    map[string]string
		wantWarn   bool
		wantRemote string
	}{
		{"private bridge address without trusted proxies", nil, "172.18.0.1:40000",
			map[string]string{"X-Forwarded-For": "198.51.100.7"}, true, "172.18.0.1"},
		{"loopback proxy not in the list", []string{"10.0.0.0/8"}, "127.0.0.1:40000",
			map[string]string{"X-Forwarded-For": "198.51.100.7"}, true, "127.0.0.1"},
		{"ipv6 loopback", nil, "[::1]:40000",
			map[string]string{"X-Forwarded-For": "198.51.100.7"}, true, "::1"},
		{"x-real-ip only", nil, "192.168.1.10:40000",
			map[string]string{"X-Real-IP": "198.51.100.7"}, true, "192.168.1.10"},
		{"trusted cidr", []string{"172.16.0.0/12"}, "172.18.0.1:40000",
			map[string]string{"X-Forwarded-For": "198.51.100.7"}, false, ""},
		{"trusted single ip", []string{"127.0.0.1"}, "127.0.0.1:40000",
			map[string]string{"X-Forwarded-For": "198.51.100.7"}, false, ""},
		{"public remote is likely forged", nil, "203.0.113.9:40000",
			map[string]string{"X-Forwarded-For": "198.51.100.7"}, false, ""},
		{"no forwarded header", nil, "172.18.0.1:40000", nil, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router, buf := newWarnerRouter(t, tc.trusted)
			serveFrom(router, tc.remoteAddr, tc.headers)
			out := buf.String()
			if got := strings.Contains(out, warnMessage); got != tc.wantWarn {
				t.Fatalf("warned = %v, want %v (log %q)", got, tc.wantWarn, out)
			}
			if tc.wantWarn && !strings.Contains(out, "remote_ip="+tc.wantRemote) {
				t.Errorf("log %q does not name remote_ip=%s", out, tc.wantRemote)
			}
		})
	}
}

// TestForwardedIPWarnerLogsOnce 断言每个进程只告警一次，不随请求刷屏。
func TestForwardedIPWarnerLogsOnce(t *testing.T) {
	router, buf := newWarnerRouter(t, nil)
	for range 3 {
		serveFrom(router, "172.18.0.1:40000", map[string]string{"X-Forwarded-For": "198.51.100.7"})
	}
	if n := strings.Count(buf.String(), warnMessage); n != 1 {
		t.Errorf("warning logged %d times, want 1", n)
	}
}
