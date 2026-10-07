# Engram

[English](README.md) | **中文**

Engram 是一个自托管的闪卡服务。

线上 demo：<https://engram.nite07.com/>（注册已关闭，用测试账号 `test` / `testdemo` 登录）

## 截图

|                        |                        |
| ---------------------- | ---------------------- |
| ![](screenshots/1.png) | ![](screenshots/2.png) |
| ![](screenshots/3.png) | ![](screenshots/4.png) |
| ![](screenshots/5.png) | ![](screenshots/6.png) |

## 功能

- **算法**：使用优秀的开源算法 FSRS v6。
- **卡片**：内置多种题型——填空、手打答案、数值、单选、多选、判断等。卡面支持 Markdown、数学公式以及媒体文件。
- **多用户**：支持 OIDC。卡组可以共享，用户间独立学习进度。
- **AI**：内置 MCP server，为 Agent 提供增删改查工具。
- **界面**：中英双语以及 PWA 支持。
- **邮件**：定时每日学习提醒及总结周报。
- **统计**：详细的学习统计面板。

## 部署

### Docker Run

使用 Sqlite 数据库。

```bash
docker run -d --name engram -p 8080:8080 \
  -e SESSION_SECRET="${SESSION_SECRET:-$(openssl rand -base64 32)}" \
  -e ENCRYPTION_KEY="${ENCRYPTION_KEY:-$(openssl rand -base64 32)}" \
  -v engram-data:/data \
  docker.io/nite07/engram:latest
```

打开 `http://localhost:8080/`。全新实例的首次访问会跳到 `/setup` 创建管理员账号。

### Docker Compose

使用 Postgresql 数据库。

参考 [docker-compose.yaml](./docker-compose.yaml)。

## 环境变量

参考 [.env.example](./.env.example)。

## 参与开发

见 [`CONTRIBUTING.md`](CONTRIBUTING.md)。

## 感谢

Engram 站在这些项目上面：

- [go-fsrs](https://github.com/open-spaced-repetition/go-fsrs) —— FSRS v6 排程与参数优化。
- [fsrs-rs](https://github.com/open-spaced-repetition/fsrs-rs) —— 优化器适配器用的训练实现。
- [Gin](https://github.com/gin-gonic/gin) 与 [GORM](https://gorm.io) —— HTTP 与数据库层。
- [Svelte](https://svelte.dev/) —— 单页前端。
- [goldmark](https://github.com/yuin/goldmark) 与
  [bluemonday](https://github.com/microcosm-cc/bluemonday) —— Markdown 渲染与 HTML 白名单清洗。
- [modelcontextprotocol/go-sdk](https://github.com/modelcontextprotocol/go-sdk) —— MCP server。
- [zitadel/oidc](https://github.com/zitadel/oidc) 与
  [go-i18n](https://github.com/nicksnyder/go-i18n) —— OIDC 登录与语言包。
- [MathJax](https://www.mathjax.org/) —— 公式渲染。
- [Tailwind CSS](https://tailwindcss.com/) —— 样式。

友情链接：[LINUX DO](https://linux.do)。

## 许可证

[AGPL-3.0](LICENSE)。
