import { describe, it, expect } from 'vitest';
import * as fs from 'node:fs';
import * as path from 'node:path';
import { fileURLToPath } from 'node:url';

/**
 * The `{@html}` allowlist.
 *
 * Card faces leave the parent DOM because unsanitised card content must only be rendered inside the sandboxed frame. Every
 * remaining HTML sink in the SPA is therefore a deliberate exception, and this case keeps the list of exceptions exact:
 * a use that is not listed fails the suite, and a listed use that no longer exists fails it too, so the list cannot rot.
 *
 * The scan is a literal token search over the text of every `.svelte` file below `frontend/src`. A token mentioned in a
 * comment counts as a use, which only means the surrounding text must be listed or reworded; that is cheaper than a real
 * parser and cannot miss a sink.
 */

interface HtmlSink {
  /** Path relative to `frontend/src`, with forward slashes. */
  file: string;
  /** The exact expression, whitespace-normalised. */
  expression: string;
  /** Why this use is allowed to stay in the parent page. */
  reason: string;
}

const ALLOWLIST: HtmlSink[] = [
  {
    file: 'lib/views/ReviewView.svelte',
    expression: '{@html item.answer_html}',
    reason:
      'The per-item grading breakdown renders the server-sanitised answer of each option of a list card. That is option ' +
      'HTML, not a card face, so it belongs to the same exception as the choice options.',
  },
  {
    file: 'lib/components/ui/RadioGroup.svelte',
    expression: '{@html option.html}',
    reason:
      'Single-choice and boolean option labels render the server-sanitised option HTML the caller passes in. These are ' +
      'option labels, not card faces, and stay in the parent page with the other option HTML.',
  },
  {
    file: 'lib/views/admin/AdminMailTemplatesView.svelte',
    expression: '{@html preview.html}',
    reason:
      'The admin mail-template preview renders the server-rendered mail body. It moves into a sandboxed frame together ' +
      'with the mail preview, not with the card face.',
  },
];

const SRC_ROOT = fileURLToPath(new URL('..', import.meta.url));
const USE_PATTERN = /\{@html\s[^}]*\}/g;

function normalise(expression: string): string {
  return expression.replace(/\s+/g, ' ').trim();
}

function svelteFiles(directory: string): string[] {
  const entries = fs.readdirSync(directory, { withFileTypes: true });
  const files: string[] = [];
  for (const entry of entries) {
    const full = path.join(directory, entry.name);
    if (entry.isDirectory()) files.push(...svelteFiles(full));
    else if (entry.name.endsWith('.svelte')) files.push(full);
  }
  return files;
}

/** Every HTML sink as `<relative file>::<expression>`, with duplicates kept so the comparison counts uses too. */
function findSinks(): string[] {
  const sinks: string[] = [];
  for (const file of svelteFiles(SRC_ROOT)) {
    const relative = path.relative(SRC_ROOT, file).split(path.sep).join('/');
    const content = fs.readFileSync(file, 'utf-8');
    for (const match of content.matchAll(USE_PATTERN)) {
      sinks.push(`${relative}::${normalise(match[0])}`);
    }
  }
  return sinks.sort();
}

describe('{@html} allowlist', () => {
  it('names every remaining HTML sink and its reason', () => {
    const allowed = ALLOWLIST.map((sink) => `${sink.file}::${normalise(sink.expression)}`).sort();
    expect(findSinks()).toEqual(allowed);
  });

  it('states a reason for every allowed sink', () => {
    for (const sink of ALLOWLIST) {
      expect(sink.reason.trim()).not.toBe('');
    }
  });
});
