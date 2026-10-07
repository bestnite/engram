// @vitest-environment happy-dom
import { describe, it, expect, beforeEach } from 'vitest';
import { get } from 'svelte/store';
import { navigate, routeStore } from '../lib/router';
import { authStore, type AuthState } from '../lib/auth';
import { readLanguageFromURL, setLanguageInURL } from '../lib/i18n/url';

/**
 * 匿名访客的语言覆盖参数住在地址上（服务端对每个请求都按「?lang 优先」解析），
 * 所以它必须熬过站内跳转与刷新。已登录用户不保留：他的语言在 /settings 里落库。
 */
const signedOut: AuthState = { initialized: true, loading: false, authenticated: false, user: null, error: null };

const currentURL = (): string => `${window.location.pathname}${window.location.search}`;

describe('anonymous language override in the URL', () => {
  beforeEach(() => {
    authStore.set(signedOut);
    window.history.replaceState({}, '', '/login');
  });

  it('writes the choice into the address so a reload keeps it', () => {
    setLanguageInURL('en');
    expect(currentURL()).toBe('/login?lang=en');
    expect(readLanguageFromURL()).toBe('en');
    // 复用已有查询串，而不是把原来的参数挤掉。
    window.history.replaceState({}, '', '/login?next=%2Fdecks');
    setLanguageInURL('zh-CN');
    expect(currentURL()).toBe('/login?next=%2Fdecks&lang=zh-CN');
  });

  it('carries the language across in-app navigation while signed out', () => {
    setLanguageInURL('en');
    navigate('/register');
    expect(currentURL()).toBe('/register?lang=en');
    // 路由状态与写进地址的 URL 一致：语言进了查询串，视图仍按 /register 匹配。
    expect(get(routeStore).path).toBe('/register');
    expect(get(routeStore).query).toEqual({ lang: 'en' });

    // 目标地址自带的参数保留（语言只有一个值，不重复追加）。
    navigate('/register?lang=en');
    expect(currentURL()).toBe('/register?lang=en');
    navigate('/setup?step=1');
    expect(currentURL()).toBe('/setup?step=1&lang=en');
  });

  it('drops the language for signed-in users so the account setting stays authoritative', () => {
    setLanguageInURL('en');
    authStore.set({ ...signedOut, authenticated: true });
    navigate('/decks');
    expect(currentURL()).toBe('/decks');
  });
});
