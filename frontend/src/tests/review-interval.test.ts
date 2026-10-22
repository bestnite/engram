import { describe, it, expect } from 'vitest';
import { get } from 'svelte/store';
import { formatInterval } from '../lib/review-interval';
import { setLocale, t } from '../lib/i18n';

/** The interval under the rating buttons picks a single unit by magnitude; months as 30 days and years as 365 days, one decimal place, and integers without a trailing ".0". */
describe('formatInterval', () => {
  const day = 86400;
  const cases: Array<[number, string, string]> = [
    [0, '<1 分钟', '<1m'],
    [59, '<1 分钟', '<1m'],
    [60, '1 分钟', '1m'],
    [600, '10 分钟', '10m'],
    [3599, '60 分钟', '60m'],
    [3600, '1 小时', '1h'],
    [5 * 3600, '5 小时', '5h'],
    [day, '1 天', '1d'],
    [3.4 * day, '3 天', '3d'],
    [29 * day, '29 天', '29d'],
    [45 * day, '1.5 个月', '1.5mo'],
    [60 * day, '2 个月', '2mo'],
    [400 * day, '1.1 年', '1.1y'],
    [730 * day, '2 年', '2y'],
  ];

  it.each(cases)('formats %d seconds', (seconds, zh, en) => {
    setLocale('zh-CN');
    expect(formatInterval(seconds, get(t))).toBe(zh);
    setLocale('en');
    expect(formatInterval(seconds, get(t))).toBe(en);
  });
});
