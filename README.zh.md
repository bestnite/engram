# Engram

[![License: AGPL-3.0](https://img.shields.io/badge/License-AGPL--3.0-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.27+-00ADD8?logo=go&logoColor=white)](go.mod)
[![Docker Image](https://img.shields.io/badge/Docker-nite07%2Fengram-2496ED?logo=docker&logoColor=white)](https://hub.docker.com/r/nite07/engram)
[![Algorithm](https://img.shields.io/badge/Algorithm-FSRS%20v6-brightgreen)](https://github.com/open-spaced-repetition/go-fsrs)

[English](README.md) | **中文**

Engram 是一个开源、自托管的渐进式间隔重复闪卡（Flashcard）服务。采用 Svelte 5 SPA 前端与 Go 后端构建，将现代自适应间隔排程算法 **FSRS v6** 与原生 **Model Context Protocol (MCP)** 服务结合，提供开箱即用的 AI 辅助制卡与自动化学习流。

线上 Demo：<https://engram.nite07.com/>（注册开放，也可使用测试账号 `test` / `testdemo` 登录体验）

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
- **兼容 Anki 的笔记类型**：笔记类型就是数据——字段、模板、CSS 与 Anki 的笔记类型选项——并内置八种已注册的答案控件（文本、数值、单选、多选、判断、列表、填空、简答）。字段值是 HTML，公式由 MathJax 渲染，音频/TTS 使用 Anki 语法（`[sound:<name>]`、`[anki:tts …]…[/anki:tts]`）。
- **Anki `.apkg` 卡组导入**：可直接导入 Anki 卡组包。支持三种 `.apkg` 结构（旧版 `collection.anki2`、`collection.anki21`，以及 zstd 压缩的 `collection.anki21b`），包内的卡组树、笔记类型、笔记、标签、卡片与媒体按 Anki 中的内容落地。再次导入同一个包时按 Anki 的 GUID 匹配笔记，因此只更新原卡组，不会产生副本，也不会改动已有进度。
- **多用户与卡组共享**：支持 OIDC / 单点登录（SSO）。卡组可在用户间共享协作，各用户复习状态与进度严格隔离独立。
- **原生 AI & MCP 支持**：内置基于 Streamable HTTP 的 Model Context Protocol (MCP) 服务，为大语言模型与 Agent 提供全套查询、增删改查、复习与导出工具。
- **现代交互与 PWA**：基于 Svelte 5 与 Tailwind CSS 的轻量单页应用，提供深色/浅色主题、全键盘快捷操作、中英双语界面，并支持 PWA 离线运行。
- **邮件提醒与周报**：支持通过 SMTP 定时发送每日学习提醒邮件与每周学习总结周报。
- **多维度学习统计**：提供复习量趋势、未来到期卡片预测、遗忘与留存率分析等多维度统计面板。

---

## 导入卡组

在**导入**页上传文件或粘贴文件的公开直链，或用 CLI（`engram import`）、MCP 的 `import_deck` 工具，都可以导入卡组。Anki 的 `.apkg` 是导入和导出使用的唯一卡组包格式。

卡组包不携带笔记的 `external_ref`、`extra` 或 `source_url`：Anki 在导入时丢弃 `notes.data`，因此这些值无法经过 Anki 导入导出往返而保留。

导入时需要选择目标：`new_deck` 新建卡组，`into_deck:<id>` 合并到你有编辑权限的卡组；CLI 与 MCP 工具还接受 `replace_deck:<id>` 整体替换现有卡组，该操作仅卡组所有者可用。

导入 `.apkg` 时，仅当 `preset_mode` 为 `new`（默认值）才读取包内的卡组配置，并且仅当同时设置 `apply_weights` 才采用包内的 FSRS 权重。当 `apply_weights` 已开启而包内权重被拒绝时，导入报告中的 `weights_invalid` 为 true。仅针对 `new_deck` 目标，`POST /api/v1/decks/import`、`POST /api/v1/decks/import-url`、`POST /api/v1/decks/import-uploads`、**导入**页、CLI 的 `--preset-mode` 和 `--preset-id` 标志以及 MCP 的 `import_deck` 和 `create_import_upload` 工具接受预设选择：`preset_mode` 为 `reuse` 时，新卡组指向 `preset_id` 指定的预设；为 `copy` 时，新卡组指向该预设的新副本。两种模式均不读取包内配置或权重，均拒绝 `apply_weights`；`reuse` 和 `copy` 对除 `new_deck` 以外的目标均被拒绝，而 `preset_id` 仅在模式为 `reuse` 或 `copy` 时被接受。`preset_id` 始终指定你自己的一个预设。学习进度仅在请求时带入：`include_progress`（默认关闭）将包内的卡片状态和复习日志应用到你自己的账户；未开启时，每张导入的卡片都从新卡开始。

### Anki `.apkg` 卡组包

包内以下内容会带入：

- **卡组树**：包内持有卡片的卡组共有的最长 `::` 名称前缀成为 Engram 卡组名；若它们没有共同前缀，则由卡片最多的分支命名卡组。更深层的卡组名保留为各张卡片的子卡组路径，例如 `A::B::C` 导入为卡组 `A::B`、路径 `C`。
- **笔记类型**：字段、模板（正反两面及其浏览器预览变体共四种）、CSS、LaTeX 前缀与后缀，以及 Anki 的笔记类型选项，均保持不变。
- **笔记、标签与卡片**：笔记按包内内容创建并保留标签，卡片按包内已有的卡片创建。
- **媒体**：图片、音频与视频按 Anki 中的文件名登记，并由包内的字段、模板与 CSS 引用。

- **调度状态与复习历史**：`include_progress` 默认关闭，开启后将包内的卡片状态和复习日志应用到你自己的账户；否则导入的卡片从新卡开始。
- **卡组配置与 FSRS 权重**：当 `preset_mode` 为 `new` 时，新卡组的预设采用包内卡组配置中受支持的设置。`apply_weights` 默认关闭；当目标为 `new_deck` 且 `preset_mode` 为 `new` 时，若包内的 FSRS 权重可以迁移，开启它即可采用这些权重。`preset_mode` 的 `reuse` 和 `copy` 模式拒绝 `apply_weights`。`into_deck` 和 `replace_deck` 目标接受但忽略 `apply_weights`，预设保持不变。导入报告中的 `weights_applied` 和 `weights_discarded` 字段记录结果。

导入的笔记类型没有答案控件，因此在笔记类型编辑器中为它添加答案控件之前，其卡片只能自评。

再次导入同一个包或它的较新导出版本时，按 Anki 的 GUID 匹配笔记、按包的来源 id 匹配笔记类型。字段与模板结构未变的笔记和笔记类型就地更新，并保留已有卡片与进度；结构发生变化时会新建笔记类型，并把笔记迁到新类型上。再次导入进度时，如果卡片在 Engram 中的最后复习时间不早于包中的最后复习时间，则保留其 Engram 状态。仅当包内的复习记录晚于导入者在 Engram 中对该卡片的最新复习记录时，才写入该记录。

导入报告会列出包中引用但未随包提供的内容：缺失的媒体文件、浏览器无法播放的视频、外部脚本或样式表，以及包中未内嵌的 LaTeX 图片。默认情况下缺失媒体会导致导入失败；勾选“媒体缺失时继续”即可照常导入。

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

Engram 暴露 23 个业务 MCP 工具：

- `list_decks` / `create_deck` / `update_deck`：查看可访问卡组、创建新卡组并指定调度预设，或修改卡组名称与描述。
- `delete_deck`：连同笔记、卡片和学习进度一起删除卡组。仅所有者可以执行，且删除后不可恢复。
- `bulk_decks`：一次删除多个卡组，逐行校验所有者；`dry_run` 只计数不写入。对应 REST `POST /decks/bulk`。
- `list_note_types` / `get_note_type`：列出一个卡组的 note 类型或读取其中一个，返回字段 id 与名称、模板和 CSS。note 的字段值是 HTML 字符串，按字段名索引，agent 不必猜请求体的形状。
- `search_notes` / `get_note` / `list_deck_tags`：按标签与关键词筛选笔记、读取单条笔记、列出卡组内用到的标签。
- `create_notes` / `update_note` / `delete_note` / `bulk_notes`：创建、更新和删除所有笔记类型的笔记，支持试运行校验、批量插入，以及批量打标签或删除。笔记用 `note_type` 指定类型，`fields` 按字段名提交 HTML 字符串。
- `get_due_cards` / `submit_review`：获取到期待复习的卡片并记录复习。自评卡片提交评分（`Again`、`Hard`、`Good`、`Easy`），简答卡片还可以附带学习者写下的作答，随复习记录保存；作答类卡片（填空、列表、输入作答、数值、选择、判断）提交作答，由服务端判分。
- `add_extra_new_cards`：学完每日新卡上限后，为某个卡组追加今天的新卡额度，只对当前复习日生效；卡组设置不变，第二天回到每日上限。
- `bury_card`：把一张卡推迟到你的下一个复习日开始时，不改动学习进度。对应 REST `POST /cards/{id}/bury`。
- `set_deck_paused`：只为自己暂停或恢复一个卡组。暂停的卡组不进入默认复习队列、待复习数和提醒邮件，但学习进度保留，指定这个卡组时仍可复习。
- `get_stats`：获取学习摘要、队列数量和留存率。
- `import_deck`：将 Anki `.apkg` 卡组包导入为新卡组、导入现有卡组或整体替换一个卡组。
- `create_export_download`：获取卡组包的一次性下载地址，使大压缩包无需经过模型。
- `create_import_upload`：为卡组包申请一次性上传地址，agent 用 `curl` 等 HTTP 客户端直接上传文件；大卡组不必经过模型输出，API Key 也不离开 MCP 客户端。

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
2. 设置 `TRUSTED_PROXIES` 为 Engram **实际看到的**反代连接来源地址（逗号分隔的 IP 或 CIDR）。登录限流、审计日志与新设备提醒都依赖客户端 IP；不设置时，所有请求看起来都来自反代本身。

`TRUSTED_PROXIES` 缺省为空，即忽略 `X-Forwarded-For` 与 `X-Real-IP`。这是有意的：请求头只和设置它的那一跳一样可信。只列出你自己运行的代理，不要列客户端网段，也不要写 `0.0.0.0/0`。

该填什么取决于反代如何连到 Engram：

| 拓扑 | Engram 看到的地址 |
| :--- | :--- |
| 反代与 Engram 在同一台主机，不用容器 | 回环地址：`127.0.0.1/32,::1/128` |
| Engram 在容器里、端口发布到宿主机，反代在宿主机上 | 通常是容器网络的**网关地址**，而不是 `127.0.0.1`：发布端口经网桥转发。用 `docker network inspect <网络名>` 查看 `Gateway` |
| 反代与 Engram 在同一个容器网络里 | 反代容器在该网络中的地址，或整个网络的子网 |
| CDN 或负载均衡 → 反代 → Engram | 每一个会追加 `X-Forwarded-For` 的跳都要列入；或者让反代在转发前用真实客户端 IP 覆盖该请求头 |

不想猜的话：先不设置 `TRUSTED_PROXIES`，在反代后启动 Engram 并发一个请求。若请求带着转发头、来源是未被信任的回环或私有地址，Engram 会记一条警告 `forwarded client ip header ignored ...`，其中 `remote_ip` 字段就是要填的地址。启动时 Engram 也会记录当前信任了哪些代理。

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
- [goldmark](https://github.com/yuin/goldmark) —— 一次性笔记迁移与邮件模板转换所用的 Markdown 渲染。卡片内容是 HTML，且只在沙箱 iframe 内显示。
- [modelcontextprotocol/go-sdk](https://github.com/modelcontextprotocol/go-sdk) —— 内置 MCP 服务端。
- [zitadel/oidc](https://github.com/zitadel/oidc) 与 [go-i18n](https://github.com/nicksnyder/go-i18n) —— OIDC 登录与国际化语言包。
- [MathJax](https://www.mathjax.org/) —— 数学公式渲染。
- [Tailwind CSS](https://tailwindcss.com/) —— 页面样式库。

友情链接：[LINUX DO](https://linux.do)。
