import { isSupportedLocale } from '../i18n';
import type { ProfileValidationResult } from './types';

/**
 * 常用时区建议候选列表（与 Go 后端 internal/web/settings.go:commonTimezones 保持一致）
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
 * 校验显示名：非空（Trim 后）
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
 * 校验界面语言代码：必须为受支持的语言码之一
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
 * 校验时区：必须为标准 IANA 时区格式且能被环境正确解析
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
 * 服务端默认日切点（本地 04:00 换日），与 Go: internal/store/stats.go 的 DefaultDayCutoffHour 同值。
 *
 * 界面不再提供「未设置」这一项：切点必须是一个具体整点。库里为 NULL 的旧账号按这个值显示，
 * 保存一次即成为显式值（两者生效结果相同，所以这不是行为变化）。
 */
export const DEFAULT_DAY_CUTOFF_HOUR = 4;

/**
 * 校验复习日切点小时：null 或空串表示未设置（落库为 NULL，按 04:00 处理）；填写的值必须是
 * 0–23 的整数（0 表示午夜）。与 Go: internal/store/stats.go 的 ResolveCutoff 同一口径。
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

/**
 * 统一验证个人资料表单
 */
export function validateProfileForm(form: {
  display_name: string | null | undefined;
  locale: string | null | undefined;
  timezone: string | null | undefined;
  day_cutoff_hour: number | string | null | undefined;
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
    },
  };
}
