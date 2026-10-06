import { writable } from 'svelte/store';
import { apiClient } from '../api';
import type {
  User,
  SessionResponse,
  LoginResponse,
  TOTPLoginResponse,
  LogoutResponse,
  RegisterRequest,
  RegisterResponse,
  SetupRequest,
  SetupResponse,
} from '../api';
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
 * SPA 登录第二步（TOTP）：提交动态验证码或一次性恢复码，通过后建立认证状态。
 * 第二步凭据由服务端在密码通过后下发（HttpOnly cookie），这里只负责提交与状态同步。
 */
export async function completeTOTP(code: string): Promise<TOTPLoginResponse> {
  authStore.update((s) => ({ ...s, loading: true, error: null }));
  try {
    const res = await apiClient.submitTOTP(code);
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

/**
 * SPA 自助注册：注册成功后服务端不签发会话（与 SSR 一致），因此不改动 authStore，
 * 由调用方跳转到登录页。CSRF token 由 apiClient 在需要时自动补齐。
 */
export async function register(input: RegisterRequest): Promise<RegisterResponse> {
  return apiClient.register(input);
}

/**
 * SPA 首个管理员引导：同样不建立会话，成功后跳转登录页。
 */
export async function setup(input: SetupRequest): Promise<SetupResponse> {
  return apiClient.setup(input);
}
