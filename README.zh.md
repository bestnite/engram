# Engram

[English](README.md) | **中文**

网页优先、多用户、自托管的间隔重复（SRS）记忆服务。卡片可在浏览器里手工创建，也可由外部系统或
Agent 通过用户级 API Key 或内置 MCP server 写入。排程使用 FSRS v6。服务本身不解析任何外部笔记
系统。

> 项目名已定为 **Engram**，模块路径为 `example.com/engram`；许可证仍是占位符。见 `AGENTS.md`
> 的 B-2 以及 `DESIGN.md` §13。

## 它不是什么

- **不兼容 Anki。** 它不读也不写 `.apkg` / `.colpkg`，也没有任何笔记系统的导入器。
- **不是离线应用。** 复习需要联网。PWA 只缓存静态外壳，设备上不保存任何答题数据。
- **不是协作编辑器。** 卡组可以共享（只读、读写、可随时撤销），但不会出现两个人同时编辑同一份
  卡组内容。
- **不是托管服务。** 不依赖第三方，不做遥测，也没有公开题库市场。

## 特性

- 多用户账号，进度各人独立。卡组内容共享，每人的排程状态私有。
- 卡组共享三种角色——owner、editor（读写）、reader（只读）——外加分享链接与克隆（fork）。撤销
  在下一个请求即生效。
- 十种内置题型，由注册表管理：`basic`、`basic_both`、`cloze`、`list`（自评）；
  `typed`、`numeric`、`choice_single`、`choice_multi`、`true_false`（机器判分）；以及
  `short_answer`（当前为自评）。新增题型 = 新增一个实现文件并注册，核心代码不改。
- FSRS v6 排程，目标保留率（默认 0.90）、学习步骤、最大间隔与 fuzz 均可配。
- Markdown + TeX 渲染（MathJax 3，自托管），HTML 走白名单清洗。
- 界面中英双语，全部文案由语言包驱动。
- PWA 外壳：可添加到主屏、独立窗口启动，只缓存静态资源。
- 卡组包（`.edeck`）：自包含的导出/导入格式，用于备份、迁移与离线转交。
- 参数优化由 web 触发、子进程执行。
- 面向外部 Agent 的 REST API（`/api/v1`）与内置 MCP server（仅 HTTP）。
- 浏览器里的管理面板：用户、注册策略、OIDC、上传上限、审计、任务、健康。

## 快速开始（SQLite，开发模式）

需要 Go 1.26 或更新版本。SQLite 是开发模式，不需要外部数据库。

```bash
# 1. 生成 templ 代码与 Tailwind CSS 产物。两者都是 gitignore 的，
#    全新检出的仓库不做这一步就无法编译。
go generate ./...

# 2. 创建存放 SQLite 文件与媒体文件的数据目录。
#    它在 gitignore 里，服务不会替你创建。
mkdir -p data

# 3. 配置环境。ENCRYPTION_KEY 必须是恰好 32 字节的 base64；
#    给任何其它值都会让服务在启动时直接退出。
export DB_DRIVER=sqlite
export DB_DSN=data/engram.db
export AUTO_MIGRATE=1
export SESSION_SECRET="${SESSION_SECRET:-$(openssl rand -base64 32)}"
export ENCRYPTION_KEY="${ENCRYPTION_KEY:-$(openssl rand -base64 32)}"

# 4. 启动服务（默认子命令就是 serve）。
go run ./cmd/engram serve
```

然后打开 `http://localhost:8080/`。全新实例的首次访问会跳到 `/setup` 创建首个管理员账号。
`HTTP_ADDR` 默认 `127.0.0.1:8080`，`BASE_URL` 默认 `http://localhost:8080`。

确认服务已就绪，并打印版本：

```bash
curl -fsS http://localhost:8080/healthz
# {"status":"ok","database":"ok","schema_version":0}

go run ./cmd/engram version
# dev
```

若 `ENCRYPTION_KEY` 不是 32 字节的 base64，启动会大声失败，例如：

```
level=ERROR msg=fatal error="invalid secret master key: ENCRYPTION_KEY must be base64 of 32 bytes (openssl rand -base64 32)"
```

## 用 PostgreSQL 部署

PostgreSQL 是默认部署库。可以用 `Containerfile` 构建容器镜像，也可以直接运行二进制，环境变量相同。

### 环境变量

服务启动时读取的全部变量。本表与 `.env.example` 一一对应。

| 变量 | 必需 | 默认值 | 含义 |
|---|---|---|---|
| `HTTP_ADDR` | 否 | `127.0.0.1:8080` | 监听地址。 |
| `BASE_URL` | 否 | `http://localhost:8080` | 对外 URL。其 scheme 决定会话 cookie 的 `Secure` 标志，因此生产必须是 `https://` 的 URL。 |
| `DB_DRIVER` | 是 | — | `postgres` 或 `sqlite`。 |
| `DB_DSN` | 是 | — | 连接串（PostgreSQL）或文件路径（SQLite）。 |
| `SESSION_SECRET` | 是 | — | 会话签名密钥；用 `openssl rand -base64 32` 生成。 |
| `ENCRYPTION_KEY` | 是 | — | 加密设置的主密钥；必须是恰好 32 字节的 base64（`openssl rand -base64 32`）。其它值会让启动失败。 |
| `AUTO_MIGRATE` | 否 | `0` | 启动时执行迁移（`1` / `true`）。 |
| `BOOTSTRAP_ADMIN_EMAIL` | 否 | — | 预填首个管理员的引导页表单。 |
| `MEDIA_DIR` | 否 | `data/media` | 本地媒体目录。 |
| `MEDIA_MAX_BYTES` | 否 | 系统设置 `media_max_bytes`，再回退 10 MiB | 单文件上传上限的覆盖值。 |

容器镜像示例（`DB_DSN` 里的 `localhost` 是占位值，请指向你自己的数据库主机）：

```bash
docker build -t engram:local .

export SESSION_SECRET="${SESSION_SECRET:-$(openssl rand -base64 32)}"
export ENCRYPTION_KEY="${ENCRYPTION_KEY:-$(openssl rand -base64 32)}"

docker run -d --name engram \
  -p 8080:8080 \
  -e HTTP_ADDR=0.0.0.0:8080 \
  -e BASE_URL=https://engram.example.com/ \
  -e DB_DRIVER=postgres \
  -e DB_DSN="postgres://engram:CHANGE_ME@localhost:5432/engram?sslmode=disable" \
  -e SESSION_SECRET="${SESSION_SECRET}" \
  -e ENCRYPTION_KEY="${ENCRYPTION_KEY}" \
  -e AUTO_MIGRATE=0 \
  -v engram-media:/data/media \
  engram:local serve
```

### 启动时的迁移

`AUTO_MIGRATE=1` 会在进程启动时执行 AutoMigrate 以及已登记的破坏性迁移。单节点部署用起来方便，开发
快速开始用的也是它。

生产环境建议保持 `AUTO_MIGRATE=0`，在启动或滚动发布新版本之前显式跑迁移：

```bash
engram schema sync
```

它执行同一套迁移并记录结果 schema 版本，这样迁移失败会表现为一条失败的命令，而不是一个半启动的服务。
在容器里，用同样的环境把它当一次性任务运行（例如 `docker run --rm ... engram:local schema sync`）。

## 备份与恢复

有两个数据库、两套流程。两种情况下都要同时备份媒体目录（`MEDIA_DIR`，默认 `data/media`）：上传的
字节放在那里，数据库只存它们的路径与哈希。

### PostgreSQL

备份（custom 格式，已压缩）：

```bash
pg_dump -Fc -f engram.dump "$DB_DSN"
```

恢复到全新或已有数据库：

```bash
pg_restore --clean --if-exists -d "$DB_DSN" engram.dump
```

恢复期间请停掉服务。`DB_DSN` 就是服务使用的同一个连接串。

### SQLite

用 SQLite 自带的在线备份，服务运行中也一致：

```bash
sqlite3 "$DB_DSN" "VACUUM INTO 'engram-backup.db'"
```

恢复：

```bash
# 1. 停掉服务。
# 2. 替换数据库文件（服务必须不在运行）。
cp engram-backup.db "$DB_DSN"
# 3. 重新启动服务。
```

直接 `cp` 文件也可以，但只能在服务停止时做。服务运行中要用 `VACUUM INTO` 才安全。

### 媒体目录

```bash
tar czf engram-media.tgz "$MEDIA_DIR"
```

恢复时把归档解到数据库备份旁边即可。媒体按 sha256 内容寻址，所以用旧快照覆盖较新的目录只会补文件，
不会破坏已有文件。

### 卡组包

若需要非管理员也能在浏览器里做的按卡组备份，用卡组包：在卡组页面导出，或用 CLI（需要与服务相同的
环境）：

```bash
engram export --deck 1 --package deck-1.edeck
engram import --package deck-1.edeck --dry-run
```

## 对外集成（REST API 与 MCP）

服务是卡片库、排程器与接口。从资料出卡发生在服务之外——脚本、Agent 或任何其它系统——通过下面两条
等价通道写进来：

- **REST API**，位于 `/api/v1`，用用户级 API Key 认证（`Authorization: Bearer fcard_...`）。
- **内置 MCP server**，挂在 `POST /mcp`（仅 HTTP——无 stdio），用同一把 API Key 认证。

两者调用同一套 service 方法，因此校验与排程规则不会漂移。Key 携带 scope（`read`、`write`、
`review`、`admin`）；MCP 握手只暴露该 key 的 scope 允许的工具。在浏览器的「设置」里创建与管理
key，明文只在创建时显示一次。

典型链路完全在服务之外：Agent 读资料、生成问答对、用 `dry_run` 预演、正式写入，人再在网页上复习。
服务只负责诚实记账。

```bash
curl -fsS https://engram.example.com/api/v1/decks \
  -H "Authorization: Bearer $FLCARD_KEY"
```

批量导入与卡组包的请求/响应 schema 在 `schema/`（`note-import.schema.json`、
`deck-package.schema.json`）。

## 开发

构建、vet、格式化与测试（AGENTS.md §4）：

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./...
```

生成代码与产物是 gitignore 的，编译前必须重新生成：

```bash
templ generate
tailwindcss -i ./internal/web/static/css/input.css \
            -o ./internal/web/static/css/tailwind.css --minify
```

`go generate ./...` 会一次跑完上面两步。

用 SQLite 本地运行：

```bash
mkdir -p data
DB_DRIVER=sqlite DB_DSN=data/engram.db AUTO_MIGRATE=1 \
SESSION_SECRET="${SESSION_SECRET:-$(openssl rand -base64 32)}" \
ENCRYPTION_KEY="${ENCRYPTION_KEY:-$(openssl rand -base64 32)}" \
go run ./cmd/engram serve
```

仓库检查（要在 `git add` 之后跑，因为它们只扫已跟踪文件）：

```bash
bash scripts/checks/no-private-data.sh
bash scripts/checks/no-template-literals.sh
```

Tailwind standalone CLI 是 glibc 二进制，所以容器构建阶段必须用 glibc 基底，不能用 Alpine。

## 许可证与项目名

项目名与模块路径已拍板；许可证仍是占位符：

- 项目名与模块路径：`Engram` / `example.com/engram`。
- 许可证：尚未选定（MIT / Apache-2.0 / AGPL-3.0）。`LICENSE` 目前是占位内容
  （AGENTS.md B-2、DESIGN.md §13 #2）。