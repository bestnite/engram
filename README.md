# Engram

[![License: AGPL-3.0](https://img.shields.io/badge/License-AGPL--3.0-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go&logoColor=white)](go.mod)
[![Docker Image](https://img.shields.io/badge/Docker-nite07%2Fengram-2496ED?logo=docker&logoColor=white)](https://hub.docker.com/r/nite07/engram)
[![Algorithm](https://img.shields.io/badge/Algorithm-FSRS%20v6-brightgreen)](https://github.com/open-spaced-repetition/go-fsrs)

**English** | [中文](README.zh.md)

Engram is an open-source, self-hosted spaced repetition flashcard service. Built with a modern Svelte 5 SPA frontend and a Go backend, it pairs the state-of-the-art **FSRS v6** scheduling algorithm with a native **Model Context Protocol (MCP)** server for AI-assisted card creation and workflow automation.

Live demo: <https://engram.nite07.com/> (registration is closed; sign in with the test account `test` / `testdemo`)

---

## Screenshots

| Deck Management | Card Review (Question) |
| :---: | :---: |
| ![Deck Management](screenshots/1.png) | ![Card Review Question](screenshots/2.png) |
| **Answer Revealed** | **Instant Feedback** |
| ![Answer Revealed](screenshots/3.png) | ![Answer Feedback](screenshots/4.png) |
| **Study Statistics & Insights** | **Card Creation & Editor** |
| ![Study Statistics](screenshots/5.png) | ![Card Creation](screenshots/6.png) |

---

## Features

- **FSRS v6 Scheduling**: Adaptive scheduling based on the open-source FSRS v6 algorithm, optimizing review intervals and reducing workload compared to legacy SM-2 algorithms. Supports custom parameter optimization.
- **Rich Card Types**: Multiple question types built in — cloze, typed answer, numeric with tolerance, single-choice, multiple-choice, and true/false. Card bodies support Markdown, MathJax formulas, and media uploads.
- **Multi-user & Deck Sharing**: Multi-tenant architecture with OIDC / Single Sign-On (SSO) support. Decks can be shared across users while keeping each user's review scheduling and progress strictly isolated.
- **AI & MCP Native**: Built-in Model Context Protocol server exposing dedicated tools for LLMs and agents to search, create, update, review, and export flashcards.
- **Modern Interface & PWA**: Clean single-page application built with Svelte 5 and Tailwind CSS, featuring dark/light themes, keyboard navigation shortcuts, internationalization (`zh-CN` and `en`), and PWA support.
- **Email Reminders & Digest**: Automated daily review alerts and weekly summaries via SMTP.
- **In-depth Analytics**: Visual insights including retention rates by interval, forecast queues, review volume heatmaps, and difficulty distributions.

---

## Deck Library

Ready-to-import `.edeck` packages live in [`decks/`](./decks/). Pick one, then import it from the **Import** page (upload the file, or paste the file's public direct link), the CLI (`engram import`), or the MCP `import_deck` tool. See [`decks/README.md`](./decks/README.md) for the deck list and the step-by-step instructions.

---

## AI & MCP Integration

Engram exposes a built-in Streamable HTTP MCP server at `/mcp` authenticated by user API keys. Any MCP-compatible client or agent (such as Claude Desktop, Cursor, or Hermes) can interact with your flashcards directly.

### Configuration

Create an API key in the **Settings** panel, then add Engram to your MCP client configuration:

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

### Available Tools

Engram provides 17 dedicated MCP tools:

- `list_decks` / `create_deck` / `update_deck`: Inspect accessible decks, create new ones with custom scheduling presets, or rename and describe them.
- `list_card_types`: List the supported card types with each type's fields and a validated example note, so an agent can write notes without guessing field names or syntax.
- `search_notes` / `get_note` / `list_deck_tags`: Query notes with tag and keyword filters, read a single note, and list the tags used in a deck.
- `create_notes` / `update_note` / `delete_note` / `bulk_notes`: Create, update and delete notes across all supported card types, with dry-run validation, bulk insertion and bulk tag or delete actions.
- `get_due_cards` / `submit_review`: Fetch cards due for review and record reviews. Self-assessed cards take a rating (`Again`, `Hard`, `Good`, `Easy`), and a short-answer card may also carry the answer the learner wrote, stored with the review; answer-type cards (cloze, list, typed, numeric, choice, true/false) take the answer instead, and the server grades it.
- `get_stats`: Retrieve learning summaries, queue counts, and retention rates.
- `export_deck` / `import_deck`: Export and import portable deck packages (`.edeck`).
- `create_import_upload`: Get a single-use upload URL for a deck package. The agent sends the file to it with an HTTP client such as `curl`, so a large package never passes through the model and the API key never leaves the MCP client.

---

## Deployment

### Quick Start (Docker Run)

Uses an embedded SQLite database suitable for personal use or testing:

```bash
docker run -d --name engram -p 8080:8080 \
  -e SESSION_SECRET="${SESSION_SECRET:-$(openssl rand -base64 32)}" \
  -e ENCRYPTION_KEY="${ENCRYPTION_KEY:-$(openssl rand -base64 32)}" \
  -v engram-data:/data \
  docker.io/nite07/engram:latest
```

Open `http://localhost:8080/`. On a fresh instance, navigate to `/setup` to create the initial administrator account.

### Production (Docker Compose)

For multi-user deployments, PostgreSQL is recommended.

Save the following as `docker-compose.yaml` (or use the repository's [docker-compose.yaml](./docker-compose.yaml)):

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

Start the stack:

```bash
SESSION_SECRET="$(openssl rand -base64 32)" \
ENCRYPTION_KEY="$(openssl rand -base64 32)" \
docker compose up -d
```

### Reverse Proxy & HTTPS

When placing Engram behind a reverse proxy (such as Caddy, Nginx, or Traefik):

1. Set `BASE_URL` to your public URL (e.g., `https://engram.example.com`).
2. Set `TRUSTED_PROXIES` to the address your proxy connects **from**, as Engram sees it (comma-separated IPs or CIDRs). Login rate limiting, audit logs and new-device alerts use the client IP; until this is set, every request appears to come from the proxy.

`TRUSTED_PROXIES` is empty by default, so `X-Forwarded-For` and `X-Real-IP` are ignored. This is deliberate: a header is only as trustworthy as the hop that set it. List the proxies you run, never client networks or `0.0.0.0/0`.

The right value depends on how the proxy reaches Engram:

| Topology | Address Engram sees |
| :--- | :--- |
| Proxy and Engram on the same host, no containers | Loopback: `127.0.0.1/32,::1/128` |
| Engram in a container, port published to the host, proxy on the host | Usually the container network's **gateway**, not `127.0.0.1`: published ports are forwarded through the bridge. Find it with `docker network inspect <network>` (`Gateway`). |
| Proxy and Engram in the same container network | The proxy container's address in that network, or the whole network's subnet |
| CDN or load balancer → proxy → Engram | Every hop that appends to `X-Forwarded-For` must be listed, or the proxy must overwrite the header with the real client IP before forwarding |

To find the address without guessing, start Engram behind your proxy with `TRUSTED_PROXIES` unset and send one request. If the request carried a forwarded header from a loopback or private address that is not trusted, Engram logs one warning, `forwarded client ip header ignored ...`, and its `remote_ip` field is the address to add. At startup Engram also logs which proxies it trusts.

---

## Configuration

Core environment variables required for startup:

| Variable | Description | Example / Default |
| :--- | :--- | :--- |
| `HTTP_ADDR` | Listening address and port | `127.0.0.1:8080` (or `0.0.0.0:8080` in Docker) |
| `BASE_URL` | Public base URL used for links and OIDC callbacks | `https://engram.example.com` |
| `DB_DRIVER` | Database driver (`sqlite` or `postgres`) | `postgres` (or `sqlite`) |
| `DB_DSN` | Database connection string or SQLite file path | `postgres://user:pass@localhost:5432/engram?sslmode=disable` |
| `SESSION_SECRET` | 32-byte Base64 key for session cookies | Generated via `openssl rand -base64 32` |
| `ENCRYPTION_KEY` | 32-byte Base64 key for database encryption | Generated via `openssl rand -base64 32` |
| `AUTO_MIGRATE` | Automatically sync schema on startup (`1` or `0`) | `1` |
| `MEDIA_DIR` | Directory for uploaded media files | `data/media` |

Additional settings (SMTP email, registration policy, OIDC provider, upload quotas) can be configured directly in the **Admin Panel** without restarting the service. See [.env.example](./.env.example) for the complete list of overrides.

---

## Contributing

See [`CONTRIBUTING.md`](CONTRIBUTING.md) for local development setup, build instructions, and testing requirements.

---

## Acknowledgements

Engram builds on these projects:

- [go-fsrs](https://github.com/open-spaced-repetition/go-fsrs) — FSRS v6 scheduling and parameter optimisation.
- [fsrs-rs](https://github.com/open-spaced-repetition/fsrs-rs) — the training implementation behind the optimiser adapter.
- [Gin](https://github.com/gin-gonic/gin) and [GORM](https://gorm.io) — HTTP and database layers.
- [Svelte](https://svelte.dev/) — the single-page front end.
- [goldmark](https://github.com/yuin/goldmark) and [bluemonday](https://github.com/microcosm-cc/bluemonday) — Markdown rendering and HTML allowlisting.
- [modelcontextprotocol/go-sdk](https://github.com/modelcontextprotocol/go-sdk) — the MCP server.
- [zitadel/oidc](https://github.com/zitadel/oidc) and [go-i18n](https://github.com/nicksnyder/go-i18n) — OIDC sign-in and translation catalogs.
- [MathJax](https://www.mathjax.org/) — formula rendering.
- [Tailwind CSS](https://tailwindcss.com/) — styling.

Friend link: [LINUX DO](https://linux.do) — a Chinese-language tech community.
