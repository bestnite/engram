/**
 * Count-driven messages and their per-language plural variants.
 *
 * A message that changes with a number is stored as its base id plus one entry per plural category
 * the language actually selects (`decks.count`, `decks.count.one`, `decks.count.few`, ...). The
 * runtime picks the variant with `Intl.PluralRules` and falls back to the base id, so a language only
 * stores the categories its own rule can return: English needs `one`, Russian needs `one`, `few` and
 * `many`, Japanese needs none.
 *
 * This list is the single definition of which ids may carry variants. Parity checks use it to compare
 * catalogs by base id, which keeps every language's key set aligned while letting a language add the
 * plural forms it needs. A key that merely ends with a category name but is not listed here (for
 * example `media.browser.class.other`, the "Other" option of a media class) is an ordinary message
 * and must exist in every catalog.
 */
export const PLURAL_CATEGORIES = ['zero', 'one', 'two', 'few', 'many', 'other'] as const;

/** Count-driven message ids; each may be stored with a `<id>.<category>` variant. */
export const PLURAL_BASE_KEYS: readonly string[] = [
  'notes.total_count',
  'presets.optimize.gate_shortfall',
  'decks.count',
  'stats.streak.days',
  'presets.optimize.shortfall',
  'admin.audit.total_label',
  'notes.bulk_delete_desc',
  'deck.tag_review.notes',
  'package.toast.entries_failed',
  'decks.bulk_delete.confirm_title',
  'decks.bulk_delete.shared_hint',
  'decks.bulk_delete.result',
  'notetype.empty.cleanup.count',
  'occlusion.shape_count',
];

const pluralBaseKeys = new Set(PLURAL_BASE_KEYS);

/**
 * Collapse `<id>.<category>` to `<id>` for a count-driven message; every other key is returned
 * unchanged. Two catalogs are aligned exactly when their collapsed key sets are equal.
 */
export function pluralBaseKey(key: string): string {
  const dot = key.lastIndexOf('.');
  if (dot < 0) return key;
  const suffix = key.slice(dot + 1);
  if (!(PLURAL_CATEGORIES as readonly string[]).includes(suffix)) return key;
  const base = key.slice(0, dot);
  return pluralBaseKeys.has(base) ? base : key;
}
