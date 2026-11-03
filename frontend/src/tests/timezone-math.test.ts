import { describe, expect, it } from 'vitest';
import { dayIn, resolveTimeZone, zonedInstant } from '../lib/timezones';

/**
 * Timezone arithmetic for the account's clock: which day an instant falls on, and which instant a wall time names.
 * An empty or unknown name reads as UTC, the server's own fallback, so page and server agree on the day.
 */
describe('timezone arithmetic', () => {
  it.each([
    ['Asia/Shanghai', 'Asia/Shanghai'],
    ['UTC', 'UTC'],
    ['', 'UTC'],
    ['  ', 'UTC'],
    ['Mars/Phobos', 'UTC'],
  ])('resolveTimeZone(%j) = %s', (name, want) => {
    expect(resolveTimeZone(name)).toBe(want);
  });

  it.each([
    ['UTC', '2026-01-01'],
    ['Asia/Shanghai', '2026-01-02'],
    ['America/New_York', '2026-01-01'],
    ['Mars/Phobos', '2026-01-01'],
  ])('dayIn(%s) places 2026-01-01T20:00Z on %s', (tz, want) => {
    expect(dayIn(tz, Date.parse('2026-01-01T20:00:00Z'))).toBe(want);
  });

  it('dayIn reads a value that is not a number as empty', () => {
    expect(dayIn('UTC', Date.parse('not a date'))).toBe('');
  });

  it.each([
    // Standard and daylight time on either side of a change, so the offset is read at the corrected instant.
    ['America/New_York', 2026, 1, 15, '2026-01-16T04:59:59.000Z'],
    ['America/New_York', 2026, 7, 15, '2026-07-16T03:59:59.000Z'],
    ['America/New_York', 2026, 3, 8, '2026-03-09T03:59:59.000Z'],
    ['Asia/Shanghai', 2099, 5, 4, '2099-05-04T15:59:59.000Z'],
    ['', 2099, 5, 4, '2099-05-04T23:59:59.000Z'],
  ])('zonedInstant end of day in %s on %d-%d-%d', (tz, y, m, d, want) => {
    expect(zonedInstant(y, m, d, 23, 59, 59, tz).toISOString()).toBe(want);
  });
});
