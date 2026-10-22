import type { CardTypeCatalog } from './card-types';

/**
 * Backend enums → UI copy.
 *
 * Values like `basic_both` from the backend are **machine vocabulary**: splicing one straight into a template
 * (`<span>{note.kind}</span>`) prints English in a Chinese UI - the result is exactly the same as
 * hardcoded English, only it does not look like it, so the lint that greps for hardcoded copy cannot catch it either.
 *
 * Two rules:
 *
 *  1. The criterion is "is this string for a machine or for a person". Values going into query strings or request bodies must be raw; anything that will
 *     appear on screen is first translated into a language pack key.
 *  2. **Unknown values always count as empty**. When `formatMessage` cannot find a key it **returns the key name as-is**, so
 *     `$t('notes.kind.' + kind)` prints `notes.kind.foo` when the backend adds a new card kind; splicing the raw value
 *     prints the machine word instead. Both are worse than "one badge fewer": the UI would rather briefly show one cell less than misreport or
 *     leak an internal identifier - the next change will fill it in anyway, while a wrong status goes unnoticed.
 */

/** Minimal shape of a translation function (`$t` is enough). */
export type Translate = (key: string) => string;

/**
 * Language pack key for a card kind; returns null for unknown/empty values or when metadata is not ready.
 * The card kind list takes the server's self-description (GET /api/v1/card-types) as its only source.
 */
export function noteKindLabelKey(
  kind: string | null | undefined,
  catalog: CardTypeCatalog | null
): string | null {
  if (!kind || !catalog || !catalog.byKind.has(kind)) return null;
  return `notes.kind.${kind}`;
}

/**
 * Kinds of dangerous action on a deck card.
 * delete: an owned deck, deletion is irreversible; leave: a shared deck, leaving only removes your own authorization row.
 */
export type DeckActionKind = 'delete' | 'leave';

/**
 * Decides which action the card shows based on the caller's explicit relation to the deck (Deck.role).
 *
 * owner -> 'delete'; editor/reader (shared) -> 'leave'.
 * A deck in the list is necessarily one of these two (owned, or explicitly granted) - there is no "visible but not authorized"
 * deck. Unknown values always return null: better to show one button fewer than to render
 * "leave shared deck" for a deck with no authorization row, which would only yield not_found when clicked.
 */
export function deckActionKind(role: string | null | undefined): DeckActionKind | null {
  switch (role) {
    case 'owner':
      return 'delete';
    case 'editor':
    case 'reader':
      return 'leave';
    default:
      return null;
  }
}

/** Card kind badge copy; returns an empty string for an unknown enum or when metadata is not ready. */
export function noteKindLabel(
  kind: string | null | undefined,
  catalog: CardTypeCatalog | null,
  t: Translate
): string {
  const key = noteKindLabelKey(kind, catalog);
  return key ? t(key) : '';
}

/**
 * Preset display name.
 *
 * The default preset's stored name is a stable identifier (`store.DefaultPresetName`, the English constant "Default"),
 * and it is **machine vocabulary**: printed straight into a Chinese UI it is English, no different from hardcoded English. Any preset name that will
 * appear on screen goes through here, with the server's `is_default` deciding between language pack copy and the name the user chose.
 */
export function presetDisplayName(name: string, isDefault: boolean, t: Translate): string {
  return isDefault ? t('presets.default_name') : name;
}

/** One item in the preset dropdown; value is the preset id as a string (Select only accepts string values). */
export interface PresetSelectOption {
  value: string;
  label: string;
}

/**
 * Preset dropdown of the create-deck dialog: projects the preset list into options and provides the value to preselect.
 *
 * Here a "default preset" sentinel item is deliberately **not** appended. The server's padding guarantees at least one default preset in the list,
 * and its display name (presets.default_name) is almost identical to the sentinel's copy: with only one preset the dropdown would show two
 * "Default" entries at once, looking like a preset that was not deleted.
 *
 * When the list is empty (the fetch failed) selected is an empty string, and the caller submits against the server's default preset on that basis (preset_id as an empty string).
 */
export function presetSelectOptions(
  presets: ReadonlyArray<{ id: string; name: string; is_default: boolean }>,
  t: Translate
): { options: PresetSelectOption[]; selected: string } {
  const options = presets.map((item) => ({
    value: String(item.id),
    label: presetDisplayName(item.name, item.is_default, t),
  }));
  const chosen = presets.find((item) => item.is_default) ?? presets[0];
  return { options, selected: chosen ? String(chosen.id) : '' };
}
