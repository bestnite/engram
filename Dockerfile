# M0-11：多阶段构建，产出「一个静态服务二进制 + 一个优化器适配器」的镜像（ROADMAP.md M0-11）。
# 服务侧构建物只有一个二进制 + 可选 SQLite 文件；静态资源、语言包与前端产物都已 go:embed。
# 另带 FSRS 优化器适配器（tools/optimizer，Rust），可执行文件与主程序一起分发。
# 本文件不含任何私有 registry、主机名或部署细节，只使用公开官方镜像。
# syntax=docker/dockerfile:1

# ---- 优化器阶段：FSRS 参数优化适配器（tools/optimizer，Rust）----
# rust:alpine 自带 musl 工具链，cargo 产物静态链接、自包含，可直接放进 alpine 运行阶段；
# 本机此前手工构建的是 glibc 动态链接版本，进 alpine 会因缺动态链接器起不来。
FROM rust:1-alpine AS optimizer

RUN apk add --no-cache gcc musl-dev

WORKDIR /src/tools/optimizer
COPY tools/optimizer/ ./
# --locked 用仓库里的 Cargo.lock（权重是持久化数据，训练实现必须可复现）；
# 构建期就跑一次 --version，证明产物在这套运行时里可执行，而不是等到作业触发才发现。
RUN cargo build --release --locked \
    && ./target/release/optimizer --version

# ---- 前端阶段：Svelte 5 SPA 生产构建（Node glibc，锁定 package-lock.json）----
FROM node:22-bookworm-slim AS frontend-builder

WORKDIR /src/frontend
# 先只拷贝依赖清单，最大化层缓存
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci

# 拷贝前端源码并执行类型检查与生产构建
COPY frontend/ ./
RUN npm run check \
    && npm run build

# ---- 构建阶段：嵌入前端产物后编译 Go 二进制 ----
FROM golang:1.26-bookworm AS builder

ARG VERSION=dev

# ca-certificates 供 go mod download 走 HTTPS；样式由前端阶段的 Vite 产出，这里不需要
# 任何模板引擎或独立 CSS 工具链。
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src
# 先只拷贝依赖清单，最大化层缓存。
COPY go.mod go.sum ./
RUN go mod download
COPY . .

# 复制前端生产构建产物（frontend/dist）供 go:embed 嵌入
COPY --from=frontend-builder /src/frontend/dist ./frontend/dist

RUN CGO_ENABLED=0 go build -trimpath \
         -ldflags "-s -w -X main.version=${VERSION}" \
         -o /out/engram ./cmd/engram

# ---- 运行阶段：alpine + tzdata，非 root，最小可运行面 ----
FROM alpine:3.22

# tzdata 供用户时区/复习日切点使用；ca-certificates 供将来 OIDC 出站。
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -u 10001 -h /app app \
    && mkdir -p /data/media \
    && chown -R 10001:10001 /data

COPY --from=builder /out/engram /usr/local/bin/engram
# FSRS 优化器适配器：internal/jobs 默认在服务二进制旁解析 /usr/local/bin/optimizer，
# 权威路径对上就不需要 OPTIMIZER_PATH；产物是 musl 静态链接，可直接跑。
COPY --from=optimizer /src/tools/optimizer/target/release/optimizer /usr/local/bin/optimizer

USER 10001:10001
WORKDIR /app

# 默认单机形态：SQLite + 启动自动迁移。密钥是占位值，
# 真实部署必须通过环境变量覆盖，镜像内不承载任何真实凭据。
ENV HTTP_ADDR=0.0.0.0:8080 \
    DB_DRIVER=sqlite \
    DB_DSN=/data/engram.db \
    AUTO_MIGRATE=1 \
    MEDIA_DIR=/data/media \
    BASE_URL=http://localhost:8080

# SESSION_SECRET 与 ENCRYPTION_KEY 故意不提供默认值：镜像里烤一个已知密钥会让
# "忘记覆盖"变成静默的弱密钥部署；运行时缺失会以英文错误快速失败（internal/config 已校验）。

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/engram"]
CMD ["serve"]
