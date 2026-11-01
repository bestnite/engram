import { describe, it, expect } from 'vitest';
import {
  resolveLocale,
  formatMessage,
  setLocale,
  localeStore,
  getLocale,
} from '../lib/i18n';

describe('i18n language resolution and formatting', () => {
  it('resolves locale following the documented priority hierarchy', () => {
    // Priority 1: a URL param overrides every later source
    expect(
      resolveLocale({
        urlParam: 'en',
        userSetting: 'zh-CN',
        navigatorLang: 'zh-CN',
      })
    ).toBe('en');

    // Priority 2: with no URL param, the signed-in user's own setting applies
    expect(
      resolveLocale({
        urlParam: null,
        userSetting: 'en',
        navigatorLang: 'zh-CN',
      })
    ).toBe('en');

    // Priority 3: no URL param and no user setting — follow the browser's Accept-Language/navigatorLang
    expect(
      resolveLocale({
        urlParam: null,
        userSetting: null,
        navigatorLang: 'en-US',
      })
    ).toBe('en');

    expect(
      resolveLocale({
        urlParam: null,
        userSetting: null,
        navigatorLang: 'zh-TW',
      })
    ).toBe('zh-CN');

    // Priority 4: no recognizable preference at all — fall back to the site default (zh-CN)
    expect(
      resolveLocale({
        urlParam: null,
        userSetting: null,
        navigatorLang: 'ja-JP',
      })
    ).toBe('zh-CN');
  });

  it('formats messages with parameter interpolation and fallback', () => {
    expect(formatMessage('zh-CN', 'app.name')).toBe('Engram');
    expect(formatMessage('en', 'nav.today')).toBe('Today');

    // Param substitution
    const custom = formatMessage('zh-CN', 'decks.select_deck', { name: 'Kanji' });
    expect(custom).toBe('选择卡组 Kanji');

    // An unknown key falls through unchanged
    expect(formatMessage('zh-CN', 'nonexistent.key')).toBe('nonexistent.key');
  });

  it('inserts a parameter literally instead of expanding replacement patterns', () => {
    // `String.replace` interprets these in a *string* replacement, so a deck name or tag carrying
    // them would corrupt the sentence: `$&` re-inserts the match, `$'` appends the rest of the text,
    // `` $` `` prepends the text before the match, and `$$` becomes one `$`.
    const cases = ['A$&B', "$'", '$`', 'A$$B'];
    for (const name of cases) {
      expect(formatMessage('zh-CN', 'decks.select_deck', { name })).toBe(`选择卡组 ${name}`);
    }
  });

  it('updates reactive localeStore correctly', () => {
    let observed: string | undefined;
    const unsub = localeStore.subscribe((val) => {
      observed = val;
    });

    setLocale('en');
    expect(getLocale()).toBe('en');
    expect(observed).toBe('en');

    setLocale('zh-CN');
    expect(getLocale()).toBe('zh-CN');
    expect(observed).toBe('zh-CN');

    unsub();
  });
});
