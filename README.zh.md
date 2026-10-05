# Engram

[English](README.md) | **中文**

Engram 是一个自托管的间隔重复（SRS）服务，多用户，网页优先。卡片在浏览器里手写，也可以让脚本或
Agent 通过 API 或内置 MCP server 推入；排程用 FSRS v6。

## 截图

|                        |                        |
| ---------------------- | ---------------------- |
| ![](screenshots/1.png) | ![](screenshots/2.png) |
| ![](screenshots/3.png) | ![](screenshots/4.png) |
| ![](screenshots/5.png) | ![](screenshots/6.png) |

## 功能

### 排程与复习

排程用 FSRS v6。目标保留率默认 0.90，学习步骤、最大间隔、fuzz 都能调。攒够复习记录之后，还可以拿
自己的历史重新训练参数：在预设页触发，后台作业执行，跑完不满意能回退。

### 卡片与题型

十种题型内置：cloze、填空打字、数字、单选、多选、判断对错等。正文支持 Markdown 和 TeX（自托管
MathJax 3），HTML 走白名单清洗。上传的图片按内容寻址存放。

### 多人协作

每个人有自己的账号，排程状态互不干扰；卡组内容则是共享的。卡组有 owner、editor、reader 三种角色，
可以发分享链接，也可以克隆一份到自己名下。单个卡组能导出成 `.edeck` 包，用于备份、迁移或离线转交。

### 接口

REST API 挂在 `/api/v1`，内置 MCP server 挂在 `POST /mcp`（只有 HTTP，没有 stdio）。两条通道等价：
都用用户级 API Key 认证，都调用同一套 service 方法，所以校验和排程规则不会两边不一致。Key 带
scope（`read`、`write`、`review`、`admin`），在浏览器的「设置」里管理；请求与响应的 schema 见
[`schema/`](schema/)。

### 界面

中英双语，文案由语言包驱动。PWA 外壳可以添加到主屏、以独立窗口启动，缓存只涉及静态资源。

### 管理面板

用户、注册策略、OIDC、上传上限、审计日志、后台任务、健康检查，都在这里。

## 快速开始

容器镜像默认用 SQLite，启动时自动迁移，所有数据放在 `/data` 下。

```bash
docker run -d --name engram -p 8080:8080 \
  -e SESSION_SECRET="${SESSION_SECRET:-$(openssl rand -base64 32)}" \
  -e ENCRYPTION_KEY="${ENCRYPTION_KEY:-$(openssl rand -base64 32)}" \
  -v engram-data:/data \
  docker.io/nite07/engram:latest
```

打开 `http://localhost:8080/`。全新实例的首次访问会跳到 `/setup` 创建管理员账号。

## 上手四步

1. **创建管理员。** 首次访问 `/setup`，填邮箱和密码。
2. **建卡组。** 在卡组列表里新建一个，用来放卡片。
3. **加卡片。** 在编辑器里手写，或者通过 API / MCP server 推入。
4. **开始复习。** 打开复习队列逐张评分，排程会跟着你的作答调整。

## 用 PostgreSQL 部署

PostgreSQL 是默认部署库。发布镜像直接可用，也可以自己构建或直接跑二进制，环境变量一样。

```bash
docker run -d --name engram -p 8080:8080 \
  -e BASE_URL=https://engram.example.com/ \
  -e DB_DRIVER=postgres \
  -e DB_DSN="postgres://engram:CHANGE_ME@localhost:5432/engram?sslmode=disable" \
  -e SESSION_SECRET="${SESSION_SECRET:-$(openssl rand -base64 32)}" \
  -e ENCRYPTION_KEY="${ENCRYPTION_KEY:-$(openssl rand -base64 32)}" \
  -e AUTO_MIGRATE=0 \
  -v engram-media:/data/media \
  docker.io/nite07/engram:latest
```

生产环境 `BASE_URL` 必须用 `https://`：它的 scheme 决定会话 cookie 的 `Secure` 标志。

启动必需项只有四个：`DB_DRIVER`、`DB_DSN`、`SESSION_SECRET`、`ENCRYPTION_KEY`；其中
`ENCRYPTION_KEY` 必须是恰好 32 字节的 base64，否则服务直接退出。注册策略、OIDC、上传上限和邮件都在
管理面板里改，改完对下一个请求即生效，不用重启。

<details>
<summary>环境变量（服务启动时读取的全部变量，与 <code>.env.example</code> 一一对应）</summary>

| 变量                     | 必需 | 默认值                  | 含义                                                   |
| ------------------------ | ---- | ----------------------- | ------------------------------------------------------ |
| `HTTP_ADDR`              | 否   | `127.0.0.1:8080`        | 监听地址。                                             |
| `BASE_URL`               | 否   | `http://localhost:8080` | 对外 URL；其 scheme 决定会话 cookie 的 `Secure` 标志。 |
| `DB_DRIVER`              | 是   | —                       | `postgres` 或 `sqlite`。                               |
| `DB_DSN`                 | 是   | —                       | 连接串（PostgreSQL）或文件路径（SQLite）。             |
| `SESSION_SECRET`         | 是   | —                       | 会话签名密钥；用 `openssl rand -base64 32` 生成。      |
| `ENCRYPTION_KEY`         | 是   | —                       | 加密设置的主密钥；恰好 32 字节的 base64。              |
| `AUTO_MIGRATE`           | 否   | `1`                     | 启动时执行迁移（`1` / `true`）。                       |
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

</details>

## 路线图

以下均为计划项，尚未实现，不属于当前版本。带验收标准的任务级明细见
[`ROADMAP.md`](ROADMAP.md)。

- **LLM 辅助判分**：把服务指向一个 OpenAI 兼容的服务商（系统级共享 key，或每人自带 key；带总开关与
  每月调用上限），主观题自动判分——服务把题目、你的答案、参考答案与绑定的参考资料一起组装成提示词，
  再把模型的结论映射到惯用的 1–4 档评分。判分走异步，模型调用不会阻塞复习提交。
- **卡片绑定参考资料**：给一张卡片、或整个卡组绑定源文档，供判分引用；先用关键词检索，向量检索之后再说。
- **判分历史**：每张卡保留机器判分的记录，与你的自评并排展示，便于看出两者分歧。
- **不再需要外部优化器二进制**：等 `go-fsrs` 自带参数优化器发版后，Rust 辅助程序与它的构建步骤一并去掉，
  优化在进程内完成；作业、写回的权重与错误信息保持不变。
- **多个 OIDC 提供商**：同时配置多个提供商——公司的 IdP 加自己私人一个——登录页任选其一。
  当前的配置只能存一个提供商。

## 参与开发

见 [`CONTRIBUTING.md`](CONTRIBUTING.md)。

## 许可证

[AGPL-3.0](LICENSE)。
