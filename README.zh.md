# Engram

[![License: AGPL-3.0](https://img.shields.io/badge/License-AGPL--3.0-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go&logoColor=white)](go.mod)
[![Docker Image](https://img.shields.io/badge/Docker-nite07%2Fengram-2496ED?logo=docker&logoColor=white)](https://hub.docker.com/r/nite07/engram)
[![Algorithm](https://img.shields.io/badge/Algorithm-FSRS%20v6-brightgreen)](https://github.com/open-spaced-repetition/go-fsrs)

[English](README.md) | **中文**

Engram 是一个开源、自托管的渐进式间隔重复闪卡（Flashcard）服务。采用 Svelte 5 SPA 前端与 Go 后端构建，将现代自适应间隔排程算法 **FSRS v6** 与原生 **Model Context Protocol (MCP)** 服务结合，提供开箱即用的 AI 辅助制卡与自动化学习流。

线上 Demo：<https://engram.nite07.com/>（注册已关闭，可使用测试账号 `test` / `testdemo` 登录体验）

---

## 界面预览

| 卡组管理 | 卡片复习（题面） |
| :---: | :---: |
| ![卡组管理](screenshots/1.png) | ![卡片复习](screenshots/2.png) |
| **答案核对** | **作答反馈** |
| ![答案核对](screenshots/3.png) | ![作答反馈](screenshots/4.png) |
| **统计与洞察** | **卡片编辑与实时预览** |
| ![统计与洞察](screenshots/5.png) | ![卡片编辑](screenshots/6.png) |

---

## 功能特性

- **FSRS v6 间隔排程**：内置新一代开源算法 FSRS v6，相比传统 SM-2 记忆模型预测更精准、长期复习负担显著减轻；支持参数独立优化。
- **丰富卡片题型**：内置填空题（Cloze）、手打答案、容差数值题、单选题、多选题、判断题等。卡面支持 Markdown、MathJax 数学公式与媒体文件。
- **多用户与卡组共享**：支持 OIDC / 单点登录（SSO）。卡组可在用户间共享协作，各用户复习状态与进度严格隔离独立。
- **原生 AI & MCP 支持**：内置基于 Streamable HTTP 的 Model Context Protocol (MCP) 服务，为大语言模型与 Agent 提供全套查询、增删改查、复习与导出工具。
- **现代交互与 PWA**：基于 Svelte 5 与 Tailwind CSS 的轻量单页应用，提供深色/浅色主题、全键盘快捷操作、中英双语界面，并支持 PWA 离线运行。
- **邮件提醒与周报**：支持通过 SMTP 定时发送每日学习提醒邮件与每周学习总结周报。
- **多维度学习统计**：提供复习量趋势、未来到期卡片预测、遗忘与留存率分析等多维度统计面板。

---

## AI 与 MCP 集成

Engram 内置 Streamable HTTP MCP 服务（挂载于 `/mcp`），通过用户 API Key 统一鉴权。支持在 Claude Desktop、Cursor、Hermes 等兼容 MCP 协议的客户端中直接调用。

### 客户端配置示例

在 Web 端**设置**中生成 API Key 后，在客户端配置中加入 Engram：

```json
{
  "mcpServers": {
    "engram": {
      "url": "http://localhost:8080/mcp",
      "headers": {
        "Authorization": "Bearer YOUR_API_KEY"
      }
    }
  }
}
```

### 工具列表

Engram 暴露 9 个业务 MCP 工具：

- `list_decks` / `create_deck`：查看可访问卡组，或创建新卡组并指定调度预设。
- `search_notes` / `create_notes` / `update_note` / `delete_note`：卡片全文检索、批量建卡、就地更新及删除；支持 Dry-Run 校验。
- `get_due_cards` / `submit_review`：拉取今日待复习卡片、提交评分（`Again`、`Hard`、`Good`、`Easy`）并推动排程。
- `get_stats`：获取用户学习总览、各状态卡片数及留存率分析。
- `export_deck` / `import_deck`：卡组包（`.edeck`）导出与导入。

---

## 部署运行

### 快速体验（Docker Run）

使用单容器内置 SQLite 数据库，适合个人本地体验：

```bash
docker run -d --name engram -p 8080:8080 \
  -e SESSION_SECRET="${SESSION_SECRET:-$(openssl rand -base64 32)}" \
  -e ENCRYPTION_KEY="${ENCRYPTION_KEY:-$(openssl rand -base64 32)}" \
  -v engram-data:/data \
  docker.io/nite07/engram:latest
```

浏览器访问 `http://localhost:8080/`。全新实例首次访问会自动重定向至 `/setup` 初始化管理员账号。

### 生产部署（Docker Compose）

多用户生产环境建议配合 PostgreSQL 使用。

创建 `docker-compose.yaml`（或直接使用仓库中的 [docker-compose.yaml](./docker-compose.yaml)）：

```yaml
services:
  db:
    image: postgres:18
    restart: unless-stopped
    environment:
      POSTGRES_USER: engram
      POSTGRES_PASSWORD: engram_password
      POSTGRES_DB: engram
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U engram -d engram"]
      interval: 5s
      timeout: 3s
      retries: 20
    volumes:
      - pg-data:/var/lib/postgresql

  engram:
    image: nite07/engram:latest
    restart: unless-stopped
    depends_on:
      db:
        condition: service_healthy
    ports:
      - "8080:8080"
    environment:
      DB_DRIVER: postgres
      DB_DSN: "postgres://engram:engram_password@db:5432/engram?sslmode=disable"
      SESSION_SECRET: "${SESSION_SECRET:?run: openssl rand -base64 32}"
      ENCRYPTION_KEY: "${ENCRYPTION_KEY:?run: openssl rand -base64 32}"
      BASE_URL: "https://engram.example.com"
    volumes:
      - engram-media:/data/media

volumes:
  pg-data:
  engram-media:
```

启动服务：

```bash
SESSION_SECRET="$(openssl rand -base64 32)" \
ENCRYPTION_KEY="$(openssl rand -base64 32)" \
docker compose up -d
```

### 反向代理与 HTTPS

若在 Caddy、Nginx 或 Traefik 等反向代理后运行：

1. 设置 `BASE_URL` 为外部访问域名（如 `https://engram.example.com`）。
2. 设置 `TRUSTED_PROXIES` 为反代服务器的内网 IP 或 CIDR 网段（如 `127.0.0.1/32,::1/128`），确保客户端真实 IP 正确解析用于速率限制与审计日志。

---

## 环境变量速查

启动必需的基础环境变量：

| 变量名 | 说明 | 示例 / 默认值 |
| :--- | :--- | :--- |
| `HTTP_ADDR` | 服务监听地址与端口 | `127.0.0.1:8080`（容器内为 `0.0.0.0:8080`） |
| `BASE_URL` | 对外访问的基础 URL（用于链接与 OIDC 回调） | `https://engram.example.com` |
| `DB_DRIVER` | 数据库驱动（`sqlite` 或 `postgres`） | `postgres`（或 `sqlite`） |
| `DB_DSN` | 数据库连接串或 SQLite 文件路径 | `postgres://user:pass@localhost:5432/engram?sslmode=disable` |
| `SESSION_SECRET` | 会话 Cookie 加密密钥（32 字节 Base64） | 运行 `openssl rand -base64 32` 生成 |
| `ENCRYPTION_KEY` | 敏感数据落库加密主密钥（32 字节 Base64） | 运行 `openssl rand -base64 32` 生成 |
| `AUTO_MIGRATE` | 启动时是否自动执行表结构迁移（`1` 或 `0`） | `1` |
| `MEDIA_DIR` | 媒体附件存储目录 | `data/media` |

SMTP 邮件服务、注册开放策略、OIDC 登录提供方及存储配额等高级项，可在系统启动后直接在**管理后台**配置，无需重启服务。完整配置项与说明详见 [.env.example](./.env.example)。

---

## 参与开发

关于本地开发环境搭建、构建测试命令与代码规范，请参阅 [`CONTRIBUTING.md`](CONTRIBUTING.md)。

---

## 鸣谢

Engram 站在以下优秀开源项目的肩膀上：

- [go-fsrs](https://github.com/open-spaced-repetition/go-fsrs) —— FSRS v6 排程与参数优化。
- [fsrs-rs](https://github.com/open-spaced-repetition/fsrs-rs) —— 优化器适配器所用训练实现。
- [Gin](https://github.com/gin-gonic/gin) 与 [GORM](https://gorm.io) —— HTTP 与数据库层。
- [Svelte](https://svelte.dev/) —— 前端单页应用。
- [goldmark](https://github.com/yuin/goldmark) 与 [bluemonday](https://github.com/microcosm-cc/bluemonday) —— Markdown 渲染与 HTML 安全过滤。
- [modelcontextprotocol/go-sdk](https://github.com/modelcontextprotocol/go-sdk) —— 内置 MCP 服务端。
- [zitadel/oidc](https://github.com/zitadel/oidc) 与 [go-i18n](https://github.com/nicksnyder/go-i18n) —— OIDC 登录与国际化语言包。
- [MathJax](https://www.mathjax.org/) —— 数学公式渲染。
- [Tailwind CSS](https://tailwindcss.com/) —— 页面样式库。

友情链接：[LINUX DO](https://linux.do)。
