# Engram

[![License: AGPL-3.0](https://img.shields.io/badge/License-AGPL--3.0-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.27+-00ADD8?logo=go&logoColor=white)](go.mod)
[![Docker Image](https://img.shields.io/badge/Docker-nite07%2Fengram-2496ED?logo=docker&logoColor=white)](https://hub.docker.com/r/nite07/engram)
[![Algorithm](https://img.shields.io/badge/Algorithm-FSRS%20v6-brightgreen)](https://github.com/open-spaced-repetition/go-fsrs)

[English](README.md) | **中文**

Engram 是一个开源、自托管的渐进式间隔重复闪卡（Flashcard）服务。基于现代化 Svelte 5 SPA 前端与 Go 后端打造，将新一代自适应间隔排程算法 **FSRS v6** 与原生 **Model Context Protocol (MCP)** 服务结合，提供开箱即用的 AI 辅助制卡与自动化学习流。

线上 Demo：<https://engram.nite07.com/>（开放注册，亦可使用体验账号 `test` / `testdemo` 登录体验）

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

- **新一代 FSRS v6 间隔排程**：内置开源 FSRS v6 自适应记忆算法，相较传统 SM-2 模型遗忘预测更精准，大幅减轻长期复习压力；支持针对个人学习历史进行参数优化。
- **深度兼容 Anki 生态**：全面支持 Anki 风格的笔记类型、正反面卡片模板、自定义 CSS 样式、MathJax 数学公式与音视频/TTS 语法（`[sound:...]`、`[anki:tts]`）。无缝导入导出 `.apkg` 卡组包。
- **丰富的交互式题型**：除传统自评分数外，内置 8 种交互式题型控件（文本输入、数值、单选、多选、判断、列表排序、完形填空、简答），支持系统自动判分与即时反馈。
- **原生 AI & MCP 服务**：内置基于 Streamable HTTP 的 Model Context Protocol (MCP) 服务端，配合 Claude Desktop、Cursor 等任意 AI Agent 即可实现对话式制卡、卡片搜索与自动化复习。
- **多用户与卡组共享协作**：多租户架构，支持 OIDC / 单点登录（SSO）。支持将卡组共享给团队或好友，各用户的排程进度与复习历史完全独立隔离。
- **现代化交互与 PWA 支持**：采用 Svelte 5 与 Tailwind CSS 构建的高性能单页应用，支持深色/浅色模式、全键盘快捷键、中英双语国际化，支持 PWA 离线运行。
- **学习提醒与周期周报**：支持配置 SMTP 邮件服务，定时发送每日待复习提醒邮件与每周学习情况总结周报。
- **多维度学习统计洞察**：提供未来到期排程预测、留存率分析、复习热力图与卡片难度分布等数据可视化看板。

---

## 卡组导入与导出

Engram 原生支持 Anki `.apkg` 格式，无论是从 Anki 迁移还是日常备份都极为便捷。

支持通过 **Web 页面**（上传文件或粘贴公开下载链接）、**命令行 CLI**（`engram import`）以及 **MCP / AI Agent**（`import_deck`）导入。

### 导入支持内容
- **层级卡组树**：完整保留父子卡组层级关系（如 `语言::英语::词汇`）。
- **笔记类型与模板**：字段定义、卡片正反面模板、CSS 样式表及 MathJax / LaTeX 格式。
- **内容与多媒体**：笔记内容、标签、卡片以及包内内嵌的所有图片、音频与视频文件。
- **学习进度迁移（可选）**：支持选择性带入原卡片排程状态、复习历史日志与 FSRS 算法权重（`include_progress`）。
- **智能增量更新**：再次导入相同卡组时，按 Anki GUID 智能匹配并就地更新内容，不会产生重复卡片，亦不会覆盖之后更新的学习记录。

---

## AI 与 MCP 集成

Engram 内置 Streamable HTTP MCP 服务（位于 `/mcp`），通过用户 API Key 统一鉴权。支持在各大 LLM 客户端与智能体中直接调用。

### 客户端配置

在 Engram Web 端**设置 → API 密钥**生成密钥后，将其添加到你的 MCP 客户端配置中（以 Claude Desktop / Cursor 为例）：

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

### 工具能力概览
Engram 提供了覆盖完整学习工作流的 23 个 MCP 工具：
- **卡组与标签管理**：查看、新建、重命名、暂停或删除卡组，列出卡组标签（`list_decks`、`create_deck`、`update_deck`、`delete_deck`、`bulk_decks`、`list_deck_tags`）。
- **笔记与制卡**：智能体可查询笔记内容，或根据阅读资料批量生成闪卡，支持富文本 HTML 字段与自定义模板（`search_notes`、`get_note`、`create_notes`、`update_note`、`bulk_notes`）。
- **复习与智能判分**：获取待复习卡片、提交自评或作答内容由服务端自动判分、临时推迟卡片或追加当日新卡（`get_due_cards`、`submit_review`、`bury_card`、`add_extra_new_cards`）。
- **学习统计与洞察**：获取学习摘要、队列分布与留存率统计（`get_stats`）。
- **卡组包导入导出**：支持直接导入 `.apkg` 卡组，或通过一次性上传/下载链接处理大文件，避免大体积二进制数据占用 LLM 上下文（`import_deck`、`create_import_upload`、`create_export_download`）。

---

## 部署运行

### 快速体验（Docker Run）

使用单容器内置 SQLite 数据库，开箱即用，适合个人轻量使用与本地试用：

```bash
docker run -d --name engram -p 8080:8080 \
  -e SESSION_SECRET="${SESSION_SECRET:-$(openssl rand -base64 32)}" \
  -e ENCRYPTION_KEY="${ENCRYPTION_KEY:-$(openssl rand -base64 32)}" \
  -v engram-data:/data \
  docker.io/nite07/engram:latest
```

访问 `http://localhost:8080/`。首次启动会自动引导进入 `/setup` 页面创建系统初始管理员账号。

### 生产部署（Docker Compose + PostgreSQL）

生产环境与多用户部署推荐使用 PostgreSQL。

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

将 Engram 部署在反向代理（如 Caddy、Nginx、Traefik）后时：

1. **`BASE_URL`**：配置为外部实际访问地址（例如 `https://engram.example.com`），用于系统生成邮件直链与 OIDC 回调地址。
2. **`TRUSTED_PROXIES`**：配置为反向代理所在 IP 或 CIDR 网段（如本地代理 `127.0.0.1/32,::1/128`，或容器网桥网关 IP）。此配置确保服务能获取真实客户端 IP，用于登录安全限流、审计日志与新设备提醒。

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

Engram 站在以下优秀开源项目与社区的肩膀上：

- [Anki](https://apps.ankiweb.net/) —— 享誉全球的间隔重复闪卡先驱，Engram 的笔记类型、模板系统与卡组包格式设计深受其启发。
- [go-fsrs](https://github.com/open-spaced-repetition/go-fsrs) 与 [fsrs-rs](https://github.com/open-spaced-repetition/fsrs-rs) —— 业界领先的 FSRS v6 排程算法与参数优化引擎。
- [Gin](https://github.com/gin-gonic/gin) 与 [GORM](https://gorm.io) —— 高性能 Go HTTP 与 ORM 框架。
- [Svelte](https://svelte.dev/) 与 [Tailwind CSS](https://tailwindcss.com/) —— 现代化响应式前端单页应用与样式库。
- [modelcontextprotocol/go-sdk](https://github.com/modelcontextprotocol/go-sdk) —— Model Context Protocol (MCP) Go 语言服务端。
- [MathJax](https://www.mathjax.org/) —— 优质的数学公式渲染库。
- [goldmark](https://github.com/yuin/goldmark) —— 高效的 Markdown 解析引擎。
- [zitadel/oidc](https://github.com/zitadel/oidc) 与 [go-i18n](https://github.com/nicksnyder/go-i18n) —— OIDC 单点登录与国际化多语言支持。

友情链接：[LINUX DO](https://linux.do) —— 真诚、友善、团结、专业的技术社区。
