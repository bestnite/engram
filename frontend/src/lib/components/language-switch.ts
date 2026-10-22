import { get } from 'svelte/store';
import { authStore } from '../auth';
import { apiClient } from '../api';
import { isSupportedLocale, localeStore, setLocale } from '../i18n';
import { clearLanguageInURL, setLanguageInURL } from '../i18n/url';

/**
 * Switch the UI language. The header language dropdown and the sidebar account menu share this logic, with two identities and two persistence targets:
 *
 * - Logged out: there is no account to write to, so the choice goes into the address as `?lang=<code>` (the server resolves the language per request with "?lang first"),
 *   which survives refreshes and sharing the address with someone else, and is not persisted to the DB;
 * - Logged in: `PATCH /api/v1/settings/locale` writes `users.locale`, the same column the /settings language field writes;
 *   at the same time any leftover `?lang=` in the address is cleared - it has the highest priority in the server's resolution and would otherwise override the freshly written
 *   account setting (surfacing as "switch the language, refresh, and it reverts").
 *
 * On failure it does not leave the illusion that "the UI changed but the DB did not": it rolls back to the previous language and returns false, and the caller shows a notice.
 */
export async function switchLocale(value: string): Promise<boolean> {
  if (!isSupportedLocale(value)) return true;
  const previous = get(localeStore);
  if (value === previous) return true;
  setLocale(value);

  if (!get(authStore).authenticated) {
    setLanguageInURL(value);
    return true;
  }

  clearLanguageInURL();
  try {
    await apiClient.updateLocale(value);
    return true;
  } catch {
    setLocale(previous);
    return false;
  }
}
