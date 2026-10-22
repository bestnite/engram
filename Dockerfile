# M0-11: multi-stage build producing an image with "one static service binary + one optimizer adapter" (ROADMAP.md M0-11).
# The server-side build output is a single binary plus an optional SQLite file; static assets, language packs and frontend artifacts are all go:embed'ed.
# It also carries the FSRS optimizer adapter (tools/optimizer, Rust); the executable ships together with the main program.
# This file contains no private registry, host name or deployment detail and uses only public official images.
# syntax=docker/dockerfile:1

# ---- optimizer stage: FSRS parameter optimization adapter (tools/optimizer, Rust) ----
# rust:alpine ships the musl toolchain, so the cargo artifact is statically linked and self-contained and can go straight into the alpine runtime stage;
# the version built by hand on this machine before was a glibc dynamically linked one, which cannot start in alpine for lack of a dynamic loader.
FROM rust:1-alpine AS optimizer

RUN apk add --no-cache gcc musl-dev

WORKDIR /src/tools/optimizer
COPY tools/optimizer/ ./
# --locked uses the repository's Cargo.lock (weights are persisted data, so the training implementation must be reproducible);
# a --version run happens at build time to prove the artifact is executable in this runtime, instead of finding out when a job is triggered.
RUN cargo build --release --locked \
    && ./target/release/optimizer --version

# ---- frontend stage: Svelte 5 SPA production build (Node glibc, pinned to package-lock.json) ----
FROM node:26-bookworm-slim AS frontend-builder

WORKDIR /src/frontend
# Copy only the dependency manifests first to maximise layer caching, then install the npm version
# declared in package.json's packageManager (the image ships an older one) before running ci.
COPY frontend/package.json frontend/package-lock.json ./
RUN npm install -g npm@12.2.1 \
    && npm ci

# Copy the frontend sources and run type checking and the production build
COPY frontend/ ./
RUN npm run check \
    && npm run build

# ---- build stage: compile the Go binary after embedding the frontend artifacts ----
FROM golang:1.27-bookworm AS builder

ARG VERSION=dev

# ca-certificates is for go mod download over HTTPS; the styles are produced by Vite in the frontend stage, so no
# template engine or separate CSS toolchain is needed here.
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src
# Copy only the dependency manifests first to maximise layer caching.
COPY go.mod go.sum ./
RUN go mod download
COPY . .

# Copy the frontend production build artifacts (frontend/dist) for go:embed to embed
COPY --from=frontend-builder /src/frontend/dist ./frontend/dist

RUN CGO_ENABLED=0 go build -trimpath \
         -ldflags "-s -w -X main.version=${VERSION}" \
         -o /out/engram ./cmd/engram

# ---- runtime stage: alpine + tzdata, non-root, minimal runnable surface ----
FROM alpine:3.22

# tzdata is for user timezones/review-day cutovers; ca-certificates is for future OIDC egress.
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -u 10001 -h /app app \
    && mkdir -p /data/media \
    && chown -R 10001:10001 /data

COPY --from=builder /out/engram /usr/local/bin/engram
# FSRS optimizer adapter: internal/jobs resolves /usr/local/bin/optimizer next to the service binary by default,
# so when the canonical path matches, OPTIMIZER_PATH is unnecessary; the artifact is musl statically linked and runs as-is.
COPY --from=optimizer /src/tools/optimizer/target/release/optimizer /usr/local/bin/optimizer

USER 10001:10001
WORKDIR /app

# Default single-host shape: SQLite + automatic migration on startup. The secrets are placeholder values;
# a real deployment must override them via environment variables, and the image carries no real credentials.
ENV HTTP_ADDR=0.0.0.0:8080 \
    DB_DRIVER=sqlite \
    DB_DSN=/data/engram.db \
    AUTO_MIGRATE=1 \
    MEDIA_DIR=/data/media \
    BASE_URL=http://localhost:8080

# SESSION_SECRET and ENCRYPTION_KEY deliberately have no default values: baking a known secret into the image would turn
# "forgot to override" into a silent weak-key deployment; a missing value at runtime fails fast with an English error (internal/config validates it).

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/engram"]
CMD ["serve"]
