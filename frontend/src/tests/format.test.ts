import { describe, expect, it } from 'vitest';
import { fieldText, formatBytes, formatDate, formatNumber, formatPercent } from '../lib/format';

describe('shared formatting', () => {
  it.each([
    [1234567, 'en', '1,234,567'],
    [0, 'zh-CN', '0'],
  ])('formatNumber(%s, %s)', (value, locale, want) => {
    expect(formatNumber(value, locale)).toBe(want);
  });

  it.each([
    [0.9482758620689655, 'en', '94.8%'],
    [1, 'zh-CN', '100.0%'],
    [0, 'en', '0.0%'],
  ])('formatPercent(%s, %s)', (rate, locale, want) => {
    expect(formatPercent(rate, locale)).toBe(want);
  });

  it.each([
    [0, '0 B'],
    [1023, '1,023 B'],
    [1536, '1.5 KiB'],
    [5 * 1024 * 1024, '5 MiB'],
    [-1, '0 B'],
  ])('formatBytes(%s)', (bytes, want) => {
    expect(formatBytes(bytes, 'en')).toBe(want);
  });

  it('formatDate formats a timestamp and passes an unparsable value through', () => {
    expect(formatDate('2026-10-02T12:00:00Z', 'en')).toBe('Oct 2, 2026');
    expect(formatDate('not a date', 'en')).toBe('not a date');
  });

  it.each([
    [undefined, ''],
    [null, ''],
    ['text', 'text'],
    [3, '3'],
    [true, 'true'],
    [['a', 'b'], 'a, b'],
    [[{ x: 1 }], '{"x":1}'],
    [{ x: 1 }, '{\n  "x": 1\n}'],
  ])('fieldText(%j)', (value, want) => {
    expect(fieldText(value)).toBe(want);
  });
});
