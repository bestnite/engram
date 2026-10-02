package mail

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/gorm"

	"example.com/engram/internal/store"
)

// testDB 开一个临时 SQLite 库并迁移全部模型（含 mail_outbox）。
func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "mail.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := store.AutoMigrate(context.Background(), db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	return db
}

// testCodec 用程序化生成的占位密钥构造编解码器，不在仓库里放真实密钥字面量。
func testCodec(t *testing.T) *store.SecretCodec {
	t.Helper()
	// 32 字节的 base64（重复的 0x2a），仅测试用。
	key := "KioqKioqKioqKioqKioqKioqKioqKioqKioqKioqKio="
	codec, err := store.NewSecretCodec(key)
	if err != nil {
		t.Fatalf("NewSecretCodec: %v", err)
	}
	return codec
}

// resolverWith 构造一个用 map 充当环境变量的解析器。
func resolverWith(t *testing.T, db *gorm.DB, codec *store.SecretCodec, env map[string]string) *Resolver {
	t.Helper()
	return &Resolver{
		DB:      db,
		Secrets: codec,
		LookupEnv: func(k string) (string, bool) {
			v, ok := env[k]
			return v, ok
		},
	}
}

// setSettings 把若干 SMTP 设置写进 settings 表。
func setSettings(t *testing.T, db *gorm.DB, kv map[string]string) {
	t.Helper()
	now := time.Now().UTC()
	for k, v := range kv {
		if err := store.PutSetting(context.Background(), db, k, v, nil, now); err != nil {
			t.Fatalf("PutSetting(%s): %v", k, err)
		}
	}
}

// splitHostPort 把 "host:port" 拆成两段；测试断言用，非法输入立即失败。
func splitHostPort(t *testing.T, addr string) (string, string) {
	t.Helper()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort(%q): %v", addr, err)
	}
	return host, port
}
