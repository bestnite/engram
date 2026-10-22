package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSQLiteDBPath covers the various shapes a path can take inside a DSN.
func TestSQLiteDBPath(t *testing.T) {
	cases := []struct {
		name string
		dsn  string
		want string
	}{
		{"plain relative", "data/engram.db", "data/engram.db"},
		{"plain absolute", "/var/lib/engram/db.sqlite", "/var/lib/engram/db.sqlite"},
		{"file uri", "file:data/engram.db", "data/engram.db"},
		{"file uri with params", "file:data/engram.db?_pragma=busy_timeout(5000)", "data/engram.db"},
		{"plain with params", "data/engram.db?cache=shared", "data/engram.db"},
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

// TestOpenSQLiteCreatesMissingParentDir is the positive acceptance case:
// when the parent directory is missing, the sqlite driver must create it automatically and idempotently and open successfully, instead of reporting out of memory.
func TestOpenSQLiteCreatesMissingParentDir(t *testing.T) {
	base := t.TempDir()
	// deliberately nest one more level of non-existent directory to verify that MkdirAll creates recursively.
	dbPath := filepath.Join(base, "nested", "deeper", "engram.db")

	db, err := Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("Open with missing parent dir should succeed, got error: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB(): %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	// the parent directory is created, and the database file really lands there (not in memory).
	if info, err := os.Stat(filepath.Dir(dbPath)); err != nil || !info.IsDir() {
		t.Fatalf("parent dir was not created: info=%v err=%v", info, err)
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("database file was not created at %q: %v", dbPath, err)
	}

	// idempotent: opening again must neither error nor change the path.
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

// TestOpenSQLiteFileURICreatesMissingParentDir covers the file: URI shape with parameters.
func TestOpenSQLiteFileURICreatesMissingParentDir(t *testing.T) {
	base := t.TempDir()
	dbPath := filepath.Join(base, "uri", "engram.db")
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

// TestOpenSQLiteParentIsFileErrorIsClear covers real 'path not writable' errors:
// when the parent path is an ordinary file, a failed MkdirAll must name that directory, and out of memory must not appear.
func TestOpenSQLiteParentIsFileErrorIsClear(t *testing.T) {
	base := t.TempDir()
	notDir := filepath.Join(base, "afile")
	if err := os.WriteFile(notDir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(notDir, "engram.db")

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

// TestOpenSQLitePathIsDirNoOutOfMemory covers the case where SQLite reuses one errno for file-level errors:
// when the DSN points at an existing directory, the open failure must name the path and must not mention out of memory.
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

// TestOpenPostgresDoesNotCreateDirs guards the boundary 'create directories only for sqlite':
// even if a postgres DSN contains a non-existent path, it must not be created as a directory.
func TestOpenPostgresDoesNotCreateDirs(t *testing.T) {
	base := t.TempDir()
	sockDir := filepath.Join(base, "no-such-socket-dir")
	// keyword-style postgres DSN: host points at a non-existent directory (unix socket form).
	dsn := "host=" + sockDir + " port=1 user=u dbname=d sslmode=disable"

	if _, err := Open("postgres", dsn); err == nil {
		t.Fatal("expected postgres open to fail against a bogus host")
	}
	if _, err := os.Stat(sockDir); !os.IsNotExist(err) {
		t.Fatalf("postgres DSN must not create directories, but %q exists (stat err: %v)", sockDir, err)
	}
}
