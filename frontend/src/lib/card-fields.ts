/**
 * Field specs and blank form initialization.
 *
 * A note type's fields are data: the editor builds a spec per field from the stored config. This file keeps only
 * transport-agnostic pure functions: the control vocabulary, empty form initialization, and multi-line text
 * conversion. A note field value itself is an HTML string and is never converted here.
 */

/** Control shape of one edit-form field. */
export type FieldControl = 'text' | 'textarea' | 'lines' | 'number' | 'bool' | 'index' | 'indexes';

/** Field spec, i.e. the shape the edit form renders one note-type field from. */
export interface FieldSpec {
  key: string;
  control: FieldControl;
  required: boolean;
  /** Server default of a boolean field that has one; when there is none the key is omitted. */
  default?: unknown;
}

/** Blank fields for a set of specs: boolean defaults follow the server semantics (missing means default false). */
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

/** Array fields ↔ multi-line text (one item per line). */
export function linesToArray(text: string): string[] {
  return text.split('\n');
}

export function arrayToLines(value: unknown): string {
  return Array.isArray(value) ? value.map((item) => String(item)).join('\n') : '';
}
