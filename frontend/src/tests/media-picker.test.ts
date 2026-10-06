import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { en } from '../lib/i18n/locales/en';
import { zhCN } from '../lib/i18n/locales/zh-CN';

describe('SPA media picker editor integration', () => {
  it('offers readable-library selection, pagination, and lazy original images', () => {
    const view = readFileSync(fileURLToPath(new URL('../lib/views/NoteEditView.svelte', import.meta.url)), 'utf8');
    expect(view).toContain('apiClient.getMediaPickerPage(deckId');
    expect(view).toContain('data-testid="spa-media-picker-toggle"');
    expect(view).toContain('data-testid="spa-media-next"');
    expect(view).toContain('loading="lazy"');
    expect(view).toContain('![](${url})');
    expect(view).toContain("{$t('media.spa.field')}");
    expect(view).not.toContain('{@html fields');
  });

  it('provides identical media.spa key sets in both catalogs', () => {
    const keys = Object.keys(en).filter((key) => key.startsWith('media.spa.')).sort();
    expect(keys.length).toBeGreaterThan(0);
    expect(Object.keys(zhCN).filter((key) => key.startsWith('media.spa.')).sort()).toEqual(keys);
  });
});
