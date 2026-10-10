# Deck Library

**English** | [中文](README.zh.md)

Ready-to-import `.edeck` deck packages. Each file is self-contained and can be imported
into any Engram instance through the web UI, the CLI or MCP. A package carries deck
content only — the exporter's review progress and history stay behind unless the export
explicitly included them.

## Decks

| File | Deck | Content | Notes | Cards | Tags |
| :--- | :--- | :--- | ---: | ---: | :--- |
| [`百化分.edeck`](./百化分.edeck) | 百化分 | 资料分析 percentage ↔ fraction drills, 36 prompts each way (`50% = 1/？` and `1/2.8 ≈ ？（百分数）`) | 72 | 72 | `百化分` |
| [`平方数与常用幂.edeck`](./平方数与常用幂.edeck) | 平方数与常用幂 | Speed-arithmetic recall: perfect squares 11²–30², the growth powers \((1+r)^{n}\) for r = 1–10% and 15–50%, and five common square roots | 89 | 89 | `平方数` `常用幂` `开方` |
| [`资料分析公式.edeck`](./资料分析公式.edeck) | 资料分析公式 | Data-analysis formulas as cloze cards, plus single-choice drills on which expression a given set of conditions calls for: growth, share, average, multiple, contribution rate, units | 70 | 70 | `增长` `比重` `平均数` `倍数` `概念` `单位` |
| [`花生十三高频成语 1000 词.edeck`](./花生十三高频成语%201000%20词.edeck) | 花生十三高频成语 1000 词 | 993 high-frequency idioms and content words from the 花生十三 lecture handout, tagged by the handout's own category and sub-category names | 993 | 993 | 291 tags, e.g. `中华文明传统文化`, `搭档配合`, `需要积累的其他词语` |

## Importing a deck

All three routes read the same package and return the same report; pick whichever fits.

### Web UI

1. Open the **Import** page of your instance: `<your-instance>/import`.
2. Leave the source on **Upload file**, choose the `.edeck` file, set **Import to** to
   **New deck**, then press **Import**.
3. To preview the outcome without writing anything, tick **Dry run first, do not write
   data**.

### Public link

On the same page, switch the source to **Public link** and paste the file's direct
download address — the raw file URL behind the repository's *Raw* / *Download* action
(GitHub: *Raw*; Gitea: *原始文件*). The link must be a public HTTPS link that returns the
package bytes; share pages, HTML pages and links behind a login are rejected. A
percent-encoded non-ASCII path (such as a Chinese file name) works as-is.

### CLI

The CLI talks to the database directly, so set `DB_DRIVER` and `DB_DSN` the same way you
do for `serve`.

```bash
# validate the package without writing anything
engram import --package decks/百化分.edeck --user <id|username> --dry-run

# import it as a new deck
engram import --package decks/百化分.edeck --user <id|username>
```

`--user` is required: the package is filed under one account and the CLI never guesses
which one. `--target` selects `new_deck` (the default), `into_deck:<id>` or
`replace_deck:<id>`; `--on-conflict` selects `update` (the default), `skip` or `fail`.

### MCP

The `import_deck` tool takes either `url` (the same public direct link) or `package` (a
base64-encoded archive) and returns the same import report.

## What is inside a package

An `.edeck` file is a zip archive. Four entries are required:

| Entry | Contents |
| :--- | :--- |
| `manifest.json` | format version, export time, deck name and description, the `include_*` flags and the entry counts |
| `notes.json` | the note content, in the same shape as the bulk note import request |
| `cards.json` | one entry per card: `note_index`, `template`, `ordinal` |
| `preset.json` | scheduling parameters; `weights: null` means the FSRS library defaults are used |

`progress.json` (the exporting user's own scheduling state) and `media.json` plus the
`media/…` binaries are optional, and are present only when the export included them. A
package that shipped one note type with no images and no progress therefore unzips to
exactly the four required files.

The format is described, with validation instructions, in
[`schema/README.md`](../schema/README.md) and
[`schema/deck-package.schema.json`](../schema/deck-package.schema.json).

## Adding a deck

1. Export the deck without progress (`include_progress` off) unless that progress is meant
   to be shared.
2. Sanitise the package before committing: no real host names, email addresses or
   credentials in the note text, and no `external_ref` value that names another system's
   private ids — a field such as `affine:<document-id>:…` points at the instance the deck
   was exported from. Drop the field, or replace it with a neutral key.
3. Validate it: import with `--dry-run` as shown above, and check the JSON form with
   `check-jsonschema` as described in [`schema/README.md`](../schema/README.md).
4. Add a row to the table above.
