// Package pgtest 提供"真 PostgreSQL 门控 + 每次测试独立 schema"的测试基础设施，
// 供 internal/store、internal/auth 等所有需要真 PG 的门控测试复用。
//
// 为什么不能用同一个库裸跑：PostgreSQL 是持久化的共享实例，测试写入的行、自增序列
// 与唯一索引都会留到下一次运行，于是同一用例的第二次执行会撞上主键/唯一键冲突
// （`-count=2` 或直接重跑必炸）。SQLite 路径每个用例用 t.TempDir() 建全新库文件，
// 天然隔离，所以这个缺陷只在 PG 下暴露。这里为每次 Open 调用创建独立的 schema，
// 并把连接的 search_path 固定到该 schema；用例结束（t.Cleanup）时 DROP SCHEMA
// ... CASCADE 连同其中所有表一起回收，隔离程度与 SQLite 的临时库等价。
package pgtest

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// EnvDSN 是启用 PostgreSQL 路径的环境变量；未设置时调用方应跳过 PG 路径。
//
// 默认不设：本机与 CI 都只跑 SQLite、不起数据库服务（AGENTS.md §2.5）。要覆盖生产用的
// PG 驱动时，指一个可用的实例手动跑，例如
//
//	TEST_PG_DSN='postgres://user:CHANGE_ME@127.0.0.1:5432/db?sslmode=disable' go test ./internal/store/ ./internal/schedule/ ./internal/digest/
const EnvDSN = "TEST_PG_DSN"

// Open 在 TEST_PG_DSN 未设置时返回 (nil, false)，让调用方跳过 PG 路径；
// 设置时返回一个绑定到全新 schema 的 *gorm.DB，并在 t.Cleanup 里丢弃该 schema。
// schema 名由测试名加随机后缀生成，因此并发跑同一用例也不会互相覆盖。
func Open(t *testing.T) (*gorm.DB, bool) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv(EnvDSN))
	if dsn == "" {
		t.Logf("%s not set: skipping the PostgreSQL path on this machine", EnvDSN)
		return nil, false
	}

	schema := schemaName(t)

	// 管理连接只用于建/删 schema，不承载被测数据。
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	if err := admin.Exec("CREATE SCHEMA " + quoteIdent(schema)).Error; err != nil {
		t.Fatalf("create schema %s: %v", schema, err)
	}
	t.Cleanup(func() {
		if err := admin.Exec("DROP SCHEMA " + quoteIdent(schema) + " CASCADE").Error; err != nil {
			t.Errorf("drop schema %s: %v", schema, err)
		}
		closeGorm(admin)
	})

	scopedDSN, err := withSearchPath(dsn, schema)
	if err != nil {
		t.Fatalf("add search_path to %s: %v", EnvDSN, err)
	}
	db, err := gorm.Open(postgres.Open(scopedDSN), &gorm.Config{})
	if err != nil {
		t.Fatalf("open postgres (schema %s): %v", schema, err)
	}
	t.Cleanup(func() { closeGorm(db) })
	return db, true
}

// withSearchPath 把 search_path 作为连接参数写回 DSN；pgx 会把未知参数当作
// 启动期运行时参数发给服务端，因此连接池里每条新连接都会落到目标 schema。
func withSearchPath(dsn, schema string) (string, error) {
	if u, err := url.Parse(dsn); err == nil && u.Scheme != "" {
		q := u.Query()
		q.Set("search_path", schema)
		u.RawQuery = q.Encode()
		return u.String(), nil
	}
	// 关键字/值 形式的 DSN（"host=... user=..."）直接追加一个键值对。
	return dsn + " search_path=" + schema, nil
}

// quoteIdent 给标识符加双引号。schema 名由本包生成、只含 [a-z0-9_]，
// 不存在注入面，这里加引号只是为了防御性地保留大小写与关键字冲突。
func quoteIdent(ident string) string {
	return `"` + ident + `"`
}

// schemaName 生成既合法又唯一的 schema 名：前缀 + 清洗过的测试名 + 随机后缀。
// PostgreSQL 标识符上限 63 字节，因此对测试名截断。
func schemaName(t *testing.T) string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generate schema suffix: %v", err)
	}
	name := strings.ToLower(t.Name())
	var sb strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			sb.WriteRune(r)
		default:
			sb.WriteByte('_')
		}
	}
	cleaned := sb.String()
	if len(cleaned) > 40 {
		cleaned = cleaned[:40]
	}
	return fmt.Sprintf("engram_test_%s_%s", cleaned, hex.EncodeToString(b[:]))
}

// closeGorm 关闭底层 sql.DB，避免测试进程里累积连接。
func closeGorm(db *gorm.DB) {
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}
