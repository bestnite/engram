import { describe, it, expect, vi, beforeEach } from 'vitest';
import { get } from 'svelte/store';
import { authStore, initAuth, login, logout } from '../lib/auth';
import { apiClient, ApiClientError } from '../lib/api';
import { localeStore } from '../lib/i18n';

describe('Auth store and SPA session management', () => {
  beforeEach(() => {
    // 重置 store 状态
    authStore.set({
      initialized: false,
      loading: false,
      authenticated: false,
      user: null,
      error: null,
    });
    vi.restoreAllMocks();
    if (typeof window !== 'undefined') {
      window.localStorage.clear();
      window.sessionStorage.clear();
    }
  });

  it('initializes auth state as unauthenticated when session is absent', async () => {
    vi.spyOn(apiClient, 'getSession').mockResolvedValueOnce({
      authenticated: false,
      user: null,
      csrf_token: 'csrf_anon_tok',
    });

    const res = await initAuth();
    expect(res?.authenticated).toBe(false);

    const state = get(authStore);
    expect(state.initialized).toBe(true);
    expect(state.authenticated).toBe(false);
    expect(state.user).toBeNull();
    expect(state.loading).toBe(false);
    expect(state.error).toBeNull();
  });

  it('initializes auth state and syncs user locale when session is active', async () => {
    vi.spyOn(apiClient, 'getSession').mockResolvedValueOnce({
      authenticated: true,
      user: {
        id: '10',
        username: 'bob',
        email: 'bob@example.com',
        role: 'user',
        locale: 'en',
      },
      csrf_token: 'csrf_sess_tok',
    });

    const res = await initAuth();
    expect(res?.authenticated).toBe(true);

    const state = get(authStore);
    expect(state.initialized).toBe(true);
    expect(state.authenticated).toBe(true);
    expect(state.user?.username).toBe('bob');
    expect(get(localeStore)).toBe('en');
  });

  it('updates auth state and user on successful login', async () => {
    vi.spyOn(apiClient, 'login').mockResolvedValueOnce({
      authenticated: true,
      user: {
        id: '20',
        username: 'carol',
        email: 'carol@example.com',
        role: 'admin',
        locale: 'zh-CN',
      },
      csrf_token: 'csrf_login_tok',
    });

    const res = await login('carol', 'Password123!');
    expect(res.authenticated).toBe(true);

    const state = get(authStore);
    expect(state.authenticated).toBe(true);
    expect(state.user?.username).toBe('carol');
    expect(get(localeStore)).toBe('zh-CN');
  });

  it('handles login failure and preserves unauthenticated state with error', async () => {
    vi.spyOn(apiClient, 'login').mockRejectedValueOnce(
      new ApiClientError('HTTP 401', {
        status: 401,
        code: 'invalid_credentials',
      })
    );

    await expect(login('carol', 'wrongpass')).rejects.toThrow(ApiClientError);

    const state = get(authStore);
    expect(state.authenticated).toBe(false);
    expect(state.user).toBeNull();
    expect(state.error).toBeTruthy();
  });

  it('handles logout and clears auth store', async () => {
    authStore.set({
      initialized: true,
      loading: false,
      authenticated: true,
      user: {
        id: '1',
        username: 'alice',
        email: 'alice@example.com',
        role: 'user',
        locale: 'zh-CN',
      },
      error: null,
    });

    vi.spyOn(apiClient, 'logout').mockResolvedValueOnce({
      authenticated: false,
      csrf_token: 'new_anon_csrf',
    });

    const res = await logout();
    expect(res.authenticated).toBe(false);

    const state = get(authStore);
    expect(state.authenticated).toBe(false);
    expect(state.user).toBeNull();
  });

  it('never leaks tokens or credentials to localStorage or sessionStorage during auth flows', async () => {
    vi.spyOn(apiClient, 'getSession').mockResolvedValueOnce({
      authenticated: true,
      user: {
        id: '1',
        username: 'alice',
        email: 'alice@example.com',
        role: 'user',
        locale: 'zh-CN',
      },
      csrf_token: 'sensitive_csrf_token',
    });

    await initAuth();

    if (typeof window !== 'undefined') {
      expect(window.localStorage.length).toBe(0);
      expect(window.sessionStorage.length).toBe(0);
    }
  });
});
