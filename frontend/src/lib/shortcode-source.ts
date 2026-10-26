/**
 * Source text of the shortcodes the renderer expands, and where an insertion lands.
 *
 * The grammar is the one the server parser in `internal/render/shortcode.go` accepts. A shortcode
 * is a single line of the form `{{name:body|key=value|...}}`:
 *
 *   - the body ends at the first `}}` on the line, and a body that itself contains `{{` is
 *     rejected (shown literally), so a shortcode never expands another one's payload;
 *   - the parameter list starts at the first `|` that is followed by `name=` (or, for audio, by
 *     the bare word `loop`); any other `|` stays part of the body;
 *   - `tts` takes `lang` (a BCP-47-like tag), `rate` (a non-negative decimal), and the bare flag `hide`;
 *   - `audio` takes a body pinned to this service's own content-addressed media path, the
 *     parameters `start`, `end`, `rate`, and the bare flag `loop`;
 *   - `ruby` takes exactly two positional parts and no parameters.
 *
 * Everything this module builds is therefore something the server recognises and, when the input
 * is rejected here, the panel disables insertion instead of writing source the server would print
 * verbatim. The two rules that go beyond the server are marked where they are enforced.
 */

/** The three shortcode names the editor can write. */
export type ShortcodeName = 'tts' | 'audio' | 'ruby';

/** The input an issue belongs to, so the panel can mark that one field. */
export type ShortcodeField =
  | 'body'
  | 'lang'
  | 'rate'
  | 'source'
  | 'start'
  | 'end'
  | 'base'
  | 'reading';

/**
 * Why the current input cannot be written. Codes are stable identifiers; the panel owns the
 * localized sentence for each one.
 */
export type ShortcodeIssueCode =
  | 'body_required'
  | 'body_single_line'
  | 'body_nested'
  | 'body_closing'
  | 'body_separator'
  | 'body_control'
  | 'lang_shape'
  | 'rate_shape'
  | 'media_required'
  | 'media_shape'
  | 'time_shape'
  | 'range_order'
  | 'ruby_base_required'
  | 'ruby_reading_required'
  | 'ruby_part';

export interface ShortcodeIssue {
  field: ShortcodeField;
  code: ShortcodeIssueCode;
}

/** What the panel's three sections collect; an absent optional is one the user left empty or unchecked. */
export type ShortcodeInput =
  | { name: 'tts'; body: string; lang?: string; rate?: string; hide?: boolean }
  | { name: 'audio'; source: string; start?: string; end?: string; rate?: string; loop?: boolean }
  | { name: 'ruby'; base: string; reading: string };

export type ShortcodeBuild =
  | { ok: true; source: string }
  | { ok: false; issue: ShortcodeIssue };

/** The number shape every numeric parameter accepts: a non-negative decimal, at most 9 + 6 digits. */
const NUMBER_PATTERN = /^[0-9]{1,9}(?:\.[0-9]{1,6})?$/;

/** The language tag shape: a two- or three-letter primary subtag, then dot-less alphanumeric subtags. */
const LANG_PATTERN = /^[A-Za-z]{2,3}(?:-[A-Za-z0-9]{2,8})*$/;

/** An audio body is this service's own media path, not a general URL. */
const MEDIA_PATTERN = /^\/media\/[0-9a-f]{64}$/;

/** Control characters have no meaning in text; the server's body pattern excludes them. */
const CONTROL_PATTERN = /[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/;

/** The shape that turns a `|` into the parameter separator. */
const PARAM_START_PATTERN = /^[A-Za-z][A-Za-z0-9]*=/;

/**
 * Checks the free-text body the way the server does, plus the `}}` case: the server ends the body
 * at that sequence, so a body containing it would silently lose its tail.
 */
function checkBody(body: string, field: ShortcodeField): ShortcodeIssue | null {
  if (body === '') return { field, code: 'body_required' };
  if (body.includes('\n') || body.includes('\r')) return { field, code: 'body_single_line' };
  if (body.includes('{{')) return { field, code: 'body_nested' };
  if (body.includes('}}')) return { field, code: 'body_closing' };
  if (CONTROL_PATTERN.test(body)) return { field, code: 'body_control' };
  // A `|name=` inside the body is read as the start of the parameter list, which cuts the body
  // short and turns the tail into parameters the shortcode does not accept.
  for (let index = 0; index < body.length; index += 1) {
    if (body[index] !== '|') continue;
    if (PARAM_START_PATTERN.test(body.slice(index + 1))) return { field, code: 'body_separator' };
  }
  return null;
}

/** A token is trimmed before it is validated: surrounding spaces are never part of a token. */
function token(value: string | undefined): string {
  return (value ?? '').trim();
}

/**
 * Builds the source text for one shortcode.
 *
 * `source` is the exact string to write into a field. Text parts (the tts body, the ruby halves)
 * keep their spaces as typed because they are visible text; tokens (language, numbers, the media
 * path) are trimmed because whitespace there is never meaningful.
 */
export function buildShortcode(input: ShortcodeInput): ShortcodeBuild {
  if (input.name === 'tts') {
    const bodyIssue = checkBody(input.body, 'body');
    if (bodyIssue) return { ok: false, issue: bodyIssue };
    const lang = token(input.lang);
    const rate = token(input.rate);
    if (lang !== '' && !LANG_PATTERN.test(lang)) return { ok: false, issue: { field: 'lang', code: 'lang_shape' } };
    if (rate !== '' && !NUMBER_PATTERN.test(rate)) return { ok: false, issue: { field: 'rate', code: 'rate_shape' } };
    const params = `${lang === '' ? '' : `|lang=${lang}`}${rate === '' ? '' : `|rate=${rate}`}${
      input.hide ? '|hide' : ''
    }`;
    return { ok: true, source: `{{tts:${input.body}${params}}}` };
  }

  if (input.name === 'audio') {
    const source = token(input.source);
    if (source === '') return { ok: false, issue: { field: 'source', code: 'media_required' } };
    if (!MEDIA_PATTERN.test(source)) return { ok: false, issue: { field: 'source', code: 'media_shape' } };
    const start = token(input.start);
    const end = token(input.end);
    const rate = token(input.rate);
    if (start !== '' && !NUMBER_PATTERN.test(start)) return { ok: false, issue: { field: 'start', code: 'time_shape' } };
    if (end !== '' && !NUMBER_PATTERN.test(end)) return { ok: false, issue: { field: 'end', code: 'time_shape' } };
    if (rate !== '' && !NUMBER_PATTERN.test(rate)) return { ok: false, issue: { field: 'rate', code: 'rate_shape' } };
    // Deliberate rule beyond the server: it accepts a reversed range, but the play control then
    // seeks to a start that is already past its end, so the segment can never play.
    if (start !== '' && end !== '' && Number(end) <= Number(start)) {
      return { ok: false, issue: { field: 'end', code: 'range_order' } };
    }
    const params = `${start === '' ? '' : `|start=${start}`}${end === '' ? '' : `|end=${end}`}${
      rate === '' ? '' : `|rate=${rate}`
    }${input.loop ? '|loop' : ''}`;
    return { ok: true, source: `{{audio:${source}${params}}}` };
  }

  const base = input.base;
  const reading = input.reading;
  if (base === '') return { ok: false, issue: { field: 'base', code: 'ruby_base_required' } };
  if (reading === '') return { ok: false, issue: { field: 'reading', code: 'ruby_reading_required' } };
  // Ruby has two positional parts: `=`, `|`, a nested opener or a body terminator would be read as
  // a parameter, a third part, or the end of the shortcode.
  const rejected = (part: string): boolean =>
    part.includes('=') ||
    part.includes('|') ||
    part.includes('{{') ||
    part.includes('}}') ||
    part.includes('\n') ||
    part.includes('\r') ||
    CONTROL_PATTERN.test(part);
  if (rejected(base)) return { ok: false, issue: { field: 'base', code: 'ruby_part' } };
  if (rejected(reading)) return { ok: false, issue: { field: 'reading', code: 'ruby_part' } };
  return { ok: true, source: `{{ruby:${base}|${reading}}}` };
}

/** A caret inside a text field: `start === end` is a collapsed cursor, otherwise a selection. */
export interface Caret {
  start: number;
  end: number;
}

/**
 * Splices `source` into `text` at a known caret, or appends it on its own line when no caret is
 * known. A known caret from before the text changed is clamped into the current length, so a
 * stale position can never corrupt the field.
 */
export function insertSource(text: string, source: string, caret?: Caret | null): { text: string; caret: Caret } {
  if (caret && Number.isFinite(caret.start) && Number.isFinite(caret.end)) {
    const start = Math.min(Math.max(0, Math.trunc(caret.start)), text.length);
    const end = Math.min(Math.max(start, Math.trunc(caret.end)), text.length);
    return {
      text: `${text.slice(0, start)}${source}${text.slice(end)}`,
      caret: { start: start + source.length, end: start + source.length },
    };
  }
  // The appended form needs the source to start a line of its own; a field that already ends with
  // a line break does not get a second one.
  const separator = text === '' || text.endsWith('\n') ? '' : '\n';
  const next = `${text}${separator}${source}`;
  return { text: next, caret: { start: next.length, end: next.length } };
}
