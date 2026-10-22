/**
 * Render the "next interval" seconds under a grade button as one short phrase (10 minutes, 3 days, 1.5 months).
 *
 * Only one unit, chosen by magnitude: there is just one small line under the button, and a precise form like "1 day 3 hours" neither fits
 * nor is the information needed to choose - what the user compares is how many orders of magnitude separate the four grades. Months are 30 days and years 365 days,
 * used for display only; one decimal is enough to tell adjacent grades apart, and whole numbers carry no ".0".
 */

/** Minimal shape of a translation function (`$t` is enough). */
export type IntervalTranslate = (key: string, params?: Record<string, string | number>) => string;

const MINUTE = 60;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

/** Keep one decimal; whole numbers carry no ".0". */
function oneDecimal(value: number): string {
  const rounded = Math.round(value * 10) / 10;
  return Number.isInteger(rounded) ? String(rounded) : rounded.toFixed(1);
}

export function formatInterval(seconds: number, t: IntervalTranslate): string {
  if (seconds < MINUTE) return t('review.interval.under_minute');
  if (seconds < HOUR) return t('review.interval.minutes', { n: Math.round(seconds / MINUTE) });
  if (seconds < DAY) return t('review.interval.hours', { n: Math.round(seconds / HOUR) });
  const days = seconds / DAY;
  if (days < 30) return t('review.interval.days', { n: Math.max(1, Math.round(days)) });
  if (days < 365) return t('review.interval.months', { n: oneDecimal(days / 30) });
  return t('review.interval.years', { n: oneDecimal(days / 365) });
}
