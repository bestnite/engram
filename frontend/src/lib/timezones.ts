import { COMMON_TIMEZONES } from './api/validation';
import type { SelectOption } from './components/ui/options';

/**
 * The full set of IANA timezones supported by the browser.
 *
 * It uses `Intl.supportedValuesOf('timeZone')` instead of copying another list into the repo: what it returns is
 * exactly the names the current browser can parse, the same source as the frontend validation (validateTimezone
 * tries a parse with Intl.DateTimeFormat), whereas a hand-copied list is bound to drift. Old browsers lack this
 * API and get an empty array, with the common list as the fallback.
 */
function browserTimeZones(): string[] {
  try {
    const supported = (Intl as { supportedValuesOf?: (key: string) => string[] }).supportedValuesOf;
    if (typeof supported !== 'function') return [];
    return supported.call(Intl, 'timeZone');
  } catch {
    return [];
  }
}

/**
 * Build the timezone candidates: UTC and the common timezones pinned to the top (in common order), the rest after them in alphabetical order.
 *
 * Three things cannot be skipped: ① `Intl.supportedValuesOf` does not include UTC, so it must be added explicitly; ② dedupe, because the common list
 * is by definition a subset of the full list; ③ **always merge in currentValue** — the database may hold a historical name this browser does not
 * recognize, and without merging it in this field would render empty and the user's save would rewrite it.
 */
export function timezoneOptions(currentValue?: string): SelectOption[] {
  const pinned = ['UTC', ...COMMON_TIMEZONES.filter((tz) => tz !== 'UTC')];
  const seen = new Set<string>();
  const out: SelectOption[] = [];

  const push = (tz: string): void => {
    const name = tz.trim();
    if (name === '' || seen.has(name)) return;
    seen.add(name);
    out.push({ value: name, label: name });
  };

  pinned.forEach(push);
  if (currentValue) push(currentValue);
  browserTimeZones()
    .slice()
    .sort((a, b) => a.localeCompare(b))
    .forEach(push);
  return out;
}
