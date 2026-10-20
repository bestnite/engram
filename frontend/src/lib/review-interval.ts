/**
 * 把评分按钮下的「下次间隔」秒数写成一个短词（10 分钟、3 天、1.5 个月）。
 *
 * 只取一个单位、按量级挑：按钮下面只有一行小字，「1 天 3 小时」这种精确写法既放不下，也不是
 * 做选择时需要的信息——用户要比较的是四档之间差多少个量级。月按 30 天、年按 365 天折算，
 * 只用于显示；一位小数足够区分相邻两档，整数时不带「.0」。
 */

/** 翻译函数的最小形状（`$t` 即可）。 */
export type IntervalTranslate = (key: string, params?: Record<string, string | number>) => string;

const MINUTE = 60;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

/** 保留一位小数，整数不带「.0」。 */
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
