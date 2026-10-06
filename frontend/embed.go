// Package frontend 承载前端 Svelte SPA 生产构建产物的嵌入（DESIGN.md §8.5、§10.2）。
package frontend

import (
	"embed"
	"fmt"
	"io/fs"
)

// DistFS 嵌入 Vite 生产构建产物（index.html 及 assets/）。
// 构建前必须先在 frontend/ 目录下执行 npm run build；若产物未构建，Go 编译将直接失败。
//
//go:embed all:dist
var DistFS embed.FS

// FS 返回以 dist 为根的文件系统，并校验必要产物（index.html）存在且非空。
func FS() (fs.FS, error) {
	return SubFS(DistFS)
}

// SubFS 从给定的 embed.FS 中提取 dist 子目录并校验 index.html（便于单元测试负例）。
func SubFS(efs embed.FS) (fs.FS, error) {
	sub, err := fs.Sub(efs, "dist")
	if err != nil {
		return nil, fmt.Errorf("frontend: open dist sub fs: %w", err)
	}
	data, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		return nil, fmt.Errorf("frontend: dist/index.html is missing: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("frontend: dist/index.html is empty; run 'npm run build' first")
	}
	return sub, nil
}
