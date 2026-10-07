import { describe, it, expect } from 'vitest';
import {
  validateDisplayName,
  validateLocale,
  validateTimezone,
  validateDayCutoffHour,
  validateProfileForm,
  COMMON_TIMEZONES,
} from '../lib/api/validation';

describe('Profile and settings form validation', () => {
  describe('validateDisplayName', () => {
    it('rejects empty or whitespace-only display name', () => {
      expect(validateDisplayName('')).toEqual({
        valid: false,
        value: '',
        errorKey: 'settings.error.display_name_required',
      });
      expect(validateDisplayName('   ')).toEqual({
        valid: false,
        value: '',
        errorKey: 'settings.error.display_name_required',
      });
      expect(validateDisplayName(null)).toEqual({
        valid: false,
        value: '',
        errorKey: 'settings.error.display_name_required',
      });
      expect(validateDisplayName(undefined)).toEqual({
        valid: false,
        value: '',
        errorKey: 'settings.error.display_name_required',
      });
    });

    it('accepts valid display name and trims leading/trailing whitespace', () => {
      expect(validateDisplayName('Alice')).toEqual({
        valid: true,
        value: 'Alice',
      });
      expect(validateDisplayName('  Bob Smith  ')).toEqual({
        valid: true,
        value: 'Bob Smith',
      });
    });
  });

  describe('validateLocale', () => {
    it('accepts supported locales (zh-CN and en)', () => {
      expect(validateLocale('zh-CN')).toEqual({ valid: true, value: 'zh-CN' });
      expect(validateLocale('en')).toEqual({ valid: true, value: 'en' });
      expect(validateLocale('  zh-CN  ')).toEqual({ valid: true, value: 'zh-CN' });
    });

    it('rejects unsupported locales with localized error key', () => {
      expect(validateLocale('fr')).toEqual({
        valid: false,
        value: 'fr',
        errorKey: 'settings.error.locale_invalid',
      });
      expect(validateLocale('ja-JP')).toEqual({
        valid: false,
        value: 'ja-JP',
        errorKey: 'settings.error.locale_invalid',
      });
      expect(validateLocale('')).toEqual({
        valid: false,
        value: '',
        errorKey: 'settings.error.locale_invalid',
      });
      expect(validateLocale(null)).toEqual({
        valid: false,
        value: '',
        errorKey: 'settings.error.locale_invalid',
      });
    });
  });

  describe('validateTimezone', () => {
    it('accepts common and standard IANA timezones', () => {
      expect(validateTimezone('Asia/Shanghai')).toEqual({
        valid: true,
        value: 'Asia/Shanghai',
      });
      expect(validateTimezone('UTC')).toEqual({
        valid: true,
        value: 'UTC',
      });
      expect(validateTimezone('America/New_York')).toEqual({
        valid: true,
        value: 'America/New_York',
      });
      expect(validateTimezone('Europe/London')).toEqual({
        valid: true,
        value: 'Europe/London',
      });
    });

    it('all COMMON_TIMEZONES pass timezone validation', () => {
      for (const tz of COMMON_TIMEZONES) {
        const res = validateTimezone(tz);
        expect(res.valid).toBe(true);
      }
    });

    it('rejects invalid or empty timezones with localized error key', () => {
      expect(validateTimezone('Mars/Phobos')).toEqual({
        valid: false,
        value: 'Mars/Phobos',
        errorKey: 'settings.error.timezone_invalid',
      });
      expect(validateTimezone('Invalid/Zone')).toEqual({
        valid: false,
        value: 'Invalid/Zone',
        errorKey: 'settings.error.timezone_invalid',
      });
      expect(validateTimezone('')).toEqual({
        valid: false,
        value: '',
        errorKey: 'settings.error.timezone_invalid',
      });
      expect(validateTimezone('   ')).toEqual({
        valid: false,
        value: '',
        errorKey: 'settings.error.timezone_invalid',
      });
      expect(validateTimezone(null)).toEqual({
        valid: false,
        value: '',
        errorKey: 'settings.error.timezone_invalid',
      });
    });
  });

  describe('validateDayCutoffHour (Go: store.ResolveCutoff)', () => {
    it('accepts empty/null/undefined as valid null (defaults to 04:00)', () => {
      expect(validateDayCutoffHour(null)).toEqual({ valid: true, value: null });
      expect(validateDayCutoffHour(undefined)).toEqual({ valid: true, value: null });
      expect(validateDayCutoffHour('')).toEqual({ valid: true, value: null });
      expect(validateDayCutoffHour('   ')).toEqual({ valid: true, value: null });
    });

    it('accepts 0 (midnight) as explicit 0', () => {
      expect(validateDayCutoffHour(0)).toEqual({ valid: true, value: 0 });
      expect(validateDayCutoffHour('0')).toEqual({ valid: true, value: 0 });
    });

    it('accepts valid hour values between 0 and 23', () => {
      expect(validateDayCutoffHour(4)).toEqual({ valid: true, value: 4 });
      expect(validateDayCutoffHour('4')).toEqual({ valid: true, value: 4 });
      expect(validateDayCutoffHour(23)).toEqual({ valid: true, value: 23 });
      expect(validateDayCutoffHour('23')).toEqual({ valid: true, value: 23 });
    });

    it('rejects out-of-range, non-integer, and non-numeric inputs', () => {
      expect(validateDayCutoffHour(-1)).toEqual({
        valid: false,
        value: null,
        errorKey: 'settings.error.cutoff_invalid',
      });
      expect(validateDayCutoffHour(24)).toEqual({
        valid: false,
        value: null,
        errorKey: 'settings.error.cutoff_invalid',
      });
      expect(validateDayCutoffHour('24')).toEqual({
        valid: false,
        value: null,
        errorKey: 'settings.error.cutoff_invalid',
      });
      expect(validateDayCutoffHour(4.5)).toEqual({
        valid: false,
        value: null,
        errorKey: 'settings.error.cutoff_invalid',
      });
      expect(validateDayCutoffHour('4.5')).toEqual({
        valid: false,
        value: null,
        errorKey: 'settings.error.cutoff_invalid',
      });
      expect(validateDayCutoffHour('abc')).toEqual({
        valid: false,
        value: null,
        errorKey: 'settings.error.cutoff_invalid',
      });
    });
  });

  describe('validateProfileForm comprehensive validation', () => {
    it('returns valid: true and cleaned data for valid input', () => {
      const res = validateProfileForm({
        display_name: '  Tester  ',
        locale: 'zh-CN',
        timezone: 'Asia/Shanghai',
        day_cutoff_hour: '4',
      });

      expect(res.valid).toBe(true);
      expect(res.errors).toEqual({});
      expect(res.data).toEqual({
        display_name: 'Tester',
        locale: 'zh-CN',
        timezone: 'Asia/Shanghai',
        day_cutoff_hour: 4,
      });
    });

    it('handles blank day_cutoff_hour as null in valid result', () => {
      const res = validateProfileForm({
        display_name: 'Tester',
        locale: 'en',
        timezone: 'UTC',
        day_cutoff_hour: '',
      });

      expect(res.valid).toBe(true);
      expect(res.data?.day_cutoff_hour).toBeNull();
    });

    it('aggregates multiple field errors simultaneously', () => {
      const res = validateProfileForm({
        display_name: '',
        locale: 'fr',
        timezone: 'Mars/Phobos',
        day_cutoff_hour: '99',
      });

      expect(res.valid).toBe(false);
      expect(res.data).toBeUndefined();
      expect(res.errors).toEqual({
        display_name: 'settings.error.display_name_required',
        locale: 'settings.error.locale_invalid',
        timezone: 'settings.error.timezone_invalid',
        day_cutoff_hour: 'settings.error.cutoff_invalid',
      });
    });
  });
});
