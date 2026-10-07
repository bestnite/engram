import { describe, it, expect, afterEach, vi } from 'vitest';
import { timezoneOptions } from '../lib/timezones';
import { COMMON_TIMEZONES, validateTimezone } from '../lib/api/validation';

/**
 * 时区候选列表：候选来自浏览器的 IANA 全量列表，加常用时区置顶与当前值兜底。
 * 这里的断言钉的是「列表里的每个值都是这个浏览器认得的时区」——列表与校验器必须同源，
 * 否则用户会在下拉里选中一个保存时被判非法。
 */
describe('timezone options', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('puts UTC and the common zones first, in the common order', () => {
    const options = timezoneOptions();
    expect(options[0]?.value).toBe('UTC');
    expect(options.slice(0, COMMON_TIMEZONES.length).map((o) => o.value)).toEqual([...COMMON_TIMEZONES]);
  });

  it('offers the full IANA list the browser can resolve, without duplicates', () => {
    const values = timezoneOptions().map((o) => o.value);
    expect(values.length).toBeGreaterThan(COMMON_TIMEZONES.length);
    expect(new Set(values).size).toBe(values.length);
  });

  it('accepts every offered zone, including the bulk beyond the common list', () => {
    for (const option of timezoneOptions()) {
      expect(validateTimezone(option.value).valid, `${option.value} 应能通过时区校验`).toBe(true);
    }
  });

  it('keeps the stored value near the top even when this browser does not know it', () => {
    // 库里的历史名字可能不在浏览器列表内：不并进来，字段就会显示成空、一保存就被改写。
    const options = timezoneOptions('Legacy/Zone');
    const pinnedAt = options.findIndex((o) => o.value === 'Legacy/Zone');
    const bulkAt = options.findIndex((o) => o.value === 'Africa/Abidjan');
    expect(pinnedAt).toBeGreaterThan(-1);
    expect(pinnedAt).toBeLessThan(bulkAt);
    expect(options).toHaveLength(timezoneOptions().length + 1);
  });

  it('falls back to the pinned list when Intl.supportedValuesOf is missing', () => {
    const original = Object.getOwnPropertyDescriptor(Intl, 'supportedValuesOf');
    Object.defineProperty(Intl, 'supportedValuesOf', { value: undefined, configurable: true });
    try {
      expect(timezoneOptions().map((o) => o.value)).toEqual([...COMMON_TIMEZONES]);
    } finally {
      if (original) Object.defineProperty(Intl, 'supportedValuesOf', original);
    }
  });

  it('falls back to the pinned list when Intl.supportedValuesOf throws', () => {
    vi.spyOn(Intl, 'supportedValuesOf').mockImplementation(() => {
      throw new Error('unsupported');
    });
    expect(timezoneOptions().map((o) => o.value)).toEqual([...COMMON_TIMEZONES]);
  });
});
