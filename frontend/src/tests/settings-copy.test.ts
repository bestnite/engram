import { describe, it, expect } from 'vitest';
import { zhCN } from '../lib/i18n/locales/zh-CN';
import { en } from '../lib/i18n/locales/en';

/**
 * Copy content for /settings and the header language switch.
 *
 * Key-set consistency across both language packs is guaranteed centrally by parity.test.ts;
 * this file only covers content: the keys that must exist are there, and the copy must not
 * keep teaching a state that has been removed (the switch's "not set").
 */
describe('settings and header copy in both catalogs', () => {
  it('has no unset option in either catalog', () => {
    expect(zhCN['settings.profile.cutoff_default']).toBeUndefined();
    expect(en['settings.profile.cutoff_default']).toBeUndefined();
    // The copy must no longer teach "leave blank" or "default" — the control has neither state any more.
    expect(zhCN['settings.profile.cutoff_hint']).not.toContain('留空');
    expect(zhCN['settings.profile.cutoff_hint']).not.toContain('默认');
    expect(en['settings.profile.cutoff_hint']).not.toContain('blank');
    expect(en['settings.profile.cutoff_hint']).not.toContain('Default');
  });

  it('has the save-failure notice in both catalogs', () => {
    expect(zhCN['language.save_failed']).toBeTruthy();
    expect(en['language.save_failed']).toBeTruthy();
  });
});
