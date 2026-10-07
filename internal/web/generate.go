// 本文件承载 go:generate 指令（AGENTS.md §4 的构建步骤）。
//
// SPA 迁移完成后，服务端不再渲染页面：SSR 模板（templ）与它们使用的 Tailwind 样式表都已删除，
// 因此这里不再有生成步骤——前端产物由 `npm --prefix frontend run build` 产出并 go:embed 进二进制。
package web
