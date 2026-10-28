/**
 * Field specs and form model conversion.
 *
 * The field table (order, control shape, requiredness, default values) comes from the server's card-kind self-description (GET /api/v1/card-types,
 * with the contract in internal/cardtype/describe.go). This file keeps only transport-agnostic pure functions: the control vocabulary,
 * empty form initialization, and conversion between the form model and the submit payload. Views no longer write ten forms per card kind.
 */
import type { CardTypeField } from './api/types';

/** Field spec, i.e. the shape the server sends (key, control, required, default). */
export type FieldSpec = CardTypeField;

/** Blank fields when creating/switching card kind: boolean defaults follow the server semantics (missing means default false). */
export function emptyFields(specs: readonly FieldSpec[]): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const spec of specs) {
    if (spec.control === 'lines' || spec.control === 'indexes') out[spec.key] = [];
    else if (spec.control === 'bool') out[spec.key] = spec.default === true;
    else if (spec.control === 'number' || spec.control === 'index') out[spec.key] = '';
    else out[spec.key] = '';
  }
  return out;
}

/**
 * Fold the server fields into the form model: keep only the keys this card kind knows and fill in the missing ones.
 * Unknown keys (e.g. left over from an older card kind) are the caller's responsibility to preserve, to avoid silently losing data.
 */
export function toFormFields(
  specs: readonly FieldSpec[],
  raw: Record<string, unknown>
): Record<string, unknown> {
  const out = emptyFields(specs);
  for (const spec of specs) {
    const key = spec.key;
    const value = raw[key];
    if (value === undefined || value === null) continue;
    if (spec.control === 'lines' || spec.control === 'indexes') out[key] = Array.isArray(value) ? [...value] : [];
    else if (spec.control === 'bool') out[key] = Boolean(value);
    // Numbers and indexes are decided by the control, not by the initial value in the blank form: the blank form initializes them to '' ("not filled"),
    // and branching on the initial value type once turned the server's 42 into the string "42" - the input box showed empty,
    // and the preview request was rejected with 400 for the type mismatch (which surfaced in the UI as "fill in the required fields to preview").
    else if (spec.control === 'number' || spec.control === 'index') out[key] = toNumberOrEmpty(value);
    else out[key] = String(value);
  }
  return out;
}

/** Numbers are kept as-is; strings that parse fully into a finite number become numbers; everything else counts as not filled. */
function toNumberOrEmpty(value: unknown): number | '' {
  if (typeof value === 'number') return Number.isFinite(value) ? value : '';
  if (typeof value === 'string' && value.trim() !== '') {
    const parsed = Number(value);
    return Number.isFinite(parsed) ? parsed : '';
  }
  return '';
}

/**
 * Form model → fields submitted to the server.
 * Rules: empty strings and empty arrays are always omitted (the server's optional field validation rejects empty strings); an explicit false must be kept
 * (AGENTS.md §2.3 item 9: boolean columns with a DB default must not be omitted by zero values).
 */
export function toPayloadFields(form: Record<string, unknown>): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(form)) {
    if (value === undefined || value === null) continue;
    if (typeof value === 'string') {
      const trimmed = value.trim();
      if (trimmed !== '') out[key] = trimmed;
      continue;
    }
    if (Array.isArray(value)) {
      // Trim string items only; numeric items (multiple-choice answer indexes) are kept as-is. The server parses indexes as integers,
      // so converting everything to strings made ["0","2"] rejected with 400, breaking both preview and save.
      const items = value.flatMap((item) => {
        if (item === undefined || item === null) return [];
        if (typeof item === 'string') {
          const trimmed = item.trim();
          return trimmed === '' ? [] : [trimmed];
        }
        return [item];
      });
      if (items.length > 0) out[key] = items;
      continue;
    }
    out[key] = value;
  }
  return out;
}

/** Array fields ↔ multi-line text (one item per line). */
export function linesToArray(text: string): string[] {
  return text.split('\n');
}

export function arrayToLines(value: unknown): string {
  return Array.isArray(value) ? value.map((item) => String(item)).join('\n') : '';
}
