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

/**
 * The timezone a time is shown in: the account's stored name, or UTC when it is empty or this browser cannot parse it.
 * UTC is the server's own fallback for the same two cases, so the page and the server agree on which day an instant
 * belongs to.
 */
export function resolveTimeZone(name: string | null | undefined): string {
  const tz = (name ?? '').trim();
  if (tz === '') return 'UTC';
  try {
    new Intl.DateTimeFormat('en', { timeZone: tz });
    return tz;
  } catch {
    return 'UTC';
  }
}

/** The wall-clock parts of an instant in a timezone. */
function zonedParts(ms: number, timeZone: string): { year: number; month: number; day: number; hour: number; minute: number; second: number } {
  const parts = new Intl.DateTimeFormat('en-US', {
    timeZone,
    hourCycle: 'h23',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  }).formatToParts(new Date(ms));
  const get = (type: string) => Number(parts.find((p) => p.type === type)?.value ?? 0);
  return { year: get('year'), month: get('month'), day: get('day'), hour: get('hour'), minute: get('minute'), second: get('second') };
}

/** The date of an instant (now by default) as `YYYY-MM-DD` in a timezone; an instant that is not a number reads as empty. */
export function dayIn(timeZone: string, at: number = Date.now()): string {
  if (!Number.isFinite(at)) return '';
  const p = zonedParts(at, resolveTimeZone(timeZone));
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${p.year}-${pad(p.month)}-${pad(p.day)}`;
}

/**
 * The instant at which a timezone's clock reads the given wall time. The zone's offset is read at a first guess and
 * read again at the corrected instant, because the guess can sit on the other side of a daylight-saving change.
 */
export function zonedInstant(year: number, month: number, day: number, hour: number, minute: number, second: number, timeZone: string): Date {
  const tz = resolveTimeZone(timeZone);
  const wall = Date.UTC(year, month - 1, day, hour, minute, second);
  const offsetAt = (ms: number) => {
    const p = zonedParts(ms, tz);
    return Date.UTC(p.year, p.month - 1, p.day, p.hour, p.minute, p.second) - ms;
  };
  let instant = wall - offsetAt(wall);
  instant = wall - offsetAt(instant);
  return new Date(instant);
}
