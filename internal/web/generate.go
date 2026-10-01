// 本文件只承载 go:generate 指令，把前端产物的生成步骤挂到 `go generate ./...` 上
// （AGENTS.md §4 的构建步骤）。生成物 tailwind.css 与 *_templ.go 同样已 gitignore。
package web

// 用 Tailwind CSS v4 的 standalone CLI，免 Node（DESIGN.md §8、§10.1）。
//
//go:generate tailwindcss -i ./static/css/input.css -o ./static/css/tailwind.css --minify
