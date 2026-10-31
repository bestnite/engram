import { describe, it, expect } from 'vitest';
import { formatMessage } from '../lib/i18n';
import { presetDisplayName, presetSelectOptions } from '../lib/labels';

/**
 * Enum label mapping: every backend enum value goes through lib/labels.ts into a language
 * pack message, and unknown values render as empty.
 */

const zh = (key: string) => formatMessage('zh-CN', key);
const en = (key: string) => formatMessage('en', key);

describe('enum labels go through the catalog', () => {
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
