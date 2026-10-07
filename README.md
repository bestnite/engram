# Engram

**English** | [中文](README.zh.md)

Engram is a self-hosted flashcard service.

Live demo: <https://engram.nite07.com/> (registration is closed; sign in with the test account
`test` / `testdemo`)

## Screenshots

|                        |                        |
| ---------------------- | ---------------------- |
| ![](screenshots/1.png) | ![](screenshots/2.png) |
| ![](screenshots/3.png) | ![](screenshots/4.png) |
| ![](screenshots/5.png) | ![](screenshots/6.png) |

## Features

- **Algorithm**: the open-source FSRS v6.
- **Cards**: several card types built in — cloze, typed, numeric, single-choice, multiple-choice, true/false and more. Card bodies support Markdown, math formulas and media files.
- **Multi-user**: OIDC supported. Decks can be shared, and each user keeps their own progress.
- **AI**: a built-in MCP server that gives agents create/read/update/delete tools.
- **Interface**: Chinese and English, plus PWA support.
- **Email**: scheduled daily study reminders and a weekly digest.
- **Stats**: a detailed study statistics panel.

## Deployment

### Docker Run

Uses a SQLite database.

```bash
docker run -d --name engram -p 8080:8080 \
  -e SESSION_SECRET="${SESSION_SECRET:-$(openssl rand -base64 32)}" \
  -e ENCRYPTION_KEY="${ENCRYPTION_KEY:-$(openssl rand -base64 32)}" \
  -v engram-data:/data \
  docker.io/nite07/engram:latest
```

Open `http://localhost:8080/`. The first visit on a fresh instance goes to `/setup` to create the
admin account.

### Docker Compose

Uses a PostgreSQL database.

See [docker-compose.yaml](./docker-compose.yaml).

## Environment variables

See [.env.example](./.env.example).

## Contributing

See [`CONTRIBUTING.md`](CONTRIBUTING.md).

## Acknowledgements

Engram builds on these projects:

- [go-fsrs](https://github.com/open-spaced-repetition/go-fsrs) — FSRS v6 scheduling and parameter
  optimisation.
- [fsrs-rs](https://github.com/open-spaced-repetition/fsrs-rs) — the training implementation behind
  the optimiser adapter.
- [Gin](https://github.com/gin-gonic/gin) and [GORM](https://gorm.io) — HTTP and database layers.
- [Svelte](https://svelte.dev/) — the single-page front end.
- [goldmark](https://github.com/yuin/goldmark) and
  [bluemonday](https://github.com/microcosm-cc/bluemonday) — Markdown rendering and HTML
  allowlisting.
- [modelcontextprotocol/go-sdk](https://github.com/modelcontextprotocol/go-sdk) — the MCP server.
- [zitadel/oidc](https://github.com/zitadel/oidc) and
  [go-i18n](https://github.com/nicksnyder/go-i18n) — OIDC sign-in and translation catalogs.
- [MathJax](https://www.mathjax.org/) — formula rendering.
- [Tailwind CSS](https://tailwindcss.com/) — styling.

Friend link: [LINUX DO](https://linux.do) — a Chinese-language tech community.

## License

[AGPL-3.0](LICENSE).
