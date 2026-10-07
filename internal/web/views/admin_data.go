package views

// 管理面板（M6-1、M6-5）的渲染数据。
// 与其它页面一致：所有用户可见文案由 handler 从语言包取好后传入，模板不含硬编码文字。

// AdminNavItem 是管理面板侧边导航的一项。
// Implemented 为 false 时模板渲染成置灰文本而不是链接：这些子页（用户管理、审计等）
// 在后续任务里实现前不能注册路由，否则点进去会 404 跳出面板。

type AdminRoleOption = SelectOption
