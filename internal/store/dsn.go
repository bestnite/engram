package store

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// 本文件解决 ROADMAP.md M0-13：DB_DSN 的父目录不存在时，SQLite 会给出
// "unable to open database file: out of memory (14)"——既没点名路径，也没提目录，
// 而 "out of memory" 只是 SQLite errno 的误导产物，会把首次部署者引向错误方向。
//
// 选择方案 (A)：对 sqlite 驱动自动创建父目录，而不是只把报错写清楚。理由：
//   - SQLite 是本项目的单文件/自托管开发驱动（README 的 data/engram.db），父目录属于
//     文件落点这一实现细节；os.MkdirAll 幂等，创建它没有副作用。
//   - 它消除了 README 里 "mkdir -p data" 这一步手动操作，正是 M0-13 想解决的首跑体验问题。
//   - 严格限定在 sqlite：postgres 的 DSN 是网络地址，绝不对其做任何目录操作。
//   - 真正的失败（父路径是文件、目录不可写等）仍会明确报错：目录创建失败会点名目录，
//     打开失败会点名数据库文件路径，且都不会再出现 "out of memory"。
//
// 另外仍然保留了 (B) 的收益：sqlite 打开失败时，把 SQLite 那个对所有文件级错误都复用的
// 误导性 errno 文案换成分名路径的清晰错误（见 sqliteOpenError）。

// sqliteDBPath 从 SQLite DSN 里取出数据库文件路径；内存库或空 DSN 返回空串。
// 需要处理的形态：裸路径 data/engram.db、file: URI（file:data/x.db?_pragma=...）、
// :memory:、以及带 mode=memory 的内存库。取不到路径时返回 ""，调用方跳过目录创建。
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
	// file: URI 里路径可能做了百分号转义；解析失败时按原样使用。
	if decoded, err := url.PathUnescape(s); err == nil {
		s = decoded
	}
	return s
}

// prepareSQLiteDir 为 sqlite DSN 幂等地创建数据库文件的父目录。
// 只应在 driver == "sqlite" 时调用；不触碰 postgres。
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
		// 真错误（父路径是文件、权限不足、只读文件系统）在这里点名目录，而不是
		// 让 SQLite 事后吐出一个 "out of memory (14)"。
		return fmt.Errorf("create sqlite database directory %q: %w", dir, err)
	}
	return nil
}

// sqliteOpenError 把 SQLite 的打开错误换成分名路径的清晰文案。
// SQLite 对“父目录不存在 / 不可写 / 路径本身是目录”都返回同一个
// "unable to open database file: out of memory (14)"，其中 "out of memory" 是
// errno 误导产物而非真实内存问题，因此这里不再透传该文案（AGENTS.md M0-13 要求）。
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
