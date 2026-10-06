import { writable } from 'svelte/store';
import { apiClient } from '../api';
import type { User, SessionResponse, LoginResponse, LogoutResponse } from '../api';
import { setLocale, isSupportedLocale } from '../i18n';

export interface AuthState {
  initialized: boolean;
  loading: boolean;
  authenticated: boolean;
  user: User | null;
  error: string | null;
}

const initialState: AuthState = {
  initialized: false,
  loading: false,
  authenticated: false,
  user: null,
  error: null,
};

export const authStore = writable<AuthState>(initialState);

/**
 * 初始化认证会话状态（调用 GET /api/v1/auth/session）
 */
export async function initAuth(): Promise<SessionResponse | null> {
  authStore.update((s) => ({ ...s, loading: true, error: null }));
  try {
    const session = await apiClient.getSession();
    authStore.set({
      initialized: true,
      loading: false,
      authenticated: session.authenticated,
      user: session.user,
      error: null,
    });
    if (session.user?.locale && isSupportedLocale(session.user.locale)) {
      setLocale(session.user.locale);
    }
    return session;
  } catch (err) {
    const errorMsg = err instanceof Error ? err.message : String(err);
    authStore.set({
      initialized: true,
      loading: false,
      authenticated: false,
      user: null,
      error: errorMsg,
    });
    return null;
  }
}

/**
 * SPA 登录操作
 */
export async function login(username: string, password: string): Promise<LoginResponse> {
  authStore.update((s) => ({ ...s, loading: true, error: null }));
  try {
    const res = await apiClient.login({ username, password });
    if (res.authenticated && res.user) {
      authStore.set({
        initialized: true,
        loading: false,
        authenticated: true,
        user: res.user,
        error: null,
      });
      if (res.user.locale && isSupportedLocale(res.user.locale)) {
        setLocale(res.user.locale);
      }
    } else {
      authStore.update((s) => ({ ...s, loading: false }));
    }
    return res;
  } catch (err) {
    authStore.update((s) => ({
      ...s,
      loading: false,
      error: err instanceof Error ? err.message : String(err),
    }));
    throw err;
  }
}

/**
 * SPA 登出操作
 */
export async function logout(): Promise<LogoutResponse> {
  authStore.update((s) => ({ ...s, loading: true }));
  try {
    const res = await apiClient.logout();
    authStore.set({
      initialized: true,
      loading: false,
      authenticated: false,
      user: null,
      error: null,
    });
    return res;
  } catch (err) {
    authStore.update((s) => ({
      ...s,
      loading: false,
      error: err instanceof Error ? err.message : String(err),
    }));
    throw err;
  }
}
