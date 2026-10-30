/**
 * Field-reference rewriting and a deliberately small template preview for the note type editor.
 *
 * The server's renderer (internal/notetype/template.go) is the authority for what a card finally
 * shows. This module exists so the editor can show the effect of unsaved templates without a round
 * trip: `rewriteFieldReferences` follows Anki's rename rule, and `renderPreview` expands the common
 * template forms. The preview is an approximation, not a second renderer; the card frame still keeps
 * the unsanitised output away from the parent page.
 */

/** Matches every `{{...}}` reference in one pass. Template content cannot contain a brace itself. */
const REFERENCE = /\{\{([^{}]*)\}\}/g;

/**
 * Rewrite a field reference when a field is renamed, the way Anki does: the plain `{{Old}}` form,
 * the conditional forms `{{#Old}}`, `{{^Old}}` and `{{/Old}}`, and filter forms whose field is the
 * last colon-separated argument (`{{text:Old}}`, `{{hint:Old}}`, `{{furigana:Old}}`,
 * `{{tts en_US:Old}}`).
 *
 * A filter argument that is not the field name is left alone: in `{{tts en_US:Old}}` the language
 * stays `en_US`, and a filter with an empty field such as `{{tts-voices:}}` is untouched. A name
 * that merely contains the old name (`{{Older}}`) does not match, because every form compares the
 * whole argument after trimming surrounding whitespace.
 *
 * Every rewrite passes the new name to the replace call as a replacement function rather than as a
 * replacement string: a string replacement treats `$&`, `` $` ``, `$'` and `$$` as substitution
 * patterns, so renaming `Old` to `Price$&` would otherwise write `{{PriceOld}}`. A replacement
 * function returns its value verbatim, which is what a field name is.
 */
export function rewriteFieldReferences(text: string, oldName: string, newName: string): string {
  if (oldName === '' || oldName === newName) return text;
  // A replacement function keeps a `$` sequence in the new name literal; see the function doc.
  const renamed = () => newName;
  return text.replace(REFERENCE, (match, rawInner: string) => {
    const inner = rawInner;

    // Conditional open/close: the prefix is part of the syntax, the rest is the field name.
    const conditional = /^([#^/])([\s\S]*)$/.exec(inner);
    if (conditional) {
      const body = conditional[2] ?? '';
      if (body.trim() === oldName) {
        return `{{${conditional[1] ?? ''}${body.replace(oldName, renamed)}}}`;
      }
      return match;
    }

    // Filter form: only the last colon-separated argument names a field.
    const colon = inner.lastIndexOf(':');
    if (colon >= 0) {
      const head = inner.slice(0, colon + 1);
      const tail = inner.slice(colon + 1);
      if (tail.trim() === oldName) {
        return `{{${head}${tail.replace(oldName, renamed)}}}`;
      }
      return match;
    }

    if (inner.trim() === oldName) {
      return `{{${inner.replace(oldName, renamed)}}}`;
    }
    return match;
  });
}

/** The special fields a template can reference besides the note's own fields. */
const SPECIAL_FIELDS = new Set(['FrontSide', 'Tags', 'Type', 'Deck', 'Subdeck', 'Card', 'CardFlag', 'CardID']);

/**
 * Expand a template for the live preview.
 *
 * `values` maps a field name to the text the preview shows for it; the editor passes the field name
 * itself, so the preview makes the layout visible without inventing sample content. Conditionals are
 * resolved by whether the referenced value is non-empty. `frontSide` is the already-rendered question
 * used by `{{FrontSide}}` on the answer side.
 *
 * This does not run answer controls, cloze expansion, LaTeX, media or typing filters; it substitutes
 * the field value for them. The result is card-author HTML and may only enter the sandboxed frame.
 */
export function renderPreview(
  template: string,
  values: Record<string, string>,
  frontSide = '',
): string {
  let out = resolveConditionals(template, values);
  out = out.replace(REFERENCE, (match, rawInner: string) => {
    const inner = rawInner.trim();
    if (inner === '') return match;
    if (inner === 'FrontSide') return frontSide;
    if (inner.startsWith('#') || inner.startsWith('^') || inner.startsWith('/')) return '';
    // A filter form names its field after the last colon; every filter therefore resolves to the
    // same field value, which is enough to show the layout.
    const colon = inner.lastIndexOf(':');
    const name = (colon >= 0 ? inner.slice(colon + 1) : inner).trim();
    if (name === '') return '';
    if (name in values) return values[name] ?? '';
    if (SPECIAL_FIELDS.has(name)) return name;
    return '';
  });
  return out;
}

/**
 * Resolve `{{#Field}}…{{/Field}}` and `{{^Field}}…{{/Field}}` from the innermost pair outwards.
 * A condition is true when the field value is non-empty (`#`) or empty (`^`).
 */
function resolveConditionals(template: string, values: Record<string, string>): string {
  const pair = /\{\{([#^])([^{}]*?)\}\}([\s\S]*?)\{\{\/([^{}]*?)\}\}/;
  let out = template;
  // A malformed template has no close for its open, so the loop bound stops it from spinning.
  for (let i = 0; i < 100; i += 1) {
    const match = pair.exec(out);
    if (!match) break;
    const [whole = '', kind = '', openName = '', body = '', closeName = ''] = match;
    const name = openName.trim();
    if (name !== closeName.trim()) {
      // A mismatched pair is left as-is for the server's validation to report.
      break;
    }
    const filled = (values[name] ?? '').trim() !== '';
    const keep = kind === '#' ? filled : !filled;
    out = out.slice(0, match.index) + (keep ? body : '') + out.slice(match.index + whole.length);
  }
  return out;
}
