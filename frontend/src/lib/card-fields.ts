/**
 * Field specs and blank form initialization.
 *
 * The field table (order, control shape, requiredness, default values) comes from the server's card-kind self-description (GET /api/v1/card-types,
 * with the contract in internal/cardtype/describe.go). This file keeps only transport-agnostic pure functions: the control vocabulary,
 * empty form initialization, and multi-line text conversion. A note field value itself is an HTML string and is never converted here.
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

/** Array fields ↔ multi-line text (one item per line). */
export function linesToArray(text: string): string[] {
  return text.split('\n');
}

export function arrayToLines(value: unknown): string {
  return Array.isArray(value) ? value.map((item) => String(item)).join('\n') : '';
}
