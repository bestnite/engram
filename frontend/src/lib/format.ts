import { dayIn, resolveTimeZone } from './timezones';

/**
 * The single place the UI turns numbers, ratios, byte counts, dates and raw field values into text. Every page formats
 * the same kind of quantity the same way, in the current locale; a component passes the locale it reads from
 * `$localeStore`, so the text follows a language switch. A date or time is shown in the account's timezone, which a
 * component reads from `$timeZoneStore`, never in the browser's: the server decides days in the account's timezone too.
 */

/** A count or measurement with the locale's digit grouping. */
export function formatNumber(value: number, locale: string): string {
  return new Intl.NumberFormat(locale).format(value);
}

/** A 0–1 ratio as a percentage with one decimal place, the sign placed the way the locale writes it. */
export function formatPercent(rate: number, locale: string): string {
  return new Intl.NumberFormat(locale, {
    style: 'percent',
    minimumFractionDigits: 1,
    maximumFractionDigits: 1,
  }).format(rate);
}

const byteUnits = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];

/** A byte count in binary units (1 KiB = 1024 B): whole bytes, then at most one decimal place. */
export function formatBytes(bytes: number, locale: string): string {
  let value = Math.max(0, bytes);
  let unit = 0;
  while (value >= 1024 && unit < byteUnits.length - 1) {
    value /= 1024;
    unit += 1;
  }
  const digits = unit === 0 ? 0 : 1;
  return `${new Intl.NumberFormat(locale, { maximumFractionDigits: digits }).format(value)} ${byteUnits[unit]}`;
}

/**
 * A duration given in milliseconds, in the largest unit that keeps it readable: milliseconds below a second, seconds
 * below a minute, minutes below an hour, hours above, with at most one decimal place. `long` spells the unit out
 * ("10 minutes"), `short` abbreviates it ("10 min").
 */
export function formatDuration(ms: number, locale: string, unitDisplay: 'long' | 'short' = 'short'): string {
  const value = Math.max(0, ms);
  const steps: Array<[number, string]> = [
    [3_600_000, 'hour'],
    [60_000, 'minute'],
    [1_000, 'second'],
  ];
  for (const [size, unit] of steps) {
    if (value >= size) {
      return new Intl.NumberFormat(locale, { style: 'unit', unit, unitDisplay, maximumFractionDigits: 1 }).format(value / size);
    }
  }
  return new Intl.NumberFormat(locale, { style: 'unit', unit: 'millisecond', unitDisplay, maximumFractionDigits: 0 }).format(value);
}

/**
 * A calendar date (medium style) in the given timezone from an ISO timestamp or a Date; a value that does not parse is
 * shown as it came. An empty or unknown timezone means UTC.
 */
export function formatDate(value: string | Date, locale: string, timeZone: string): string {
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) return String(value);
  return new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeZone: resolveTimeZone(timeZone) }).format(date);
}

/** A calendar date as `YYYY-MM-DD` in the given timezone, for a narrow column; a value that does not parse reads as empty. */
export function formatIsoDay(value: string, timeZone: string): string {
  return dayIn(timeZone, Date.parse(value));
}

/** A date with its time to the minute (medium date, short time) in the given timezone; otherwise as formatDate. */
export function formatDateTime(value: string | Date, locale: string, timeZone: string): string {
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) return String(value);
  return new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeStyle: 'short', timeZone: resolveTimeZone(timeZone) }).format(date);
}

/**
 * The plain text of a raw note field value, for places that show a field without its rendered HTML: text as it is,
 * numbers and booleans as written, a list as its items joined by commas, anything else as indented JSON. Missing
 * values read as empty.
 */
export function fieldText(value: unknown): string {
  if (value === null || value === undefined) return '';
  if (typeof value === 'string') return value;
  if (typeof value === 'number' || typeof value === 'boolean') return String(value);
  if (Array.isArray(value)) {
    return value.map((item) => (typeof item === 'object' && item !== null ? JSON.stringify(item) : String(item))).join(', ');
  }
  return JSON.stringify(value, null, 2);
}

/** Character references a field summary decodes by name; every other named reference is kept as written. */
const namedEntities: Record<string, string> = { amp: '&', lt: '<', gt: '>', quot: '"', apos: "'", nbsp: ' ' };

/**
 * The text of a field value's HTML on one line, for a list row that shows a summary instead of the rendered face.
 *
 * The rule follows what a reader sees on the card: a ruby annotation (`<rt>`, `<rp>`) is left out so the base reads as
 * one word, a speech tag is dropped because its text is already shown in front of it, a sound tag is dropped because it
 * is a file name, and markup is removed with block boundaries kept as spaces. The result is plain text: the caller
 * shows it as text, so no stored markup reaches the page. A regular expression is enough, and it runs where no DOM
 * exists (the server-rendered tests); a parser would only change how malformed markup is cut.
 */
export function htmlToText(html: string): string {
  return html
    .replace(/<(script|style|rt|rp)\b[^>]*>[\s\S]*?<\/\1\s*>/gi, '')
    .replace(/\[anki:tts\b[^\]]*\][\s\S]*?\[\/anki:tts\]/gi, '')
    .replace(/\[sound:[^\]]*\]/gi, '')
    .replace(/<(br|\/?(p|div|li|ul|ol|h[1-6]|tr|td|th|blockquote|pre|table))\b[^>]*>/gi, ' ')
    .replace(/<[^>]*>/g, '')
    .replace(/&(#x[0-9a-f]+|#[0-9]+|[a-z]+);/gi, (match, ref: string) => {
      if (ref[0] !== '#') return namedEntities[ref.toLowerCase()] ?? match;
      const code = ref[1] === 'x' || ref[1] === 'X' ? Number.parseInt(ref.slice(2), 16) : Number.parseInt(ref.slice(1), 10);
      return Number.isFinite(code) && code > 0 && code <= 0x10ffff ? String.fromCodePoint(code) : match;
    })
    .replace(/\s+/g, ' ')
    .trim();
}
