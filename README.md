# Engram

[![License: AGPL-3.0](https://img.shields.io/badge/License-AGPL--3.0-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.27+-00ADD8?logo=go&logoColor=white)](go.mod)
[![Docker Image](https://img.shields.io/badge/Docker-nite07%2Fengram-2496ED?logo=docker&logoColor=white)](https://hub.docker.com/r/nite07/engram)
[![Algorithm](https://img.shields.io/badge/Algorithm-FSRS%20v6-brightgreen)](https://github.com/open-spaced-repetition/go-fsrs)

**English** | [中文](README.zh.md)

Engram is an open-source, self-hosted spaced repetition flashcard service. Built with a modern Svelte 5 SPA frontend and a Go backend, it pairs the state-of-the-art **FSRS v6** scheduling algorithm with a native **Model Context Protocol (MCP)** server for AI-assisted card creation and workflow automation.

Live demo: <https://engram.nite07.com/> (registration is open; test account: `test` / `testdemo`)

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

- **FSRS v6 Spaced Repetition**: Powered by the modern open-source FSRS v6 algorithm. Provides superior retention modeling, optimizes review intervals, and dramatically cuts long-term study load compared to legacy SM-2. Supports personalized parameter optimization.
- **Deep Anki Compatibility**: Full support for Anki-style note types, templates, custom CSS, MathJax formulas, and audio/TTS (`[sound:...]`, `[anki:tts]`). Seamlessly imports and exports `.apkg` packages including nested subdecks, tags, cards, and media.
- **Interactive Answer Controls**: Beyond self-rating, Engram supports built-in interactive question types: text input, numeric, single choice, multiple choice, true/false, ordered list, cloze fill-in-the-blank, and short answer with instant automated grading.
- **Native AI & MCP Server**: Out-of-the-box Model Context Protocol (MCP) server. Connect Claude Desktop, Cursor, or any AI agent to automatically generate cards, search decks, study interactively, and manage reviews.
- **Multi-User & Deck Sharing**: Multi-tenant architecture with OIDC / Single Sign-On (SSO) support. Share decks with team members or friends while keeping each learner's scheduling and review history strictly isolated.
- **Modern Interface & PWA**: Responsive single-page application built with Svelte 5 and Tailwind CSS. Features dark/light modes, full keyboard shortcuts, internationalization (`zh-CN` and `en`), and PWA support for mobile and offline use.
- **Study Reminders & Digest**: Automated daily review notifications and weekly summary digests via SMTP email.
- **Visual Analytics**: Interactive insights including retention rates by interval, forecast queues, review volume heatmaps, and difficulty distributions.

---

## Importing & Exporting Decks

Engram natively supports Anki `.apkg` deck packages for seamless migration and backup.

You can import packages via the **Web UI** (upload file or paste a public URL), the **CLI** (`engram import`), or through **MCP / AI agents** (`import_deck`).

### What Gets Imported
- **Decks & Subdecks**: Preserves full deck hierarchy (e.g. `Language::Grammar`).
- **Note Types & Templates**: Fields, card templates (front/back), styling (CSS), and MathJax / LaTeX formatting.
- **Content & Media**: Notes, tags, cards, and all bundled media (images, audio, video).
- **Optional Study History**: Optionally import your existing scheduling states, review history logs, and FSRS weights (`include_progress`).
- **Smart In-Place Updates**: Re-importing a deck matches notes by Anki GUIDs, updating content in place without creating duplicates or overwriting newer review progress.

---

## AI & MCP Integration

Engram includes a built-in Streamable HTTP MCP server at `/mcp`, secured with user API keys. Any MCP-compatible agent or client can seamlessly connect to your flashcard collection.

### Connecting Your AI Client

Generate an API key in **Settings → API Keys**, then add Engram to your client configuration (e.g., Claude Desktop, Cursor):

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

### Capabilities
Engram exposes a comprehensive suite of MCP tools designed for LLM workflows:
- **Deck & Tag Management**: Browse, create, update, or delete decks (`list_decks`, `create_deck`, `update_deck`, `delete_deck`, `bulk_decks`, `list_deck_tags`).
- **Note & Card Authoring**: Search and inspect notes, or batch-create flashcards from reading materials using flexible templates (`search_notes`, `get_note`, `create_notes`, `update_note`, `bulk_notes`).
- **Study & Automated Grading**: Fetch due cards, submit self-evaluations or answers for automated grading, postpone cards, or adjust daily limits (`get_due_cards`, `submit_review`, `bury_card`, `add_extra_new_cards`).
- **Statistics & Insights**: Query study summaries, due queues, and retention rates (`get_stats`).
- **Package Import & Export**: Import Anki packages or generate one-time download/upload tickets for large archives without saturating LLM context windows (`import_deck`, `create_import_upload`, `create_export_download`).

---

## Deployment

### Quick Start (Docker Run)

Run a standalone instance with embedded SQLite storage (ideal for personal use):

```bash
docker run -d --name engram -p 8080:8080 \
  -e SESSION_SECRET="${SESSION_SECRET:-$(openssl rand -base64 32)}" \
  -e ENCRYPTION_KEY="${ENCRYPTION_KEY:-$(openssl rand -base64 32)}" \
  -v engram-data:/data \
  docker.io/nite07/engram:latest
```

Visit `http://localhost:8080/`. On initial launch, complete the quick setup wizard at `/setup` to create the administrator account.

### Production Setup (Docker Compose with PostgreSQL)

For multi-user or production setups, PostgreSQL is recommended.

Download or create `docker-compose.yaml` (see [docker-compose.yaml](./docker-compose.yaml)):

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

Start the containers:

```bash
SESSION_SECRET="$(openssl rand -base64 32)" \
ENCRYPTION_KEY="$(openssl rand -base64 32)" \
docker compose up -d
```

### Reverse Proxy & HTTPS

When deploying behind a reverse proxy (such as Caddy, Nginx, or Traefik):

1. **`BASE_URL`**: Set to your external address (e.g. `https://engram.example.com`). Used for email links and OIDC redirects.
2. **`TRUSTED_PROXIES`**: Set to the IP address or CIDR range of your proxy (e.g. `127.0.0.1/32,::1/128` for a local proxy, or the container gateway IP in Docker). This ensures client IP addresses are correctly identified for rate limiting, audit logging, and security alerts.

---

## Configuration

Core environment variables:

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

Most runtime configurations (SMTP mail server, registration policy, OIDC SSO providers, storage quotas) can be configured directly through the **Admin Panel** without restarting the service. See [.env.example](./.env.example) for all available options.

---

## Contributing

See [`CONTRIBUTING.md`](CONTRIBUTING.md) for the complete local start command, build instructions, and testing guidelines.

---

## Acknowledgements

Engram is built upon and inspired by these great open-source projects:

- [Anki](https://apps.ankiweb.net/) — The gold standard for spaced repetition; its note types, templates, and package ecosystem inspired Engram's compatibility design.
- [go-fsrs](https://github.com/open-spaced-repetition/go-fsrs) & [fsrs-rs](https://github.com/open-spaced-repetition/fsrs-rs) — The cutting-edge FSRS v6 algorithm and parameter optimization engine.
- [Gin](https://github.com/gin-gonic/gin) & [GORM](https://gorm.io) — Fast, ergonomic Go HTTP and ORM framework.
- [Svelte](https://svelte.dev/) & [Tailwind CSS](https://tailwindcss.com/) — Modern, responsive SPA front end and styling.
- [modelcontextprotocol/go-sdk](https://github.com/modelcontextprotocol/go-sdk) — Model Context Protocol (MCP) server implementation.
- [MathJax](https://www.mathjax.org/) — Beautiful mathematical formula rendering.
- [goldmark](https://github.com/yuin/goldmark) — High-performance Markdown parser.
- [zitadel/oidc](https://github.com/zitadel/oidc) & [go-i18n](https://github.com/nicksnyder/go-i18n) — OIDC authentication and internationalization catalogs.

Friend link: [LINUX DO](https://linux.do) — A vibrant tech community.
