/**
 * The single place the UI turns numbers, ratios, byte counts, dates and raw field values into text. Every page formats
 * the same kind of quantity the same way, in the current locale; a component passes the locale it reads from
 * `$localeStore`, so the text follows a language switch.
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

/** A calendar date (medium style) from an ISO timestamp or a Date; a value that does not parse is shown as it came. */
export function formatDate(value: string | Date, locale: string): string {
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) return String(value);
  return new Intl.DateTimeFormat(locale, { dateStyle: 'medium' }).format(date);
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
