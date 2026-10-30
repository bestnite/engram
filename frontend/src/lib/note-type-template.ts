/**
 * Field-reference rewriting for the note type editor.
 *
 * The server's renderer (internal/notetype/template.go) is the authority for what a card shows. This module holds only
 * the rename rule: `rewriteFieldReferences` follows Anki's rewrite of a template when a field is renamed.
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
