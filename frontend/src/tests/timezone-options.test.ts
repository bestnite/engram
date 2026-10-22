import { describe, it, expect, afterEach, vi } from 'vitest';
import { timezoneOptions } from '../lib/timezones';
import { COMMON_TIMEZONES, validateTimezone } from '../lib/api/validation';

/**
 * Timezone candidate list: candidates come from the browser's full IANA list, with common
 * zones pinned to the top and the current value as a fallback. The assertion here pins that
 * "every value in the list is a timezone this browser recognises" — the list and the
 * validator must share one source, otherwise the user picks a value from the dropdown that
 * is rejected as invalid on save.
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
    // A historical name from the library may be missing from the browser's list: if it is not merged in, the field shows empty and gets rewritten on every save.
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
