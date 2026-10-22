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
 * Initialize auth session state (calls GET /api/v1/auth/session)
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
 * Reset auth state to zero when the session becomes invalid mid-way (called by the shell in ApiClient.onUnauthorized).
 *
 * Deliberately leaves error untouched: what the UI shows is up to the caller; this only handles "no longer claiming to be logged in".
 * Not navigating here is for the same reason - navigation policy belongs to the shell.
 */
export function clearSession(): void {
  authStore.set({
    initialized: true,
    loading: false,
    authenticated: false,
    user: null,
    error: null,
  });
}

/**
 * SPA login operation
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
 * SPA login second step (TOTP): submit the one-time code or a one-time recovery code and establish auth state on success.
 * The second-step credential is issued by the server after the password passes (HttpOnly cookie); this only submits and syncs state.
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
 * SPA logout operation
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
 * SPA self-registration: after a successful registration the server issues no session (consistent with SSR), so authStore is left untouched and
 * the caller redirects to the login page. The CSRF token is filled in automatically by apiClient when needed.
 */
export async function register(input: RegisterRequest): Promise<RegisterResponse> {
  return apiClient.register(input);
}

/**
 * SPA first-admin bootstrap: likewise establishes no session and redirects to the login page on success.
 */
export async function setup(input: SetupRequest): Promise<SetupResponse> {
  return apiClient.setup(input);
}
