# M0-11：多阶段构建，产出「一个静态二进制」的镜像（DESIGN.md §10.2、AGENTS.md §5 M0-11）。
# 构建物只有一个二进制 + 可选 SQLite 文件；模板、静态资源、语言包都已 go:embed。
# 本文件不含任何私有 registry、主机名或部署细节，只使用公开官方镜像。
# syntax=docker/dockerfile:1

# ---- 构建阶段：装 templ 与 Tailwind standalone CLI，先生成再编译 ----
# 用 glibc 基底（bookworm）：Tailwind 官方预编译的 tailwindcss-linux-x64 是 glibc 二进制，
# 在 musl 的 Alpine 里 exec 会报 “no such file or directory”（缺动态链接器）。
FROM golang:1.26-bookworm AS builder

# templ 版本与 go.mod 的 github.com/a-h/templ 对齐；Tailwind 与本机 standalone 对齐。
ARG TEMPL_VERSION=v0.3.1020
ARG TAILWIND_VERSION=v4.3.3
ARG VERSION=dev

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates wget \
    && rm -rf /var/lib/apt/lists/*
RUN go install github.com/a-h/templ/cmd/templ@${TEMPL_VERSION}
RUN wget -q -O /usr/local/bin/tailwindcss \
      https://github.com/tailwindlabs/tailwindcss/releases/download/${TAILWIND_VERSION}/tailwindcss-linux-x64 \
    && chmod +x /usr/local/bin/tailwindcss

WORKDIR /src
# 先只拷贝依赖清单，最大化层缓存。
COPY go.mod go.sum ./
RUN go mod download
COPY . .

# *_templ.go 与 tailwind.css 是 gitignore 的产物，必须在编译前生成。
RUN go generate ./... \
    && CGO_ENABLED=0 go build -trimpath \
         -ldflags "-s -w -X main.version=${VERSION}" \
         -o /out/engram ./cmd/engram

# ---- 运行阶段：alpine + tzdata，非 root，最小可运行面 ----
FROM alpine:3.22

# tzdata 供用户时区/复习日切点使用（DESIGN.md §10.4）；ca-certificates 供将来 OIDC 出站。
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -u 10001 -h /app app \
    && mkdir -p /data/media \
    && chown -R 10001:10001 /data

COPY --from=builder /out/engram /usr/local/bin/engram

USER 10001:10001
WORKDIR /app

# 默认单机形态：SQLite + 启动自动迁移（DESIGN.md §10.2）。密钥是占位值，
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
