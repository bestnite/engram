package web

import (
	"bufio"
	"encoding/base64"
	"net"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// testSecretKey 是测试用的 ENCRYPTION_KEY：base64(32 字节)，与任何真实密钥无关。
var testSecretKey = base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))

// startFakeSMTP 在测试进程内起一个最小 SMTP 服务，只应答投递所需命令。
// 返回 "host:port"；测试结束自动关闭。不依赖外网或真实 SMTP 服务。
func startFakeSMTP(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := bufio.NewReader(c)
				w := bufio.NewWriter(c)
				say := func(s string) { _, _ = w.WriteString(s + "\r\n"); _ = w.Flush() }
				say("220 fake ready")
				inData := false
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					line = strings.TrimRight(line, "\r\n")
					if inData {
						if line == "." {
							inData = false
							say("250 accepted")
						}
						continue
					}
					switch {
					case strings.HasPrefix(strings.ToUpper(line), "EHLO"),
						strings.HasPrefix(strings.ToUpper(line), "HELO"):
						say("250 greet")
					case strings.HasPrefix(strings.ToUpper(line), "DATA"):
						say("354 go")
						inData = true
					case strings.HasPrefix(strings.ToUpper(line), "QUIT"):
						say("221 bye")
						return
					default:
						say("250 ok")
					}
				}
			}(conn)
		}
	}()
	return ln.Addr().String()
}

// deadAddr 返回一个当前无人监听的本地地址。
func deadAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

// mustCodec 构造一个测试用编解码器（32 字节占位密钥）。
func mustCodec(t *testing.T) *store.SecretCodec {
	t.Helper()
	codec, err := store.NewSecretCodec(testSecretKey)
	if err != nil {
		t.Fatalf("NewSecretCodec: %v", err)
	}
	return codec
}
