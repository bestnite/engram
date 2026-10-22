/**
 * The language override parameter for anonymous visitors.
 *
 * The server resolves the language per request as "?lang > logged-in user settings > Accept-Language > site default"
 * (the language middleware in internal/web/middleware.go), and the entry's `<html lang>` is rewritten per request with the same priority.
 * So `?lang=<code>` is the only override channel the spec provides for anonymous visitors: it writes nothing to the DB and travels with the address.
 *
 * Logged-in users do not go through here - their language is persisted in /settings (PATCH /api/v1/profile),
 * and letting the URL override the account setting long-term would only make the two disagree.
 */
export const LANGUAGE_PARAM = 'lang';

/**
 * Read the language override parameter from the address; returns null outside a browser environment or when the parameter is absent.
 * search can be passed explicitly, for tests and for evaluating the target address within the same navigation.
 */
export function readLanguageFromURL(
  search: string = typeof window === 'undefined' ? '' : window.location.search
): string | null {
  const value = new URLSearchParams(search).get(LANGUAGE_PARAM);
  if (value === null || value.trim() === '') return null;
  return value;
}

/**
 * Write the language into the current address (swaps only the query string, does not trigger routing).
 * Uses replaceState rather than pushState: language is a display preference and should not add a layer to the browser's back/forward history.
 */
export function setLanguageInURL(code: string): void {
  if (typeof window === 'undefined') return;
  const url = new URL(window.location.href);
  url.searchParams.set(LANGUAGE_PARAM, code);
  window.history.replaceState({}, '', url);
}

/**
 * Remove the language override parameter from the address (does nothing when it is absent).
 *
 * For logged-in users the account setting wins (switching persists via PATCH /api/v1/settings/locale), while a leftover
 * `?lang=` in the address has top priority in server resolution - it must be cleared after a successful header switch, otherwise the URL fights the account setting.
 */
export function clearLanguageInURL(): void {
  if (typeof window === 'undefined') return;
  const url = new URL(window.location.href);
  if (!url.searchParams.has(LANGUAGE_PARAM)) return;
  url.searchParams.delete(LANGUAGE_PARAM);
  window.history.replaceState({}, '', url);
}
