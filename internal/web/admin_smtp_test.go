package web

import (
	"bufio"
	"encoding/base64"
	"net"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// testSecretKey is the ENCRYPTION_KEY used in tests: base64(32 bytes), unrelated to any real key.
var testSecretKey = base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))

// startFakeSMTP starts a minimal SMTP server inside the test process, answering only the commands delivery needs.
// Returns "host:port"; closed automatically when the test ends. Depends on neither the internet nor a real SMTP server.
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

// deadAddr returns a local address nobody is currently listening on.
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

// mustCodec builds a codec for tests (a 32-byte placeholder key).
func mustCodec(t *testing.T) *store.SecretCodec {
	t.Helper()
	codec, err := store.NewSecretCodec(testSecretKey)
	if err != nil {
		t.Fatalf("NewSecretCodec: %v", err)
	}
	return codec
}
