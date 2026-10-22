import { describe, it, expect } from 'vitest';
import { formatMessage } from '../lib/i18n';
import {
  noteKindLabel,
  noteKindLabelKey,
  presetDisplayName,
  presetSelectOptions,
} from '../lib/labels';
import { createCatalog } from '../lib/card-types';
import { CARD_TYPES } from './card-type-fixture';

const catalog = createCatalog(CARD_TYPES.kinds);

/**
 * Enum label mapping: every backend enum value goes through lib/labels.ts into a language
 * pack message, and unknown values render as empty.
 *
 * The test is not "is it quoted" but "is this string meant for a machine or a human" —
 * interpolating the raw value in a template (`{note.kind}`) prints `basic_both` in the
 * Chinese UI, which is as bad as hardcoded English yet harder to spot.
 */

const zh = (key: string) => formatMessage('zh-CN', key);
const en = (key: string) => formatMessage('en', key);

describe('enum labels go through the catalog', () => {
  it('translates card kinds instead of printing the enum', () => {
    expect(noteKindLabel('basic_both', catalog, zh)).toBe('双向问答');
    expect(noteKindLabel('cloze', catalog, zh)).toBe('填空');
    expect(noteKindLabel('basic', catalog, en)).toBe('Basic');
  });

  it('renders nothing for an unknown or missing value', () => {
    // The point: an unknown value must never fall back to the key name or the machine word;
    // $t returns the key unchanged when the lookup fails, and the raw value prints English — both are worse than a missing badge.
    expect(noteKindLabel('made_up_kind', catalog, zh)).toBe('');
    expect(noteKindLabel(null, catalog, zh)).toBe('');
    expect(noteKindLabel(undefined, catalog, zh)).toBe('');
    // With metadata not yet loaded there is no field-type list either, so likewise empty rather than a bare key.
    expect(noteKindLabel('cloze', null, zh)).toBe('');
    expect(noteKindLabelKey('cloze', catalog)).toBe('notes.kind.cloze');
    expect(noteKindLabelKey('nope', catalog)).toBeNull();
    expect(noteKindLabelKey(null, catalog)).toBeNull();
    expect(noteKindLabelKey('cloze', null)).toBeNull();
  });

  it('renders the default preset through the catalog instead of its storage name', () => {
    // The library name "Default" (store.DefaultPresetName) is the default preset's identity: printed
    // as-is in the Chinese UI it is English. The criterion is the server's is_default; a user-created preset shows its name unchanged.
    expect(presetDisplayName('Default', true, zh)).toBe('默认');
    expect(presetDisplayName('Default', true, en)).toBe('Default');
    expect(presetDisplayName('Evening', false, zh)).toBe('Evening');
    expect(presetDisplayName('Evening', false, en)).toBe('Evening');
  });

  it('builds the create-deck preset options from the preset list alone', () => {
    // Regression: the dropdown used to hardcode a "default preset" sentinel item on top of the
    // real list, so with a single preset there were two "Default" entries (the sentinel text and presets.default_name are nearly identical), looking like a preset nobody deleted.
    // Options must correspond one-to-one with the presets the API returns — no more, no fewer.
    const one = presetSelectOptions([{ id: '6', name: 'Default', is_default: true }], zh);
    expect(one.options).toEqual([{ value: '6', label: '默认' }]);
    expect(one.selected).toBe('6');

    const many = presetSelectOptions(
      [
        { id: '7', name: 'Evening', is_default: false },
        { id: '6', name: 'Default', is_default: true },
      ],
      en
    );
    expect(many.options).toEqual([
      { value: '7', label: 'Evening' },
      { value: '6', label: 'Default' },
    ]);
    expect(many.selected).toBe('6');
  });

  it('preselects the first preset when none is flagged default, and nothing when the list is empty', () => {
    // Without is_default, fall back to the first one; when the list is empty (fetch failed) selected is an empty string,
    // and callers use that to submit the server's default preset (preset_id as an empty string).
    expect(presetSelectOptions([{ id: '9', name: 'Morning', is_default: false }], zh).selected).toBe('9');
    const empty = presetSelectOptions([], zh);
    expect(empty.options).toEqual([]);
    expect(empty.selected).toBe('');
  });
});
