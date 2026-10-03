# JSON Schemas

Three JSON Schema (draft 2020-12) documents that describe the data exchanged with the
HTTP API, so external tools and agents can validate and generate payloads without
reading the server code. All of them are the machine-readable companion of `DESIGN.md`; the
server remains the source of truth for validation.

| File | Describes | Design reference |
|---|---|---|
| `note-import.schema.json` | Request body of the bulk note endpoint `POST /api/v1/decks/:id/notes` | `DESIGN.md` §7.3 (endpoint), §6.2 (card types) |
| `note-bulk.schema.json` | Request body of the notes bulk action endpoint `POST /api/v1/notes/bulk` | `DESIGN.md` §7.3 (endpoint) |
| `deck-package.schema.json` | Logical content of a `.edeck` deck package (export and import) | `DESIGN.md` §7.6 |

## `note-bulk.schema.json`

The request carries one `action` applied to every note in `note_ids`:

| `action` | `tags` | Effect |
|---|---|---|
| `delete` | not allowed | soft-deletes the notes; their cards and all users' progress are kept |
| `add_tags` | required | adds the given tags |
| `remove_tags` | required | removes the given tags |
| `set_tags` | required | replaces the whole tag list |

`note_ids` holds 1 to 500 entries and `tags` 1 to 20. Two kinds of failure are
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

Each note is discriminated by its `kind` field. The `fields` object holds the
type-specific values and is validated per kind through a `oneOf` whose branches pin
`kind` with a `const`; a `discriminator` annotation names `kind` for generators. The
built-in kinds and their required fields are:

| `kind` | Required `fields` | Notes |
|---|---|---|
| `basic` | `front`, `back` | one card |
| `basic_both` | `front`, `back` | two cards (forward and reverse) |
| `cloze` | `text` | `text` must contain at least one `{{cN::text}}` / `{{cN::text::hint}}`; one card per index |
| `list` | `prompt`, `items` | `items` has at least one entry; optional `ordered` |
| `typed` | `prompt`, `answer` | optional `accept`, `ignore_case`, `ignore_whitespace`, `regex` |
| `numeric` | `prompt`, `value` | optional `tolerance_absolute`, `tolerance_relative`, `unit` |
| `choice_single` | `question`, `options`, `answer` | `options` has at least two unique entries; `answer` is a zero-based index |
| `choice_multi` | `question`, `options`, `answers` | `options` has at least two unique entries; `answers` is a non-empty set of zero-based indices |
| `true_false` | `statement`, `answer` | `answer` is a boolean |
| `short_answer` | `prompt` | optional `reference`; self-graded for now |

Every note may also carry `external_ref` (the caller-defined idempotency key),
`note_id`, `tags`, `extra` and `source_url`.

`note_id` addresses an existing note of the deck in the path by primary key, so a
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

- A choice answer index must be smaller than the length of `options`.
  The schema only constrains it to a non-negative integer.
- `numeric` accepts an answer when either tolerance matches; both tolerances are
  optional and their combination is a runtime rule.

`unknown fields` inside a note object are allowed and ignored (see the version policy).

## `deck-package.schema.json`

A deck package is one zip archive with the extension `.edeck` (or a plain
`.edeck.json` when no media is included). The schema models the package as an object
whose properties are the package's entry names, and defines each file under `$defs`:

- `manifest.json` (required) — `format_version`, export time, application version,
  deck metadata, the `include_*` flags and the entry counts.
- `notes.json` (required) — the note content, same note shape as the import request.
- `cards.json` (required) — one entry per card: `note_index`, `template`, `ordinal`.
- `preset.json` (required) — scheduling parameters, including the 21 FSRS weights
  (`weights` is `null` when the library defaults are used).
- `progress.json` (optional) — the exporting user's own `card_states` and optional
  `reviews`.
- `media.json` (optional) — a map from `sha256` to `path`, `mime` and dimensions.
- `media/<sha256>.<ext>` entries — the media binaries themselves.

## Version policy

- `manifest.json` carries `format_version`, currently `1`. An importer must support at
  least `N-1`; this schema file describes version 1. When version 2 ships, add a branch
  (or a new schema file) and keep accepting version 1.
- Unknown fields inside any object are ignored, never fatal.
- Unknown extra entries in the package itself are ignored, so a newer package can still
  be read by an older importer.
- An unknown note `kind` is an error. It matches none of the `oneOf` branches, and the
  importer must list every offending entry instead of dropping it silently.
- `manifest.app_version` is informational and never used for compatibility decisions.

## Validating with check-jsonschema

Install-free run with `uvx`:

```bash
uvx check-jsonschema \
  --schemafile schema/note-import.schema.json \
  schema/examples/note-import.example.json

uvx check-jsonschema \
  --schemafile schema/deck-package.schema.json \
  schema/examples/deck-package.example.json
```

Both commands print `ok -- validation done` when the example is valid. To validate your
own payload, pass its path in place of the example file. Because the note shapes are
chosen with `oneOf`, add `--verbose` to make the offending field appear by name, for
example `$.notes[0].fields: 'value' is a required property`. If `uvx` is unavailable, a
syntax-only fallback is `python3 -m json.tool <file>`.

## Examples

- `examples/note-import.example.json` — a valid bulk import request exercising
  `basic`, `cloze`, `numeric`, `choice_multi` and `list`.
- `examples/deck-package.example.json` — a valid package with a manifest, two notes,
  their cards, a preset and one media entry.

Both examples are sanitised: they use `example.com`, a neutral `example-system` source
namespace and a placeholder `example-agent`. No real domain, host name, address,
credential or personal data appears in any file in this directory.
