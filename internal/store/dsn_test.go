package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSQLiteDBPath 覆盖从 DSN 里取路径的各种形态（AGENTS.md §5 M0-13）。
func TestSQLiteDBPath(t *testing.T) {
	cases := []struct {
		name string
		dsn  string
		want string
	}{
		{"plain relative", "data/flashcard.db", "data/flashcard.db"},
		{"plain absolute", "/var/lib/flashcard/db.sqlite", "/var/lib/flashcard/db.sqlite"},
		{"file uri", "file:data/flashcard.db", "data/flashcard.db"},
		{"file uri with params", "file:data/flashcard.db?_pragma=busy_timeout(5000)", "data/flashcard.db"},
		{"plain with params", "data/flashcard.db?cache=shared", "data/flashcard.db"},
		{"memory colon", ":memory:", ""},
		{"file memory", "file::memory:?cache=shared", ""},
		{"mode memory", "file:memdb1?mode=memory&cache=shared", ""},
		{"empty", "   ", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sqliteDBPath(tc.dsn); got != tc.want {
				t.Fatalf("sqliteDBPath(%q) = %q, want %q", tc.dsn, got, tc.want)
			}
		})
	}
}

// TestOpenSQLiteCreatesMissingParentDir 是 M0-13 的正向验收：
// 父目录不存在时，sqlite 驱动应自动、幂等地创建它并成功打开，而不是报 out of memory。
func TestOpenSQLiteCreatesMissingParentDir(t *testing.T) {
	base := t.TempDir()
	// 故意多套一层不存在的目录，验证 MkdirAll 递归创建。
	dbPath := filepath.Join(base, "nested", "deeper", "flashcard.db")

	db, err := Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("Open with missing parent dir should succeed, got error: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB(): %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	// 父目录被创建，且数据库文件真的落在这里（而不是内存里）。
	if info, err := os.Stat(filepath.Dir(dbPath)); err != nil || !info.IsDir() {
		t.Fatalf("parent dir was not created: info=%v err=%v", info, err)
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("database file was not created at %q: %v", dbPath, err)
	}

	// 幂等：再打开一次不应报错，也不应改变路径。
	db2, err := Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("reopen should be idempotent, got error: %v", err)
	}
	sqlDB2, err := db2.DB()
	if err != nil {
		t.Fatalf("db2.DB(): %v", err)
	}
	t.Cleanup(func() { _ = sqlDB2.Close() })
}

// TestOpenSQLiteFileURICreatesMissingParentDir 覆盖带参数的 file: URI 形态。
func TestOpenSQLiteFileURICreatesMissingParentDir(t *testing.T) {
	base := t.TempDir()
	dbPath := filepath.Join(base, "uri", "flashcard.db")
	dsn := "file:" + dbPath + "?_pragma=busy_timeout(5000)"

	db, err := Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("Open with file: URI and missing parent dir should succeed, got: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB(): %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("database file was not created at %q: %v", dbPath, err)
	}
}

// TestOpenSQLiteParentIsFileErrorIsClear 覆盖“路径不可写”类真错误：
// 父路径是一个普通文件时，MkdirAll 失败必须点名该目录，且不得出现 out of memory。
func TestOpenSQLiteParentIsFileErrorIsClear(t *testing.T) {
	base := t.TempDir()
	notDir := filepath.Join(base, "afile")
	if err := os.WriteFile(notDir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(notDir, "flashcard.db")

	_, err := Open("sqlite", dbPath)
	if err == nil {
		t.Fatal("expected an error when the parent path is a regular file")
	}
	msg := err.Error()
	if !strings.Contains(msg, notDir) {
		t.Errorf("error should name the parent path %q, got: %v", notDir, msg)
	}
	if strings.Contains(msg, "out of memory") {
		t.Errorf("error must not contain the misleading 'out of memory', got: %v", msg)
	}
}

// TestOpenSQLitePathIsDirNoOutOfMemory 覆盖 SQLite 对文件级错误复用 errno 的情形：
// DSN 指向一个已存在的目录时，打开失败的错误要点名路径且不得出现 out of memory。
func TestOpenSQLitePathIsDirNoOutOfMemory(t *testing.T) {
	dir := t.TempDir()

	_, err := Open("sqlite", dir)
	if err == nil {
		t.Fatal("expected an error when the DSN points at a directory")
	}
	msg := err.Error()
	if !strings.Contains(msg, dir) {
		t.Errorf("error should name the path %q, got: %v", dir, msg)
	}
	if strings.Contains(msg, "out of memory") {
		t.Errorf("error must not contain the misleading 'out of memory', got: %v", msg)
	}
}

// TestOpenPostgresDoesNotCreateDirs 守住“只对 sqlite 建目录”这条边界：
// postgres 的 DSN 里即使出现一个不存在的路径，也不得被创建成目录。
func TestOpenPostgresDoesNotCreateDirs(t *testing.T) {
	base := t.TempDir()
	sockDir := filepath.Join(base, "no-such-socket-dir")
	// 关键字式 postgres DSN：host 指向一个不存在的目录（unix socket 形态）。
	dsn := "host=" + sockDir + " port=1 user=u dbname=d sslmode=disable"

	if _, err := Open("postgres", dsn); err == nil {
		t.Fatal("expected postgres open to fail against a bogus host")
	}
	if _, err := os.Stat(sockDir); !os.IsNotExist(err) {
		t.Fatalf("postgres DSN must not create directories, but %q exists (stat err: %v)", sockDir, err)
	}
}
