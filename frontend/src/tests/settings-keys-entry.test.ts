import { describe, it, expect, beforeEach } from 'vitest';
import { render } from 'svelte/server';
import NavHeader from '../lib/components/NavHeader.svelte';
import SettingsView from '../lib/views/SettingsView.svelte';
import { setLocale } from '../lib/i18n';
import { zhCN } from '../lib/i18n/locales/zh-CN';
import { en } from '../lib/i18n/locales/en';
import { matchRoute } from '../lib/router';
import { routes } from '../lib/router/routes';

/**
 * API 密钥入口从页头导航移到 /settings 页。
 *
 * 两个可观测事实：页头不再有 /settings/keys 链接；设置页多了一张指向 /settings/keys 的入口卡片。
 * 页面内容本身仍留在 /settings/keys——这里只验证路由还在，不重复 APIKeysView 自己的测试。
 */
describe('API keys entry lives on the settings page, not the header', () => {
  beforeEach(() => {
    setLocale('zh-CN');
  });

  it('renders the API keys entry on /settings with a link to /settings/keys', () => {
    const { html } = render(SettingsView, { props: { initialLoading: false } });
    expect(html).toContain('data-testid="settings-keys-entry"');
    expect(html).toContain('href="/settings/keys"');
    expect(html).toContain('管理 API 密钥');
  });

  it('no longer offers an API keys link in the header nav', () => {
    const { html } = render(NavHeader);
    expect(html).not.toContain('href="/settings/keys"');
    // 设置入口本身仍在页头：否则整段导航被删时上面那条否定断言也会「通过」。
    expect(html).toContain('href="/settings"');
  });

  it('keeps /settings/keys as the canonical address for the page', () => {
    expect(matchRoute('/settings/keys', routes).route?.name).toBe('settings-keys');
    expect(matchRoute('/settings', routes).route?.name).toBe('settings');
  });

  it('drops the now-unused nav.api_keys key and carries the settings.keys entry in both catalogs', () => {
    expect(zhCN['nav.api_keys']).toBeUndefined();
    expect(en['nav.api_keys']).toBeUndefined();
    expect(zhCN['settings.keys.heading']).toBeTruthy();
    expect(en['settings.keys.heading']).toBeTruthy();
    expect(zhCN['settings.keys.entry']).toBeTruthy();
    expect(en['settings.keys.entry']).toBeTruthy();
  });
});
