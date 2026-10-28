import { isSupportedLocale } from '../i18n';
import type { ProfileValidationResult } from './types';

/**
 * Common timezone suggestion candidates (kept in sync with the Go backend's internal/web/settings.go:commonTimezones)
 */
export const COMMON_TIMEZONES = [
  'UTC',
  'Asia/Shanghai',
  'Asia/Hong_Kong',
  'Asia/Taipei',
  'Asia/Tokyo',
  'Asia/Seoul',
  'Asia/Singapore',
  'Europe/London',
  'Europe/Paris',
  'Europe/Berlin',
  'America/New_York',
  'America/Chicago',
  'America/Los_Angeles',
  'Australia/Sydney',
] as const;

/**
 * Validate display name: non-empty (after Trim)
 */
export function validateDisplayName(displayName: string | null | undefined): {
  valid: boolean;
  value: string;
  errorKey?: string;
} {
  const trimmed = (displayName ?? '').trim();
  if (trimmed === '') {
    return { valid: false, value: '', errorKey: 'settings.error.display_name_required' };
  }
  return { valid: true, value: trimmed };
}

/**
 * Validate UI language code: must be one of the supported language codes
 */
export function validateLocale(locale: string | null | undefined): {
  valid: boolean;
  value: string;
  errorKey?: string;
} {
  const trimmed = (locale ?? '').trim();
  if (!isSupportedLocale(trimmed)) {
    return { valid: false, value: trimmed, errorKey: 'settings.error.locale_invalid' };
  }
  return { valid: true, value: trimmed };
}

/**
 * Validate timezone: must be a standard IANA timezone format and resolvable by the environment
 */
export function validateTimezone(timezone: string | null | undefined): {
  valid: boolean;
  value: string;
  errorKey?: string;
} {
  const trimmed = (timezone ?? '').trim();
  if (trimmed === '') {
    return { valid: false, value: '', errorKey: 'settings.error.timezone_invalid' };
  }
  try {
    Intl.DateTimeFormat(undefined, { timeZone: trimmed });
    return { valid: true, value: trimmed };
  } catch {
    return { valid: false, value: trimmed, errorKey: 'settings.error.timezone_invalid' };
  }
}

/**
 * Server default day cutoff (the local day changes at 04:00), same value as DefaultDayCutoffHour in Go: internal/store/stats.go.
 *
 * The UI no longer offers an "unset" option: the cutoff must be a concrete hour. Old accounts with NULL in the DB display this value,
 * and saving once makes it explicit (both produce the same effect, so this is not a behavior change).
 */
export const DEFAULT_DAY_CUTOFF_HOUR = 4;

/**
 * Validate the review day cutoff hour: null or an empty string means unset (stored as NULL, treated as 04:00); a provided value must be
 * an integer in 0-23 (0 means midnight). Same rule as ResolveCutoff in Go: internal/store/stats.go.
 */
export function validateDayCutoffHour(raw: number | string | null | undefined): {
  valid: boolean;
  value: number | null;
  errorKey?: string;
} {
  if (raw === null || raw === undefined) {
    return { valid: true, value: null };
  }
  if (typeof raw === 'string') {
    const trimmed = raw.trim();
    if (trimmed === '') {
      return { valid: true, value: null };
    }
    const num = Number(trimmed);
    if (!Number.isInteger(num) || num < 0 || num > 23 || !/^\d+$/.test(trimmed)) {
      return { valid: false, value: null, errorKey: 'settings.error.cutoff_invalid' };
    }
    return { valid: true, value: num };
  }
  if (typeof raw === 'number') {
    if (!Number.isInteger(raw) || raw < 0 || raw > 23) {
      return { valid: false, value: null, errorKey: 'settings.error.cutoff_invalid' };
    }
    return { valid: true, value: raw };
  }
  return { valid: false, value: null, errorKey: 'settings.error.cutoff_invalid' };
}

/** Default and maximum of the early-study window (minutes), same rule as ResolveLearnAhead in Go: internal/store/stats.go. */
export const DEFAULT_LEARN_AHEAD_MINUTES = 0;
export const MAX_LEARN_AHEAD_MINUTES = 1440;

/**
 * Validate the early-study window (minutes): an empty string means use the default (stored as NULL); a provided value must be
 * an integer in 0-1440 (0 means off).
 */
function validateLearnAheadMinutes(raw: string | null | undefined): {
  valid: boolean;
  value: number | null;
  errorKey?: string;
} {
  const trimmed = (raw ?? '').trim();
  if (trimmed === '') {
    return { valid: true, value: null };
  }
  const num = Number(trimmed);
  if (!/^\d+$/.test(trimmed) || !Number.isInteger(num) || num > MAX_LEARN_AHEAD_MINUTES) {
    return { valid: false, value: null, errorKey: 'settings.error.learn_ahead_invalid' };
  }
  return { valid: true, value: num };
}

/**
 * Validate that the two password entries match (shared by registration and first-admin bootstrap).
 *
 * Only responsible for the one purely frontend rule "do the two entries match": password strength and other rules are decided by the server policy,
 * not reimplemented here, and this carries no server semantics (the request body has only the password field).
 * Returns a localized error key; when they match errorKey is undefined.
 */
export function validatePasswordConfirmation(password: string, confirmation: string): {
  valid: boolean;
  errorKey?: string;
} {
  if (password !== confirmation) {
    return { valid: false, errorKey: 'auth.error.password_mismatch' };
  }
  return { valid: true };
}

/**
 * Validate the whole profile form
 */
export function validateProfileForm(form: {
  display_name: string | null | undefined;
  locale: string | null | undefined;
  timezone: string | null | undefined;
  day_cutoff_hour: number | string | null | undefined;
  learn_ahead_minutes?: string | null | undefined;
}): ProfileValidationResult {
  const errors: ProfileValidationResult['errors'] = {};

  const nameRes = validateDisplayName(form.display_name);
  if (!nameRes.valid && nameRes.errorKey) {
    errors.display_name = nameRes.errorKey;
  }

  const localeRes = validateLocale(form.locale);
  if (!localeRes.valid && localeRes.errorKey) {
    errors.locale = localeRes.errorKey;
  }

  const tzRes = validateTimezone(form.timezone);
  if (!tzRes.valid && tzRes.errorKey) {
    errors.timezone = tzRes.errorKey;
  }

  const cutoffRes = validateDayCutoffHour(form.day_cutoff_hour);
  if (!cutoffRes.valid && cutoffRes.errorKey) {
    errors.day_cutoff_hour = cutoffRes.errorKey;
  }

  const aheadRes = validateLearnAheadMinutes(form.learn_ahead_minutes);
  if (!aheadRes.valid && aheadRes.errorKey) {
    errors.learn_ahead_minutes = aheadRes.errorKey;
  }

  const valid = Object.keys(errors).length === 0;
  if (!valid) {
    return { valid: false, errors };
  }

  return {
    valid: true,
    errors: {},
    data: {
      display_name: nameRes.value,
      locale: localeRes.value,
      timezone: tzRes.value,
      day_cutoff_hour: cutoffRes.value,
      learn_ahead_minutes: aheadRes.value,
    },
  };
}
