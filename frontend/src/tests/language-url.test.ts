// @vitest-environment happy-dom
import { describe, it, expect, beforeEach } from 'vitest';
import { get } from 'svelte/store';
import { navigate, routeStore } from '../lib/router';
import { authStore, type AuthState } from '../lib/auth';
import { readLanguageFromURL, setLanguageInURL, clearLanguageInURL } from '../lib/i18n/url';

/**
 * Anonymous visitors keep their language override in the URL (the server resolves every
 * request with "?lang wins"), so it must survive in-site navigation and reloads.
 * Signed-in users do not keep it: their language is persisted in /settings.
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
    // Reuse the existing query string instead of pushing the original params out.
    window.history.replaceState({}, '', '/login?next=%2Fdecks');
    setLanguageInURL('zh-CN');
    expect(currentURL()).toBe('/login?next=%2Fdecks&lang=zh-CN');
  });

  it('carries the language across in-app navigation while signed out', () => {
    setLanguageInURL('en');
    navigate('/register');
    expect(currentURL()).toBe('/register?lang=en');
    // Router state matches the URL written to the address bar: the language went into the query string, and the view still matches /register.
    expect(get(routeStore).path).toBe('/register');
    expect(get(routeStore).query).toEqual({ lang: 'en' });

    // Params already on the target URL are kept (language has a single value, so it is not appended twice).
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

  it('clears a stale override once the language lives on the account', () => {
    // When a signed-in user switches in the header, the account setting is what gets written; a leftover ?lang in the URL outranks everything, so keeping it would override the value just stored.
    setLanguageInURL('en');
    expect(readLanguageFromURL()).toBe('en');
    clearLanguageInURL();
    expect(currentURL()).toBe('/login');
    expect(readLanguageFromURL()).toBeNull();

    // With no such param it is a no-op and other query strings are untouched.
    window.history.replaceState({}, '', '/decks?page=2');
    clearLanguageInURL();
    expect(currentURL()).toBe('/decks?page=2');
  });
});
