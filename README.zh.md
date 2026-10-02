# Engram

[English](README.md) | **中文**

Engram 是一个网页优先、多用户、自托管的间隔重复（SRS）服务。你可以在浏览器里手工写卡片，也可以让
外部脚本或 Agent 通过用户级 API Key 或内置 MCP server 把卡片推入。排程使用 FSRS v6。服务本身不解析
任何外部笔记系统。

## 你能得到什么

- 多用户账号，排程状态各人私有；卡组内容共享，含 owner、editor、reader 三种角色、分享链接与克隆。
- 十种内置题型，由注册表管理：cloze、typed、numeric、单选、多选、true/false 等。
- FSRS v6 排程，目标保留率（默认 0.90）、学习步骤、最大间隔与 fuzz 均可配。
- Markdown + TeX 渲染（自托管 MathJax 3），HTML 走白名单清洗；界面中英双语，文案由语言包驱动。
- PWA 外壳（可添加到主屏、独立窗口启动，只缓存静态资源）与卡组包（`.edeck`），后者用于备份、迁移
  与离线转交。
- REST API（`/api/v1`）、内置 MCP server，以及管理面板：用户、注册策略、OIDC、上传上限、审计、
  任务、健康。
- 参数优化：用你自己的复习历史重新训练 FSRS 参数，在预设页触发、后台作业执行，完成后可回退。

## 截图

<!-- 截图待补。 -->

## 快速开始

容器镜像默认用 SQLite、启动时自动迁移，并把所有数据放在 `/data` 下。

```bash
docker build -t engram:local .

docker run -d --name engram -p 8080:8080 \
  -e SESSION_SECRET="${SESSION_SECRET:-$(openssl rand -base64 32)}" \
  -e ENCRYPTION_KEY="${ENCRYPTION_KEY:-$(openssl rand -base64 32)}" \
  -v engram-data:/data \
  engram:local
```

打开 `http://localhost:8080/`。全新实例的首次访问会跳到 `/setup` 创建首个管理员账号。

## 上手四步

1. **创建管理员。** 首次访问落在 `/setup`，填邮箱与密码即可。
2. **建卡组。** 在卡组列表里新建一个卡组来放卡片。
3. **加卡片。** 在编辑器里手工写，或通过 API / MCP server 推入。
4. **开始复习。** 打开复习队列，逐张评分；排程会随你的作答调整。

## 用 PostgreSQL 部署

PostgreSQL 是默认部署库。用 `Dockerfile` 构建容器镜像，或直接运行二进制，环境变量相同。

```bash
docker run -d --name engram -p 8080:8080 \
  -e BASE_URL=https://engram.example.com/ \
  -e DB_DRIVER=postgres \
  -e DB_DSN="postgres://engram:CHANGE_ME@localhost:5432/engram?sslmode=disable" \
  -e SESSION_SECRET="${SESSION_SECRET:-$(openssl rand -base64 32)}" \
  -e ENCRYPTION_KEY="${ENCRYPTION_KEY:-$(openssl rand -base64 32)}" \
  -e AUTO_MIGRATE=0 \
  -v engram-media:/data/media \
  engram:local
```

生产环境 `BASE_URL` 必须用 `https://`：它的 scheme 决定会话 cookie 的 `Secure` 标志。

### 环境变量

服务启动时读取的全部变量。本表与 `.env.example` 一一对应。

| 变量                     | 必需 | 默认值                  | 含义                                                   |
| ------------------------ | ---- | ----------------------- | ------------------------------------------------------ |
| `HTTP_ADDR`              | 否   | `127.0.0.1:8080`        | 监听地址。                                             |
| `BASE_URL`               | 否   | `http://localhost:8080` | 对外 URL；其 scheme 决定会话 cookie 的 `Secure` 标志。 |
| `DB_DRIVER`              | 是   | —                       | `postgres` 或 `sqlite`。                               |
| `DB_DSN`                 | 是   | —                       | 连接串（PostgreSQL）或文件路径（SQLite）。             |
| `SESSION_SECRET`         | 是   | —                       | 会话签名密钥；用 `openssl rand -base64 32` 生成。      |
| `ENCRYPTION_KEY`         | 是   | —                       | 加密设置的主密钥；恰好 32 字节的 base64。              |
| `AUTO_MIGRATE`           | 否   | `0`                     | 启动时执行迁移（`1` / `true`）。                       |
| `BOOTSTRAP_ADMIN_EMAIL`  | 否   | —                       | 预填首个管理员的引导页表单。                           |
| `MEDIA_DIR`              | 否   | `data/media`            | 本地媒体目录。                                         |
| `MEDIA_MAX_BYTES`        | 否   | 系统设置，再回退 10 MiB | 单文件上传上限的覆盖值。                               |
| `MEDIA_USER_QUOTA_BYTES` | 否   | 系统设置，`0` = 不限    | 每用户媒体配额的覆盖值。                               |
| `MEDIA_ALLOWED_MIMES`    | 否   | 内置白名单              | 允许上传的 MIME 类型覆盖值。                           |
| `SMTP_HOST`              | 否   | —                       | SMTP 主机；留空即未配置。                              |
| `SMTP_PORT`              | 否   | `587`                   | SMTP 端口。                                            |
| `SMTP_USERNAME`          | 否   | —                       | SMTP 用户名。                                          |
| `SMTP_PASSWORD`          | 否   | —                       | SMTP 口令。                                            |
| `SMTP_FROM`              | 否   | —                       | 发件人地址。                                           |
| `SMTP_TLS_MODE`          | 否   | `starttls`              | `none`、`starttls` 或 `implicit`。                     |

`MEDIA_*` 与 `SMTP_*` 通常直接在管理面板里配，改完即时生效；环境变量只是覆盖这些值。

## 配置

- 启动必需项只有四个：`DB_DRIVER`、`DB_DSN`、`SESSION_SECRET`、`ENCRYPTION_KEY`；其中
  `ENCRYPTION_KEY` 必须是恰好 32 字节的 base64，否则服务直接退出。
- 注册策略、OIDC、上传上限与邮件都在管理面板里改，改完对下一个请求即生效，无需重启。

## 升级

在启动或滚动发布新版本之前，显式跑迁移：

```bash
engram schema sync
```

生产环境保持 `AUTO_MIGRATE=0`，这样迁移失败会表现为一条失败的命令，而不是一个半启动的服务。在容器
里，用同样的环境把它当一次性任务运行（`docker run --rm ... engram:local schema sync`）。

## 备份与恢复

同时备份媒体目录（`MEDIA_DIR`）：上传的字节放在那里，数据库只存它们的路径与哈希。

**PostgreSQL** —— `pg_dump -Fc -f engram.dump "$DB_DSN"`；恢复用
`pg_restore --clean --if-exists -d "$DB_DSN" engram.dump`（先停掉服务）。

**SQLite** —— `sqlite3 "$DB_DSN" "VACUUM INTO 'engram-backup.db'"` 在服务运行中也一致；恢复时停掉
服务并替换数据库文件（直接 `cp` 只在服务停止时可用）。

**媒体目录** —— `tar czf engram-media.tgz "$MEDIA_DIR"`，恢复时解到数据库备份旁边即可。媒体按
sha256 内容寻址，用旧快照覆盖不会破坏已有文件。

**卡组包** —— 任何用户都能在卡组页面导出的按卡组备份，或用 CLI：

```bash
engram export --deck 1 --package deck-1.edeck
engram import --package deck-1.edeck --user admin --dry-run
```

## API 与 MCP

服务是卡片库、排程器与接口。从资料出卡发生在服务之外，通过两条等价通道写进来：**REST API**，位于
`/api/v1`；以及**内置 MCP server**，挂在 `POST /mcp`（仅 HTTP，无 stdio）。两者都用用户级 API Key
认证，并调用同一套 service 方法，因此校验与排程规则不会漂移。Key 携带 scope（`read`、`write`、
`review`、`admin`），在浏览器的「设置」里管理。请求/响应 schema 在 [`schema/`](schema/)。

## 参与开发

见 [`CONTRIBUTING.md`](CONTRIBUTING.md)。

## 许可证

[AGPL-3.0](LICENSE)。
