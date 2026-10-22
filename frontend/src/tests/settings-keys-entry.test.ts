import { describe, it, expect, beforeEach } from 'vitest';
import { render } from 'svelte/server';
import AppSidebar from '../lib/components/shell/AppSidebar.svelte';
import SettingsView from '../lib/views/SettingsView.svelte';
import SecurityView from '../lib/views/SecurityView.svelte';
import EmailSettingsView from '../lib/views/EmailSettingsView.svelte';
import { setLocale } from '../lib/i18n';
import { zhCN } from '../lib/i18n/locales/zh-CN';
import { en } from '../lib/i18n/locales/en';
import { matchRoute, routeStore } from '../lib/router';
import { routes } from '../lib/router/routes';

/**
 * The API key entry moved from the main nav to the /settings page.
 *
 * Two observable facts: the main nav no longer has a /settings/keys link, and the settings
 * page's tab bar has an item pointing at /settings/keys. The page content itself stays at
 * /settings/keys — this only verifies the route is still there and does not repeat
 * APIKeysView's own tests.
 */
describe('API keys entry lives on the settings page, not the header', () => {
  beforeEach(() => {
    setLocale('zh-CN');
  });

  it('renders the API keys tab on /settings with a link to /settings/keys', () => {
    const { html } = render(SettingsView, { props: { initialLoading: false } });
    expect(html).toContain('data-testid="settings-nav"');
    expect(html).toMatch(/href="\/settings\/keys"[^>]*data-testid="settings-nav-keys"/);
    expect(html).toContain('API 与 MCP');
  });

  it('highlights only the general tab on /settings, not every settings tab', () => {
    routeStore.set(matchRoute('/settings'));
    const { html } = render(SettingsView, { props: { initialLoading: false } });
    expect(html).toMatch(/href="\/settings"[^>]*data-testid="settings-nav-general"[^>]*aria-current="page"/);
    expect(html).not.toMatch(/data-testid="settings-nav-keys"[^>]*aria-current="page"/);
  });

  it('no longer offers an API keys link in the main navigation', () => {
    const { html } = render(AppSidebar);
    expect(html).not.toContain('href="/settings/keys"');
    // The settings entry itself is still in the main nav: otherwise the negative assertion above would "pass" even if the whole nav were deleted.
    expect(html).toContain('href="/settings"');
  });

  it('keeps /settings/keys as the canonical address for the page', () => {
    expect(matchRoute('/settings/keys', routes).route?.name).toBe('settings-keys');
    expect(matchRoute('/settings', routes).route?.name).toBe('settings');
  });

  it('drops the now-unused nav.api_keys key and carries the settings.keys entry in both catalogs', () => {
    expect(zhCN['nav.api_keys']).toBeUndefined();
    expect(en['nav.api_keys']).toBeUndefined();
    expect(zhCN['settings.nav.api']).toBeTruthy();
    expect(en['settings.nav.api']).toBeTruthy();
    // The entry card became a tab bar, so the card button's copy is no longer needed.
    expect(zhCN['settings.keys.entry']).toBeUndefined();
    expect(en['settings.keys.entry']).toBeUndefined();
  });
});

describe('settings tabs after the merge', () => {
  beforeEach(() => {
    setLocale('zh-CN');
  });

  it('offers exactly four tabs: general, security, email, API & MCP', () => {
    const { html } = render(SettingsView, { props: { initialLoading: false } });
    for (const id of ['general', 'security', 'email', 'keys']) {
      expect(html).toContain(`data-testid="settings-nav-${id}"`);
    }
    expect(html).not.toContain('data-testid="settings-nav-totp"');
    expect(html).not.toContain('data-testid="settings-nav-notifications"');
  });

  it('moves the password form off the general page onto the security page', () => {
    expect(render(SettingsView, { props: { initialLoading: false } }).html).not.toContain('data-testid="settings-password"');
    routeStore.set(matchRoute('/settings/security'));
    const { html } = render(SecurityView);
    expect(html).toContain('data-testid="settings-password"');
    expect(html).toContain('data-testid="totp-view"');
    expect(html).toMatch(/data-testid="settings-nav-security"[^>]*aria-current="page"/);
  });

  it('shows notification preferences on the email page', () => {
    const { html } = render(EmailSettingsView);
    expect(html).toContain('data-testid="notifications-view"');
  });
});
