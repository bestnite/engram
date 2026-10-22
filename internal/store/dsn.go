package store

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// This file solves: when the parent directory of DB_DSN does not exist, SQLite reports
// \"unable to open database file: out of memory (14)\" — naming neither the path nor the directory,
// and \"out of memory\" is just a misleading artifact of SQLite's errno, which points first-time deployers in the wrong direction.
//
// Option (A) is chosen: create the parent directory automatically for the sqlite driver, rather than only making the error clearer. Reasons:
//   - SQLite is this project's single-file / self-hosted development driver (data/engram.db in the README), and the parent directory is
//     an implementation detail of where the file lands; os.MkdirAll is idempotent, so creating it has no side effects.
//   - It removes the manual \"mkdir -p data\" step from the README, which is exactly the first-run experience problem being solved.
//   - Strictly limited to sqlite: a postgres DSN is a network address and must never get any directory operation.
//   - Real failures (parent path is a file, directory not writable, etc.) still error clearly: a failed directory creation names the directory,
//     a failed open names the database file path, and \"out of memory\" no longer appears in either.
//
// It also keeps the benefit of (B): when sqlite fails to open, SQLite's misleading errno text, reused for every file-level error,
// is replaced with a clear error that names the path (see sqliteOpenError).

// sqliteDBPath extracts the database file path from a SQLite DSN; it returns an empty string for an in-memory DB or an empty DSN.
// Shapes to handle: a bare path data/engram.db, a file: URI (file:data/x.db?_pragma=...),
// :memory:, and in-memory DBs with mode=memory. When no path can be recovered it returns \"\" and the caller skips directory creation.
func sqliteDBPath(dsn string) string {
	s := strings.TrimSpace(dsn)
	if s == "" || strings.Contains(s, "mode=memory") {
		return ""
	}
	if i := strings.IndexByte(s, '?'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimPrefix(s, "file:")
	if s == "" || s == ":memory:" {
		return ""
	}
	// the path in a file: URI may be percent-escaped; on parse failure it is used as-is.
	if decoded, err := url.PathUnescape(s); err == nil {
		s = decoded
	}
	return s
}

// prepareSQLiteDir idempotently creates the parent directory of the database file for a sqlite DSN.
// Call it only when driver == \"sqlite\"; it never touches postgres.
func prepareSQLiteDir(dsn string) error {
	path := sqliteDBPath(dsn)
	if path == "" {
		return nil
	}
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		// real errors (parent path is a file, insufficient permissions, read-only filesystem) name the directory here, instead of
		// letting SQLite spit out an \"out of memory (14)\" afterwards.
		return fmt.Errorf("create sqlite database directory %q: %w", dir, err)
	}
	return nil
}

// sqliteOpenError replaces SQLite's open error with clear text naming the path.
// SQLite returns the same \"unable to open database file: out of memory (14)\" for 'parent directory does not exist / not writable / the path itself is a directory',
// where \"out of memory\" is an errno artifact rather than a real memory problem, so this text is no
// longer passed through here (requirement).
func sqliteOpenError(path string, err error) error {
	if path == "" {
		return fmt.Errorf("open database: %w", err)
	}
	if strings.Contains(err.Error(), "unable to open database file") {
		return fmt.Errorf(
			"open sqlite database %q: cannot open database file (check that the parent directory exists and is writable)",
			path,
		)
	}
	return fmt.Errorf("open sqlite database %q: %w", path, err)
}
