# JSON Schemas

Two JSON Schema (draft 2020-12) documents that describe the data exchanged with the
HTTP API, so external tools and agents can validate and generate payloads without
reading the server code. They mirror the payloads the server accepts; the
server remains the source of truth for validation.

| File | Describes |
|---|---|
| `note-import.schema.json` | Request body of the bulk note endpoint `POST /api/v1/decks/:id/notes`: `note_type` plus `fields` as a map from field name to HTML string |
| `note-bulk.schema.json` | Request body of the notes bulk action endpoint `POST /api/v1/notes/bulk` |

## `note-bulk.schema.json`

The request carries one `action` applied to every note in `note_ids`:

| `action` | `tags` | Effect |
|---|---|---|
| `delete` | not allowed | soft-deletes the notes; their cards and all users' progress are kept |
| `add_tags` | required | adds the given tags |
| `remove_tags` | required | removes the given tags |
| `set_tags` | required | replaces the whole tag list |

`note_ids` holds 1 to 500 note **public ids** (opaque strings, not database keys) and `tags` 1 to 20.
Two kinds of failure are
deliberately different, which the schema states as far as JSON Schema can:

- **Request-level failure** — an unknown action, `note_ids` empty or over the bound, a tag
  action without tags, or `delete` carrying tags: the whole request is refused with the
  `invalid_request` code and nothing is written. The schema rejects these bodies.
- **Per-note outcome** — an id that does not exist, is soft-deleted, or belongs to a deck the
  caller may not edit: reported in the response's `skipped` array as `not_found` or
  `insufficient_role`, while the remaining notes are still processed. The schema cannot know
  which ids those are, so it accepts them.

The response is `{dry_run, affected, skipped:[{note_id, code}]}`. `affected` counts only the
notes whose stored state actually changed, so resending the same request reports
`affected: 0` — the action is idempotent. `tags` counts apply to the arrays as written while
the server applies them after trimming and deduplicating, so the schema is the stricter of
the two for bodies carrying repeated or blank entries.

## `note-import.schema.json`

The request carries a `notes` array (1 to 500 entries) plus the `dry_run` and
`on_conflict` options.

Each note names its note type with `note_type`, the opaque **public id** returned by
`GET /api/v1/decks/:id/note-types`, and sends `fields` as an object from field name to
HTML string. There is no per-kind shape any more: every field value is an HTML string,
including a numeric value (`"100"`) and a boolean (`"true"`), and list-like fields join
their items with `<br>`. The server maps each field name to the note type's field id and
rejects an unknown name with a per-row error that names it. A field the payload omits
becomes the empty string.

The request names the note type with `note_type` only. There is no `kind` key: the server has
no field for it, so a note that sends `kind` without `note_type` names no note type and the
import reports it as a per-row `note_type is required` error, and the schema rejects such a
body because `note_type` is required. Every note is imported into the note type `note_type`
names; nothing is resolved from a stock kind.

Every note may also carry `external_ref` (the caller-defined idempotency key),
`note_id`, `tags`, `extra` and `source_url`. `extra` and `source_url` are stored in the
note's own columns rather than as potential note type fields, unless the note type declares
a field of that name; the note write path decides which reading applies.

`note_id` addresses an existing note of the deck in the path by its opaque
**public id** (a string, never the numeric primary key), so a
single request can rewrite several existing notes in place; their cards and every
user's review progress are preserved. It is mutually exclusive with `external_ref`,
which names a note by its idempotency key: a note object that carries both is rejected
with a per-row error. The schema states this with an `allOf` branch whose `not` /
`required` combination forbids the two keys from appearing together. The server remains
the source of truth for everything the schema cannot express: a `note_id` that is
unknown, soft-deleted or owned by another deck is reported as a row error (the two
cases are not distinguished, so the server never leaks whether an id exists in another
deck), and the note is only rewritten when it belongs to the deck in the path.

What JSON Schema cannot express and the server enforces instead:

- A note type id must belong to the deck in the path and must exist; the schema only
  requires a non-empty string.
- A field name must be declared by the note type; the schema accepts any string key.
- `numeric` accepts an answer when either tolerance matches; both tolerances are
  optional and their combination is a runtime rule.

Unknown fields inside a note object are allowed and ignored.

## Validating with check-jsonschema

Install-free run with `uvx`:

```bash
uvx check-jsonschema \
  --schemafile schema/note-import.schema.json \
  schema/examples/note-import.example.json

uvx check-jsonschema \
  --schemafile schema/note-bulk.schema.json \
  schema/examples/note-bulk.example.json
```

Each command prints `ok -- validation done` when the example is valid. To validate your
own payload, pass its path in place of the example file. If `uvx` is unavailable, a
syntax-only fallback is `python3 -m json.tool <file>`.

## Examples

- `examples/note-import.example.json` — a valid bulk import request exercising five note
  types, with `fields` as a map from field name to HTML string.
- `examples/note-bulk.example.json` — a valid bulk tag action on three notes.

Both examples are sanitised: they use `example.com`, a neutral `example-system` source
namespace and a placeholder `example-agent`. No real domain, host name, address,
credential or personal data appears in any file in this directory.
