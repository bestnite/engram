import { describe, it, expect } from 'vitest';
import {
  resolveLocale,
  formatMessage,
  setLocale,
  localeStore,
  getLocale,
} from '../lib/i18n';

describe('i18n language resolution and formatting', () => {
  it('resolves locale following DESIGN.md §8.3 priority hierarchy', () => {
    // 优先级 1：URL 参数覆盖所有后续配置
    expect(
      resolveLocale({
        urlParam: 'en',
        userSetting: 'zh-CN',
        navigatorLang: 'zh-CN',
      })
    ).toBe('en');

    // 优先级 2：无 URL 参数时，已登录用户个人设置生效
    expect(
      resolveLocale({
        urlParam: null,
        userSetting: 'en',
        navigatorLang: 'zh-CN',
      })
    ).toBe('en');

    // 优先级 3：无 URL 参数且无用户设置时，跟随浏览器 Accept-Language/navigatorLang
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

    // 优先级 4：没有任何可识别偏好时，退回站点默认 (zh-CN)
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

    // 参数替换
    const custom = formatMessage('zh-CN', 'shell.welcome');
    expect(custom).toBe('欢迎使用 Engram');

    // 未知 key 原样回退
    expect(formatMessage('zh-CN', 'nonexistent.key')).toBe('nonexistent.key');
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
